package graph

import (
	"encoding/binary"
	"math"
)

// NodeID uniquely identifies a node in the database.
// IDs start at 1; 0 is reserved as a sentinel meaning "no node".
type NodeID uint64

// Node represents a vertex in the property graph.
type Node struct {
	ID         NodeID
	Labels     []string
	Properties map[string]Value
}

// EdgeID uniquely identifies an edge in the database.
// IDs start at 1; 0 is reserved as a sentinel meaning "no edge".
type EdgeID uint64

// Edge represents a directed relationship between two nodes in the property graph.
type Edge struct {
	ID         EdgeID
	Type       string
	SourceID   NodeID
	TargetID   NodeID
	Properties map[string]Value
}

// Direction specifies which edges to retrieve relative to a node.
type Direction int

const (
	Outgoing Direction = iota
	Incoming
	Both
)

// ValueType identifies the type of data stored in a Value.
type ValueType byte

const (
	ValueTypeNull   ValueType = 0
	ValueTypeInt    ValueType = 1
	ValueTypeFloat  ValueType = 2
	ValueTypeString ValueType = 3
	ValueTypeBool   ValueType = 4
	ValueTypeBytes  ValueType = 5
)

// Value is a typed property value. It stores the raw bytes alongside a type
// tag for correct deserialization. Use the helper constructors (IntValue,
// StringValue, etc.) to create Values.
type Value struct {
	Type ValueType
	Data []byte
}

// --- Value constructors ---

func IntValue(v int64) Value {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v))
	return Value{Type: ValueTypeInt, Data: buf}
}

func FloatValue(v float64) Value {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, math.Float64bits(v))
	return Value{Type: ValueTypeFloat, Data: buf}
}

func StringValue(v string) Value {
	return Value{Type: ValueTypeString, Data: []byte(v)}
}

func BoolValue(v bool) Value {
	b := byte(0)
	if v {
		b = 1
	}
	return Value{Type: ValueTypeBool, Data: []byte{b}}
}

func BytesValue(v []byte) Value {
	cp := make([]byte, len(v))
	copy(cp, v)
	return Value{Type: ValueTypeBytes, Data: cp}
}

func NullValue() Value {
	return Value{Type: ValueTypeNull, Data: nil}
}

// --- Value accessors ---

func (v Value) AsInt() int64 {
	if v.Type != ValueTypeInt {
		panic("Value.AsInt called on non-int value")
	}
	return int64(binary.LittleEndian.Uint64(v.Data))
}

func (v Value) AsFloat() float64 {
	if v.Type != ValueTypeFloat {
		panic("Value.AsFloat called on non-float value")
	}
	return math.Float64frombits(binary.LittleEndian.Uint64(v.Data))
}

func (v Value) AsString() string {
	if v.Type != ValueTypeString {
		panic("Value.AsString called on non-string value")
	}
	return string(v.Data)
}

func (v Value) AsBool() bool {
	if v.Type != ValueTypeBool {
		panic("Value.AsBool called on non-bool value")
	}
	return v.Data[0] != 0
}

func (v Value) AsBytes() []byte {
	if v.Type != ValueTypeBytes {
		panic("Value.AsBytes called on non-bytes value")
	}
	return v.Data
}
