// Package wal implements the Write-Ahead Log for crash recovery.
//
// The WAL ensures durability and atomicity by logging all page modifications
// before they are written to the data file.
package wal

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/seankohjs/neo4g/storage"
)

// Record types.
const (
	RecordUpdate      byte = 1
	RecordCommit      byte = 2
	RecordAbort       byte = 3
	RecordCheckpoint  byte = 4
	RecordUpdateDelta byte = 5 // delta-compressed page update
)

const recordHeaderSize = 25 // LSN(8) + TxID(8) + Type(1) + PageID(4) + DataLen(4)

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
}

// Open opens or creates a WAL file at the given path.
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
	}

	if info.Size() > 0 {
		lastLSN, err := w.findLastLSN()
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("scan WAL: %w", err)
		}
		w.nextLSN = lastLSN + 1
	}

	// Seek to end so buffered writes append correctly
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		file.Close()
		return nil, err
	}
	w.buf = bufio.NewWriter(file)

	return w, nil
}

// Close closes the WAL file.
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

// LogCheckpoint writes a CHECKPOINT record.
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

// Sync flushes the WAL file to durable storage.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.buf.Flush(); err != nil {
		return err
	}
	return w.file.Sync()
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
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return records, fmt.Errorf("read WAL record: %w", err)
		}
		records = append(records, rec)
	}

	// Seek back to end so subsequent buffered writes append correctly
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return records, err
	}
	w.buf.Reset(w.file)

	return records, nil
}

// Truncate removes all WAL records, resetting the file.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Truncate(0); err != nil {
		return err
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.buf.Reset(w.file) // reset buffer to point at truncated file
	return nil
}

// --- Internal helpers ---

func (w *WAL) writeRecord(rec *Record) error {
	var dataLen int
	switch rec.Type {
	case RecordUpdateDelta:
		dataLen = len(rec.DeltaData)
	case RecordUpdate:
		dataLen = len(rec.BeforeData) + len(rec.AfterData)
	}

	header := make([]byte, recordHeaderSize)
	binary.LittleEndian.PutUint64(header[0:], uint64(rec.LSN))
	binary.LittleEndian.PutUint64(header[8:], uint64(rec.TxID))
	header[16] = rec.Type
	binary.LittleEndian.PutUint32(header[17:], uint32(rec.PageID))
	binary.LittleEndian.PutUint32(header[21:], uint32(dataLen))

	if _, err := w.buf.Write(header); err != nil {
		return err
	}

	switch rec.Type {
	case RecordUpdateDelta:
		if len(rec.DeltaData) > 0 {
			if _, err := w.buf.Write(rec.DeltaData); err != nil {
				return err
			}
		}
	case RecordUpdate:
		if len(rec.BeforeData) > 0 {
			if _, err := w.buf.Write(rec.BeforeData); err != nil {
				return err
			}
		}
		if len(rec.AfterData) > 0 {
			if _, err := w.buf.Write(rec.AfterData); err != nil {
				return err
			}
		}
	}

	return nil
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
	if dataLen > 0 {
		switch rec.Type {
		case RecordUpdate:
			rec.BeforeData = make([]byte, storage.PageSize)
			if _, err := io.ReadFull(w.file, rec.BeforeData); err != nil {
				return rec, err
			}
			rec.AfterData = make([]byte, storage.PageSize)
			if _, err := io.ReadFull(w.file, rec.AfterData); err != nil {
				return rec, err
			}
		case RecordUpdateDelta:
			rec.DeltaData = make([]byte, dataLen)
			if _, err := io.ReadFull(w.file, rec.DeltaData); err != nil {
				return rec, err
			}
		default:
			buf := make([]byte, dataLen)
			if _, err := io.ReadFull(w.file, buf); err != nil {
				return rec, err
			}
		}
	}

	return rec, nil
}

func (w *WAL) findLastLSN() (LSN, error) {
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	var lastLSN LSN
	for {
		rec, err := w.readRecord()
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return lastLSN, err
		}
		lastLSN = rec.LSN
	}
	return lastLSN, nil
}
