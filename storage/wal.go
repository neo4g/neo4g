package storage

// wal.go implements the Write-Ahead Log (WAL) for crash recovery.
//
// The WAL ensures durability and atomicity by logging all page modifications
// before they are written to the data file. On crash recovery, the WAL is
// replayed to restore the database to a consistent state.
//
// Protocol:
//   1. Before a dirty page is written to the data file, its WAL record must
//      be flushed to disk (the "write-ahead" guarantee).
//   2. A transaction's changes become durable only when its COMMIT record
//      is flushed to the WAL.
//   3. On recovery, committed transactions are redone; uncommitted ones are
//      undone by restoring the before-image from the WAL.
//
// WAL record format:
//
//	[LSN:8][TxID:8][Type:1][PageID:4][DataLen:4][BeforeImage...][AfterImage...]
//
// Record types:
//   - UPDATE: contains before-image and after-image of a page
//   - COMMIT: marks a transaction as committed (no page data)
//   - ABORT:  marks a transaction as aborted (no page data)
//   - CHECKPOINT: all dirty pages have been flushed; WAL can be truncated before this point
//
// Design choices:
//   - Full page images (not deltas) for simplicity. This uses more space but
//     makes recovery straightforward — just overwrite the page.
//   - Sequential append-only writes for high throughput.
//   - File is pre-allocated in chunks to reduce fragmentation.

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

// WAL record types.
const (
	WALRecordUpdate     byte = 1
	WALRecordCommit     byte = 2
	WALRecordAbort      byte = 3
	WALRecordCheckpoint byte = 4
)

// WAL record header size: LSN(8) + TxID(8) + Type(1) + PageID(4) + DataLen(4) = 25 bytes.
const walRecordHeaderSize = 25

// LSN (Log Sequence Number) uniquely identifies a WAL record.
// Monotonically increasing.
type LSN uint64

// WALRecord represents a single entry in the write-ahead log.
type WALRecord struct {
	LSN        LSN
	TxID       TxID
	Type       byte
	PageID     PageID
	BeforeData []byte // full page before-image (for undo)
	AfterData  []byte // full page after-image (for redo)
}

// WAL manages the write-ahead log file.
//
// Thread safety: WAL is safe for concurrent use. All operations are
// serialized by a mutex.
type WAL struct {
	mu      sync.Mutex
	file    *os.File
	nextLSN LSN
}

// OpenWAL opens or creates a WAL file at the given path.
func OpenWAL(path string) (*WAL, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open WAL file: %w", err)
	}

	// Determine next LSN by scanning to end of file.
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	w := &WAL{
		file:    file,
		nextLSN: 1,
	}

	// If file has content, scan to find the last LSN.
	if info.Size() > 0 {
		lastLSN, err := w.findLastLSN()
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("scan WAL: %w", err)
		}
		w.nextLSN = lastLSN + 1
	}

	return w, nil
}

// Close closes the WAL file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// LogUpdate writes an UPDATE record for a page modification.
// Returns the LSN of the written record.
func (w *WAL) LogUpdate(txID TxID, pageID PageID, before, after *Page) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rec := WALRecord{
		LSN:        w.nextLSN,
		TxID:       txID,
		Type:       WALRecordUpdate,
		PageID:     pageID,
		BeforeData: before[:],
		AfterData:  after[:],
	}

	if err := w.writeRecord(&rec); err != nil {
		return 0, err
	}

	lsn := w.nextLSN
	w.nextLSN++
	return lsn, nil
}

// LogCommit writes a COMMIT record for a transaction.
func (w *WAL) LogCommit(txID TxID) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rec := WALRecord{
		LSN:  w.nextLSN,
		TxID: txID,
		Type: WALRecordCommit,
	}

	if err := w.writeRecord(&rec); err != nil {
		return 0, err
	}

	lsn := w.nextLSN
	w.nextLSN++
	return lsn, nil
}

