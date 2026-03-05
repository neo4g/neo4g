package storage

// node.go defines the Node type and its associated data structures.
//
// A Node represents a vertex in the property graph. Each node has:
//   - A unique NodeID (monotonically increasing, assigned by the disk manager)
//   - Zero or more labels (strings like "Person", "Company")
//   - Zero or more properties (key-value pairs with typed values)
//
// Nodes are stored in node-typed slotted pages and indexed by NodeID via B+Tree.
// Adjacency (which edges connect to this node) is tracked in a separate
// adjacency index, not stored inline on the node record — this keeps node
// records compact and avoids expensive updates when edges are added/removed.

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

// IntValue creates a Value holding a 64-bit signed integer.
func IntValue(v int64) Value {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v))
	return Value{Type: ValueTypeInt, Data: buf}
}

// FloatValue creates a Value holding a 64-bit IEEE 754 float.
func FloatValue(v float64) Value {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, math.Float64bits(v))
	return Value{Type: ValueTypeFloat, Data: buf}
}

// StringValue creates a Value holding a UTF-8 string.
func StringValue(v string) Value {
	return Value{Type: ValueTypeString, Data: []byte(v)}
}

// BoolValue creates a Value holding a boolean.
func BoolValue(v bool) Value {
	b := byte(0)
	if v {
		b = 1
	}
	return Value{Type: ValueTypeBool, Data: []byte{b}}
}

// BytesValue creates a Value holding arbitrary bytes.
func BytesValue(v []byte) Value {
	cp := make([]byte, len(v))
	copy(cp, v)
	return Value{Type: ValueTypeBytes, Data: cp}
}

// NullValue creates a Value representing null/no value.
func NullValue() Value {
	return Value{Type: ValueTypeNull, Data: nil}
}

// --- Value accessors ---

// AsInt returns the value as int64. Panics if the type is not ValueTypeInt.
func (v Value) AsInt() int64 {
	if v.Type != ValueTypeInt {
		panic("Value.AsInt called on non-int value")
	}
	return int64(binary.LittleEndian.Uint64(v.Data))
}

// AsFloat returns the value as float64. Panics if type is not ValueTypeFloat.
func (v Value) AsFloat() float64 {
	if v.Type != ValueTypeFloat {
		panic("Value.AsFloat called on non-float value")
	}
	return math.Float64frombits(binary.LittleEndian.Uint64(v.Data))
}

// AsString returns the value as a string. Panics if type is not ValueTypeString.
func (v Value) AsString() string {
	if v.Type != ValueTypeString {
		panic("Value.AsString called on non-string value")
	}
	return string(v.Data)
}

// AsBool returns the value as bool. Panics if type is not ValueTypeBool.
func (v Value) AsBool() bool {
	if v.Type != ValueTypeBool {
		panic("Value.AsBool called on non-bool value")
	}
	return v.Data[0] != 0
}

// AsBytes returns the value as a byte slice. Panics if type is not ValueTypeBytes.
func (v Value) AsBytes() []byte {
	if v.Type != ValueTypeBytes {
		panic("Value.AsBytes called on non-bytes value")
	}
	return v.Data
}
