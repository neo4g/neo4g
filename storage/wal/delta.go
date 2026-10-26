package wal

import (
	"encoding/binary"

	"github.com/neo4g/neo4g/storage"
)

// Bidirectional delta format — each chunk stores both before and after bytes
// so the delta can be applied in either direction (redo or undo).
//
// Chunk: skip(uint16) + length(uint16) + before[length] + after[length]
//
// skip  = number of identical bytes to skip from current position
// length = number of changed bytes
// before = original bytes at that position (for undo)
// after  = new bytes at that position (for redo)

// EncodeDelta produces a compact bidirectional diff between before and after pages.
func EncodeDelta(before, after *storage.Page) []byte {
	var buf []byte
	i := 0
	n := storage.PageSize

	for i < n {
		skip := 0
		for i+skip < n && before[i+skip] == after[i+skip] {
			skip++
		}
		i += skip
		if i >= n {
			break
		}

		start := i
		for i < n && before[i] != after[i] {
			i++
		}
		length := i - start

		for skip > 0 || length > 0 {
			s := skip
			if s > 0xFFFF {
				s = 0xFFFF
			}
			l := length
			if l > 0xFFFF {
				l = 0xFFFF
			}

			var hdr [4]byte
			binary.LittleEndian.PutUint16(hdr[0:], uint16(s))
			binary.LittleEndian.PutUint16(hdr[2:], uint16(l))
			buf = append(buf, hdr[:]...)
			buf = append(buf, before[start:start+l]...)
			buf = append(buf, after[start:start+l]...)

			skip -= s
			start += l
			length -= l
		}
	}
	return buf
}

// ApplyDelta applies a forward delta (before -> after).
func ApplyDelta(before *storage.Page, delta []byte) storage.Page {
	var out storage.Page
	copy(out[:], before[:])

	pos := 0
	off := 0
	for off+4 <= len(delta) {
		skip := int(binary.LittleEndian.Uint16(delta[off:]))
		length := int(binary.LittleEndian.Uint16(delta[off+2:]))
		off += 4
		pos += skip
		if length > 0 && off+2*length <= len(delta) {
			off += length // skip before-bytes
			copy(out[pos:pos+length], delta[off:off+length])
			off += length // skip after-bytes
			pos += length
		}
	}
	return out
}

// ApplyDeltaUndo reverses a delta (after -> before).
func ApplyDeltaUndo(after *storage.Page, delta []byte) storage.Page {
	var out storage.Page
	copy(out[:], after[:])

	pos := 0
	off := 0
	for off+4 <= len(delta) {
		skip := int(binary.LittleEndian.Uint16(delta[off:]))
		length := int(binary.LittleEndian.Uint16(delta[off+2:]))
		off += 4
		pos += skip
		if length > 0 && off+2*length <= len(delta) {
			copy(out[pos:pos+length], delta[off:off+length])
			off += 2 * length // skip both before and after
			pos += length
		}
	}
	return out
}
