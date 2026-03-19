package query

import "github.com/seankohjs/neo4g/graph"

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
func GoToValue(v interface{}) graph.Value {
	switch val := v.(type) {
	case int64:
		return graph.IntValue(val)
	case int:
		return graph.IntValue(int64(val))
	case float64:
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
