// Package encoding implements binary serialization for Node and Edge records.
package encoding

import (
	"encoding/binary"
	"fmt"

	"github.com/neo4g/neo4g/graph"
)

// EncodeNode serializes a Node into a byte slice for storage in a slotted page.
func EncodeNode(n *graph.Node) []byte {
	size := 8 + 2 // NodeID + LabelCount
	for _, l := range n.Labels {
		size += 2 + len(l)
	}
	size += 2
	for k, v := range n.Properties {
		size += 2 + len(k) + 1 + 2 + len(v.Data)
	}

	buf := make([]byte, size)
	off := 0

	binary.LittleEndian.PutUint64(buf[off:], uint64(n.ID))
	off += 8

	binary.LittleEndian.PutUint16(buf[off:], uint16(len(n.Labels)))
	off += 2
	for _, l := range n.Labels {
		binary.LittleEndian.PutUint16(buf[off:], uint16(len(l)))
		off += 2
		copy(buf[off:], l)
		off += len(l)
	}

	off = encodeProperties(buf, off, n.Properties)
	return buf[:off]
}

// DecodeNode deserializes a Node from a byte slice.
func DecodeNode(data []byte) (*graph.Node, error) {
	if len(data) < 10 {
		return nil, fmt.Errorf("node record too short: %d bytes", len(data))
	}

	n := &graph.Node{}
	off := 0

	n.ID = graph.NodeID(binary.LittleEndian.Uint64(data[off:]))
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
func EncodeEdge(e *graph.Edge) []byte {
	size := 8 + 2 + len(e.Type) + 8 + 8 + 2
	for k, v := range e.Properties {
		size += 2 + len(k) + 1 + 2 + len(v.Data)
	}

	buf := make([]byte, size)
	off := 0

	binary.LittleEndian.PutUint64(buf[off:], uint64(e.ID))
	off += 8

	binary.LittleEndian.PutUint16(buf[off:], uint16(len(e.Type)))
	off += 2
	copy(buf[off:], e.Type)
	off += len(e.Type)

	binary.LittleEndian.PutUint64(buf[off:], uint64(e.SourceID))
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], uint64(e.TargetID))
	off += 8

	off = encodeProperties(buf, off, e.Properties)
	return buf[:off]
}

// DecodeEdge deserializes an Edge from a byte slice.
func DecodeEdge(data []byte) (*graph.Edge, error) {
	if len(data) < 26 {
		return nil, fmt.Errorf("edge record too short: %d bytes", len(data))
	}

	e := &graph.Edge{}
	off := 0

	e.ID = graph.EdgeID(binary.LittleEndian.Uint64(data[off:]))
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
	e.SourceID = graph.NodeID(binary.LittleEndian.Uint64(data[off:]))
	off += 8
	e.TargetID = graph.NodeID(binary.LittleEndian.Uint64(data[off:]))
	off += 8

	var err error
	e.Properties, off, err = decodeProperties(data, off)
	if err != nil {
		return nil, fmt.Errorf("edge properties: %w", err)
	}

	return e, nil
}

// --- Shared property encoding ---

func encodeProperties(buf []byte, off int, props map[string]graph.Value) int {
	binary.LittleEndian.PutUint16(buf[off:], uint16(len(props)))
	off += 2

	for k, v := range props {
		binary.LittleEndian.PutUint16(buf[off:], uint16(len(k)))
		off += 2
		copy(buf[off:], k)
		off += len(k)

		buf[off] = byte(v.Type)
		off++

		binary.LittleEndian.PutUint16(buf[off:], uint16(len(v.Data)))
		off += 2
		copy(buf[off:], v.Data)
		off += len(v.Data)
	}
	return off
}

func decodeProperties(data []byte, off int) (map[string]graph.Value, int, error) {
	if off+2 > len(data) {
		return nil, off, fmt.Errorf("truncated property count")
	}

	count := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2

	props := make(map[string]graph.Value, count)
	for i := range count {
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

		if off+1 > len(data) {
			return nil, off, fmt.Errorf("truncated property %d value type", i)
		}
		vType := graph.ValueType(data[off])
		off++

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

		props[key] = graph.Value{Type: vType, Data: vData}
	}

	return props, off, nil
}