// LogAbort writes an ABORT record for a transaction.
func (w *WAL) LogAbort(txID TxID) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rec := WALRecord{
		LSN:  w.nextLSN,
		TxID: txID,
		Type: WALRecordAbort,
	}

	if err := w.writeRecord(&rec); err != nil {
		return 0, err
	}

	lsn := w.nextLSN
	w.nextLSN++
	return lsn, nil
}

// LogCheckpoint writes a CHECKPOINT record indicating all dirty pages
// have been flushed to disk.
func (w *WAL) LogCheckpoint() (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rec := WALRecord{
		LSN:  w.nextLSN,
		Type: WALRecordCheckpoint,
	}

	if err := w.writeRecord(&rec); err != nil {
		return 0, err
	}

	lsn := w.nextLSN
	w.nextLSN++
	return lsn, nil
}

// Sync flushes the WAL file to durable storage.
// Must be called after writing COMMIT records to ensure durability.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Sync()
}

// ReadAll reads all WAL records from the beginning of the file.
// Used during crash recovery.
func (w *WAL) ReadAll() ([]WALRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	var records []WALRecord
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
	return records, nil
}

// Truncate removes all WAL records, resetting the file.
// Called after a successful checkpoint when all data is safely on disk.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Truncate(0); err != nil {
		return err
	}
	_, err := w.file.Seek(0, io.SeekStart)
	return err
}

// --- Internal helpers ---

// writeRecord appends a WAL record to the file.
func (w *WAL) writeRecord(rec *WALRecord) error {
	// Calculate data length.
	dataLen := len(rec.BeforeData) + len(rec.AfterData)

	// Write header.
	header := make([]byte, walRecordHeaderSize)
	binary.LittleEndian.PutUint64(header[0:], uint64(rec.LSN))
	binary.LittleEndian.PutUint64(header[8:], uint64(rec.TxID))
	header[16] = rec.Type
	binary.LittleEndian.PutUint32(header[17:], uint32(rec.PageID))
	binary.LittleEndian.PutUint32(header[21:], uint32(dataLen))

	if _, err := w.file.Write(header); err != nil {
		return err
	}

	// Write before-image.
	if len(rec.BeforeData) > 0 {
		if _, err := w.file.Write(rec.BeforeData); err != nil {
			return err
		}
	}

	// Write after-image.
	if len(rec.AfterData) > 0 {
		if _, err := w.file.Write(rec.AfterData); err != nil {
			return err
		}
	}

	return nil
}

// readRecord reads one WAL record from the current file position.
func (w *WAL) readRecord() (WALRecord, error) {
	header := make([]byte, walRecordHeaderSize)
	if _, err := io.ReadFull(w.file, header); err != nil {
		return WALRecord{}, err
	}

	rec := WALRecord{
		LSN:    LSN(binary.LittleEndian.Uint64(header[0:])),
		TxID:   TxID(binary.LittleEndian.Uint64(header[8:])),
		Type:   header[16],
		PageID: PageID(binary.LittleEndian.Uint32(header[17:])),
	}

	dataLen := int(binary.LittleEndian.Uint32(header[21:]))
	if dataLen > 0 {
		if rec.Type == WALRecordUpdate {
			// Half is before-image, half is after-image (both full pages).
			rec.BeforeData = make([]byte, PageSize)
			if _, err := io.ReadFull(w.file, rec.BeforeData); err != nil {
				return rec, err
			}
			rec.AfterData = make([]byte, PageSize)
			if _, err := io.ReadFull(w.file, rec.AfterData); err != nil {
				return rec, err
			}
		} else {
			// Skip unexpected data for non-UPDATE records.
			buf := make([]byte, dataLen)
			if _, err := io.ReadFull(w.file, buf); err != nil {
				return rec, err
			}
		}
	}

	return rec, nil
}

// findLastLSN scans the WAL to find the highest LSN.
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
