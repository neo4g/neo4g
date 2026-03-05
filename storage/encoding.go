package storage

// encoding.go implements binary serialization for Node and Edge records.
//
// Format is designed for compact storage and fast sequential deserialization:
//   - Integers are fixed-width little-endian (no varint complexity for IDs)
//   - Strings are length-prefixed (uint16 length + raw bytes)
//   - Counts use uint16 (sufficient for labels/properties per record)
//
// Node binary format:
//
//	[NodeID:8][LabelCount:2][Labels...][PropCount:2][Props...]
//	  where Label = [Len:2][Bytes...]
//	  where Prop  = [KeyLen:2][Key...][ValueType:1][DataLen:2][Data...]
//
// Edge binary format:
//
//	[EdgeID:8][TypeLen:2][Type...][SourceID:8][TargetID:8][PropCount:2][Props...]
//	  where Prop = same as node
//
// Why custom binary encoding instead of protobuf/JSON:
//   - Zero allocation overhead on decode (read directly from byte slice)
//   - No schema registry or code generation needed
//   - Compact: no field tags, no padding, no structural overhead
//   - A database storage engine needs full control over byte layout

import (
	"encoding/binary"
	"fmt"
)

// EncodeNode serializes a Node into a byte slice for storage in a slotted page.
func EncodeNode(n *Node) []byte {
	// Calculate total size.
	size := 8 + 2 // NodeID + LabelCount
	for _, l := range n.Labels {
		size += 2 + len(l) // len prefix + string
	}
	size += 2 // PropCount
	for k, v := range n.Properties {
		size += 2 + len(k) + 1 + 2 + len(v.Data) // key + type + data
	}

	buf := make([]byte, size)
	off := 0

	// NodeID
	binary.LittleEndian.PutUint64(buf[off:], uint64(n.ID))
	off += 8

	// Labels
	binary.LittleEndian.PutUint16(buf[off:], uint16(len(n.Labels)))
	off += 2
	for _, l := range n.Labels {
		binary.LittleEndian.PutUint16(buf[off:], uint16(len(l)))
		off += 2
		copy(buf[off:], l)
		off += len(l)
	}

	// Properties
	off = encodeProperties(buf, off, n.Properties)
	return buf[:off]
}

// DecodeNode deserializes a Node from a byte slice.
// Returns an error if the data is malformed or truncated.
func DecodeNode(data []byte) (*Node, error) {
	if len(data) < 10 { // minimum: 8 (ID) + 2 (label count)
		return nil, fmt.Errorf("node record too short: %d bytes", len(data))
	}

	n := &Node{}
	off := 0

	n.ID = NodeID(binary.LittleEndian.Uint64(data[off:]))
	off += 8

	labelCount := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2
	n.Labels = make([]string, labelCount)
	for i := range labelCount {
		if off+2 > len(data) {
			return nil, fmt.Errorf("truncated label %d", i)
		}
		l := int(binary.LittleEndian.Uint16(data[off:]))
		off += 2
		if off+l > len(data) {
			return nil, fmt.Errorf("truncated label %d data", i)
		}
		n.Labels[i] = string(data[off : off+l])
		off += l
	}

	var err error
	n.Properties, off, err = decodeProperties(data, off)
	if err != nil {
		return nil, fmt.Errorf("node properties: %w", err)
	}

	return n, nil
}

// EncodeEdge serializes an Edge into a byte slice for storage in a slotted page.
func EncodeEdge(e *Edge) []byte {
	size := 8 + 2 + len(e.Type) + 8 + 8 + 2 // EdgeID + Type + SourceID + TargetID + PropCount
	for k, v := range e.Properties {
		size += 2 + len(k) + 1 + 2 + len(v.Data)
	}

	buf := make([]byte, size)
	off := 0

	// EdgeID
	binary.LittleEndian.PutUint64(buf[off:], uint64(e.ID))
	off += 8

	// Type
	binary.LittleEndian.PutUint16(buf[off:], uint16(len(e.Type)))
	off += 2
	copy(buf[off:], e.Type)
	off += len(e.Type)

	// SourceID, TargetID
	binary.LittleEndian.PutUint64(buf[off:], uint64(e.SourceID))
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], uint64(e.TargetID))
	off += 8

	// Properties
	off = encodeProperties(buf, off, e.Properties)
	return buf[:off]
}

// DecodeEdge deserializes an Edge from a byte slice.
func DecodeEdge(data []byte) (*Edge, error) {
	if len(data) < 26 { // 8 + 2 + 0 + 8 + 8 = minimum
		return nil, fmt.Errorf("edge record too short: %d bytes", len(data))
	}

	e := &Edge{}
	off := 0

	e.ID = EdgeID(binary.LittleEndian.Uint64(data[off:]))
	off += 8

	typeLen := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2
	if off+typeLen > len(data) {
		return nil, fmt.Errorf("truncated edge type")
	}
	e.Type = string(data[off : off+typeLen])
	off += typeLen

	if off+16 > len(data) {
		return nil, fmt.Errorf("truncated source/target IDs")
	}
	e.SourceID = NodeID(binary.LittleEndian.Uint64(data[off:]))
	off += 8
	e.TargetID = NodeID(binary.LittleEndian.Uint64(data[off:]))
	off += 8

	var err error
	e.Properties, off, err = decodeProperties(data, off)
	if err != nil {
		return nil, fmt.Errorf("edge properties: %w", err)
	}

	return e, nil
}

// --- Shared property encoding ---

// encodeProperties writes a property map into buf at the given offset.
// Returns the new offset after writing.
func encodeProperties(buf []byte, off int, props map[string]Value) int {
	binary.LittleEndian.PutUint16(buf[off:], uint16(len(props)))
	off += 2

	for k, v := range props {
		// Key
		binary.LittleEndian.PutUint16(buf[off:], uint16(len(k)))
		off += 2
		copy(buf[off:], k)
		off += len(k)

		// Value type
		buf[off] = byte(v.Type)
		off++

		// Value data
		binary.LittleEndian.PutUint16(buf[off:], uint16(len(v.Data)))
		off += 2
		copy(buf[off:], v.Data)
		off += len(v.Data)
	}
	return off
}

// decodeProperties reads a property map from data at the given offset.
// Returns the map, new offset, and any error.
func decodeProperties(data []byte, off int) (map[string]Value, int, error) {
	if off+2 > len(data) {
		return nil, off, fmt.Errorf("truncated property count")
	}

	count := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2

	props := make(map[string]Value, count)
	for i := range count {
		// Key
		if off+2 > len(data) {
			return nil, off, fmt.Errorf("truncated property %d key length", i)
		}
		kLen := int(binary.LittleEndian.Uint16(data[off:]))
		off += 2
		if off+kLen > len(data) {
			return nil, off, fmt.Errorf("truncated property %d key", i)
		}
		key := string(data[off : off+kLen])
		off += kLen

		// Value type
		if off+1 > len(data) {
			return nil, off, fmt.Errorf("truncated property %d value type", i)
		}
		vType := ValueType(data[off])
		off++

		// Value data
		if off+2 > len(data) {
			return nil, off, fmt.Errorf("truncated property %d data length", i)
		}
		dLen := int(binary.LittleEndian.Uint16(data[off:]))
		off += 2
		if off+dLen > len(data) {
			return nil, off, fmt.Errorf("truncated property %d data", i)
		}
		vData := make([]byte, dLen)
		copy(vData, data[off:off+dLen])
		off += dLen

		props[key] = Value{Type: vType, Data: vData}
	}

	return props, off, nil
}
