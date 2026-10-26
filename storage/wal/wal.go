// Package wal implements the Write-Ahead Log for crash recovery.
//
// The WAL ensures durability and atomicity by logging all page modifications
// before they are written to the data file. Each record carries a CRC so a
// torn tail can be discarded on open. Log records stay buffered until Force
// or Sync; callers must Force before writing a data page (group commit).
package wal

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"

	"github.com/neo4g/neo4g/storage"
)

// Record types.
const (
	RecordUpdate      byte = 1
	RecordCommit      byte = 2
	RecordAbort       byte = 3
	RecordCheckpoint  byte = 4
	RecordUpdateDelta byte = 5 // delta-compressed page update
)

const (
	recordHeaderSize = 25 // LSN(8) + TxID(8) + Type(1) + PageID(4) + DataLen(4)
	checksumSize     = 4
)

// ErrChecksum is returned when a record's CRC does not match its bytes.
var ErrChecksum = errors.New("wal checksum mismatch")

// LSN (Log Sequence Number) uniquely identifies a WAL record.
type LSN uint64

// Record represents a single entry in the write-ahead log.
type Record struct {
	LSN        LSN
	TxID       storage.TxID
	Type       byte
	PageID     storage.PageID
	BeforeData []byte // full before-image (RecordUpdate only)
	AfterData  []byte // full after-image (RecordUpdate only)
	DeltaData  []byte // compressed delta (RecordUpdateDelta only)
}

// WAL manages the write-ahead log file.
//
// Thread safety: WAL is safe for concurrent use.
type WAL struct {
	mu      sync.Mutex
	file    *os.File
	buf     *bufio.Writer
	nextLSN LSN
	durable bool // buffered records have been flushed and fsynced
}

// Open opens or creates a WAL file at the given path.
// A partial record at the tail is truncated back to the last valid CRC.
func Open(path string) (*WAL, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open WAL file: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	w := &WAL{
		file:    file,
		nextLSN: 1,
		durable: true,
	}

	if info.Size() > 0 {
		lastLSN, err := w.truncateTornTail()
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("scan WAL: %w", err)
		}
		if lastLSN > 0 {
			w.nextLSN = lastLSN + 1
		}
	}

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		file.Close()
		return nil, err
	}
	w.buf = bufio.NewWriter(file)

	return w, nil
}

// Close flushes the WAL buffer and closes the underlying file.
// Close does not fsync; Sync or Force makes records durable.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.buf.Flush(); err != nil {
		w.file.Close()
		return err
	}
	return w.file.Close()
}

// LogUpdate writes a delta-compressed UPDATE record for a page modification.
// The record is buffered. It is not stable storage until Force or Sync.
func (w *WAL) LogUpdate(txID storage.TxID, pageID storage.PageID, before, after *storage.Page) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delta := EncodeDelta(before, after)

	rec := Record{
		LSN:       w.nextLSN,
		TxID:      txID,
		Type:      RecordUpdateDelta,
		PageID:    pageID,
		DeltaData: delta,
	}

	if err := w.writeRecord(&rec); err != nil {
		return 0, err
	}

	lsn := w.nextLSN
	w.nextLSN++
	return lsn, nil
}

// LogCommit writes a COMMIT record for a transaction.
func (w *WAL) LogCommit(txID storage.TxID) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rec := Record{
		LSN:  w.nextLSN,
		TxID: txID,
		Type: RecordCommit,
	}

	if err := w.writeRecord(&rec); err != nil {
		return 0, err
	}

	lsn := w.nextLSN
	w.nextLSN++
	return lsn, nil
}

// LogAbort writes an ABORT record for a transaction.
func (w *WAL) LogAbort(txID storage.TxID) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rec := Record{
		LSN:  w.nextLSN,
		TxID: txID,
		Type: RecordAbort,
	}

	if err := w.writeRecord(&rec); err != nil {
		return 0, err
	}

	lsn := w.nextLSN
	w.nextLSN++
	return lsn, nil
}

// LogCheckpoint writes a CHECKPOINT record. It does not truncate the log.
func (w *WAL) LogCheckpoint() (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rec := Record{
		LSN:  w.nextLSN,
		Type: RecordCheckpoint,
	}

	if err := w.writeRecord(&rec); err != nil {
		return 0, err
	}

	lsn := w.nextLSN
	w.nextLSN++
	return lsn, nil
}

// Force makes every buffered record durable. A second call is a no-op until
// another record is appended, so a group of commits can sync once.
func (w *WAL) Force() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.forceLocked()
}

// Sync flushes the WAL file to durable storage.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.forceLocked()
}

// Durable reports whether every appended record has been fsynced.
func (w *WAL) Durable() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.durable
}

// ReadAll reads all WAL records from the beginning of the file.
func (w *WAL) ReadAll() ([]Record, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.buf.Flush(); err != nil {
		return nil, err
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	var records []Record
	for {
		rec, err := w.readRecord()
		if err == io.EOF {
			break
		}
		if err != nil {
			return records, fmt.Errorf("read WAL record: %w", err)
		}
		records = append(records, rec)
	}

	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return records, err
	}
	w.buf.Reset(w.file)

	return records, nil
}

