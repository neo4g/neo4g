package query

import (
	"math"

	"github.com/neo4g/neo4g/graph"
)

// Row holds variable bindings for a single matched result.
// Keys are variable names; values are *graph.Node, *graph.Edge, or primitives.
type Row map[string]interface{}

// Result is the output of query execution.
type Result struct {
	Columns []string  `json:"columns"`
	Rows    []RowData `json:"rows"`
}

// RowData is a JSON-friendly row: column name -> serialized value.
type RowData map[string]interface{}

// --- graph.Value <-> Go primitive bridging ---

// ValueToGo converts a graph.Value to a Go primitive for expression evaluation.
func ValueToGo(v graph.Value) interface{} {
	switch v.Type {
	case graph.ValueTypeInt:
		return v.AsInt()
	case graph.ValueTypeFloat:
		return v.AsFloat()
	case graph.ValueTypeString:
		return v.AsString()
	case graph.ValueTypeBool:
		return v.AsBool()
	case graph.ValueTypeNull:
		return nil
	default:
		return v.Data
	}
}

// GoToValue converts a Go primitive (from AST literals / params) to graph.Value.
// Integral float64 values (JSON numbers) are stored as ints so they compare
// equal to int64 properties.
func GoToValue(v interface{}) graph.Value {
	switch val := v.(type) {
	case int64:
		return graph.IntValue(val)
	case int:
		return graph.IntValue(int64(val))
	case float64:
		if i, ok := integralInt64(val); ok {
			return graph.IntValue(i)
		}
		return graph.FloatValue(val)
	case string:
		return graph.StringValue(val)
	case bool:
		return graph.BoolValue(val)
	case nil:
		return graph.NullValue()
	default:
		return graph.NullValue()
	}
}

// normalizeParams copies params, converting integral float64 values to int64.
// JSON object numbers arrive as float64; stored integers are int64.
func normalizeParams(params map[string]interface{}) map[string]interface{} {
	if len(params) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(params))
	for k, v := range params {
		out[k] = normalizeScalar(v)
	}
	return out
}

// normalizeScalar converts an integral float64 to int64. Other values are unchanged.
func normalizeScalar(v interface{}) interface{} {
	f, ok := v.(float64)
	if !ok {
		return v
	}
	if i, ok := integralInt64(f); ok {
		return i
	}
	return f
}

// integralInt64 reports whether f is a finite whole number that fits in int64.
func integralInt64(f float64) (int64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, false
	}
	// 2^63 is the first integer that does not fit in int64. It is exactly
	// representable as float64; MaxInt64 itself is not.
	if f >= 9223372036854775808.0 || f < -9223372036854775808.0 {
		return 0, false
	}
	i := int64(f)
	if float64(i) != f {
		return 0, false
	}
	return i, true
}

// NodeToMap converts a *graph.Node to a JSON-friendly map.
func NodeToMap(n *graph.Node) map[string]interface{} {
	props := make(map[string]interface{}, len(n.Properties))
	for k, v := range n.Properties {
		props[k] = ValueToGo(v)
	}
	return map[string]interface{}{
		"id":         uint64(n.ID),
		"labels":     n.Labels,
		"properties": props,
	}
}

// EdgeToMap converts a *graph.Edge to a JSON-friendly map.
func EdgeToMap(e *graph.Edge) map[string]interface{} {
	props := make(map[string]interface{}, len(e.Properties))
	for k, v := range e.Properties {
		props[k] = ValueToGo(v)
	}
	return map[string]interface{}{
		"id":         uint64(e.ID),
		"type":       e.Type,
		"source":     uint64(e.SourceID),
		"target":     uint64(e.TargetID),
		"properties": props,
	}
}