// NextLSN returns the next LSN that will be assigned to a new record.
func (w *WAL) NextLSN() LSN {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextLSN
}

// ReadFrom reads all WAL records with LSN >= fromLSN.
func (w *WAL) ReadFrom(fromLSN LSN) ([]Record, error) {
	all, err := w.ReadAll()
	if err != nil {
		return nil, err
	}
	var filtered []Record
	for _, r := range all {
		if r.LSN >= fromLSN {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

// Truncate removes all WAL records. The in-memory next LSN is left unchanged
// so a later append does not reuse sequence numbers. Reopening an empty file
// still starts at LSN 1; checkpoint and recovery do not call Truncate.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Truncate(0); err != nil {
		return err
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.buf.Reset(w.file)
	w.durable = true
	return nil
}

// --- Internal helpers ---

func (w *WAL) forceLocked() error {
	if w.durable {
		return nil
	}
	if err := w.buf.Flush(); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	w.durable = true
	return nil
}

func (w *WAL) writeRecord(rec *Record) error {
	payload := recordPayload(rec)

	header := make([]byte, recordHeaderSize)
	binary.LittleEndian.PutUint64(header[0:], uint64(rec.LSN))
	binary.LittleEndian.PutUint64(header[8:], uint64(rec.TxID))
	header[16] = rec.Type
	binary.LittleEndian.PutUint32(header[17:], uint32(rec.PageID))
	binary.LittleEndian.PutUint32(header[21:], uint32(len(payload)))

	sum := crc32.Update(0, crc32.IEEETable, header)
	sum = crc32.Update(sum, crc32.IEEETable, payload)
	var crc [checksumSize]byte
	binary.LittleEndian.PutUint32(crc[:], sum)

	if _, err := w.buf.Write(header); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.buf.Write(payload); err != nil {
			return err
		}
	}
	if _, err := w.buf.Write(crc[:]); err != nil {
		return err
	}
	w.durable = false
	return nil
}

func recordPayload(rec *Record) []byte {
	switch rec.Type {
	case RecordUpdateDelta:
		return rec.DeltaData
	case RecordUpdate:
		buf := make([]byte, 0, len(rec.BeforeData)+len(rec.AfterData))
		buf = append(buf, rec.BeforeData...)
		buf = append(buf, rec.AfterData...)
		return buf
	default:
		return nil
	}
}

func (w *WAL) readRecord() (Record, error) {
	header := make([]byte, recordHeaderSize)
	if _, err := io.ReadFull(w.file, header); err != nil {
		return Record{}, err
	}

	rec := Record{
		LSN:    LSN(binary.LittleEndian.Uint64(header[0:])),
		TxID:   storage.TxID(binary.LittleEndian.Uint64(header[8:])),
		Type:   header[16],
		PageID: storage.PageID(binary.LittleEndian.Uint32(header[17:])),
	}

	dataLen := int(binary.LittleEndian.Uint32(header[21:]))
	payload := make([]byte, dataLen)
	if dataLen > 0 {
		if _, err := io.ReadFull(w.file, payload); err != nil {
			return Record{}, err
		}
	}

	var crcBuf [checksumSize]byte
	if _, err := io.ReadFull(w.file, crcBuf[:]); err != nil {
		return Record{}, err
	}
	sum := crc32.Update(0, crc32.IEEETable, header)
	sum = crc32.Update(sum, crc32.IEEETable, payload)
	if sum != binary.LittleEndian.Uint32(crcBuf[:]) {
		return Record{}, ErrChecksum
	}

	switch rec.Type {
	case RecordUpdate:
		if len(payload) < 2*storage.PageSize {
			return Record{}, fmt.Errorf("short update payload (%d bytes)", len(payload))
		}
		rec.BeforeData = payload[:storage.PageSize]
		rec.AfterData = payload[storage.PageSize : 2*storage.PageSize]
	case RecordUpdateDelta:
		rec.DeltaData = payload
	}
	return rec, nil
}

// truncateTornTail scans to the last complete record and truncates anything
// past it (a torn write or a bad checksum). The file position is left at the
// new end. Returns the last valid LSN, or 0 when the file has no records.
func (w *WAL) truncateTornTail() (LSN, error) {
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	var lastLSN LSN
	goodEnd := int64(0)
	for {
		rec, err := w.readRecord()
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF || errors.Is(err, ErrChecksum) {
			break
		}
		if err != nil {
			return 0, err
		}
		off, err := w.file.Seek(0, io.SeekCurrent)
		if err != nil {
			return 0, err
		}
		lastLSN = rec.LSN
		goodEnd = off
	}

	if err := w.file.Truncate(goodEnd); err != nil {
		return 0, err
	}
	if _, err := w.file.Seek(goodEnd, io.SeekStart); err != nil {
		return 0, err
	}
	return lastLSN, nil
}
