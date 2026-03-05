package storage

import (
	"testing"
)

func TestEncodeDecodeNode(t *testing.T) {
	node := &Node{
		ID:     42,
		Labels: []string{"Person", "Employee"},
		Properties: map[string]Value{
			"name": StringValue("Alice"),
			"age":  IntValue(30),
		},
	}

	data := EncodeNode(node)
	decoded, err := DecodeNode(data)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}

	if decoded.ID != 42 {
		t.Errorf("ID: expected 42, got %d", decoded.ID)
	}
	if len(decoded.Labels) != 2 {
		t.Fatalf("Labels: expected 2, got %d", len(decoded.Labels))
	}
	if decoded.Labels[0] != "Person" || decoded.Labels[1] != "Employee" {
		t.Errorf("Labels: %v", decoded.Labels)
	}
	if decoded.Properties["name"].AsString() != "Alice" {
		t.Errorf("name: expected 'Alice', got %q", decoded.Properties["name"].AsString())
	}
	if decoded.Properties["age"].AsInt() != 30 {
		t.Errorf("age: expected 30, got %d", decoded.Properties["age"].AsInt())
	}
}

func TestEncodeDecodeNodeNoLabels(t *testing.T) {
	node := &Node{
		ID:         1,
		Labels:     nil,
		Properties: map[string]Value{},
	}

	data := EncodeNode(node)
	decoded, err := DecodeNode(data)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	if decoded.ID != 1 {
		t.Errorf("ID: expected 1, got %d", decoded.ID)
	}
	if len(decoded.Labels) != 0 {
		t.Errorf("Labels: expected 0, got %d", len(decoded.Labels))
	}
}

func TestEncodeDecodeEdge(t *testing.T) {
	edge := &Edge{
		ID:       7,
		Type:     "KNOWS",
		SourceID: 1,
		TargetID: 2,
		Properties: map[string]Value{
			"since": IntValue(2020),
		},
	}

	data := EncodeEdge(edge)
	decoded, err := DecodeEdge(data)
	if err != nil {
		t.Fatalf("DecodeEdge: %v", err)
	}

	if decoded.ID != 7 {
		t.Errorf("ID: expected 7, got %d", decoded.ID)
	}
	if decoded.Type != "KNOWS" {
		t.Errorf("Type: expected 'KNOWS', got %q", decoded.Type)
	}
	if decoded.SourceID != 1 || decoded.TargetID != 2 {
		t.Errorf("Source/Target: expected 1→2, got %d→%d", decoded.SourceID, decoded.TargetID)
	}
	if decoded.Properties["since"].AsInt() != 2020 {
		t.Errorf("since: expected 2020, got %d", decoded.Properties["since"].AsInt())
	}
}

func TestEncodeDecodeEdgeNoProps(t *testing.T) {
	edge := &Edge{
		ID:         1,
		Type:       "LIKES",
		SourceID:   10,
		TargetID:   20,
		Properties: map[string]Value{},
	}

	data := EncodeEdge(edge)
	decoded, err := DecodeEdge(data)
	if err != nil {
		t.Fatalf("DecodeEdge: %v", err)
	}
	if decoded.Type != "LIKES" {
		t.Errorf("Type: expected 'LIKES', got %q", decoded.Type)
	}
}

func TestDecodeNodeTruncated(t *testing.T) {
	_, err := DecodeNode([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestDecodeEdgeTruncated(t *testing.T) {
	_, err := DecodeEdge([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestValueTypes(t *testing.T) {
	iv := IntValue(-42)
	if iv.AsInt() != -42 {
		t.Errorf("IntValue: expected -42, got %d", iv.AsInt())
	}

	sv := StringValue("hello")
	if sv.AsString() != "hello" {
		t.Errorf("StringValue: expected 'hello', got %q", sv.AsString())
	}

	bv := BoolValue(true)
	if !bv.AsBool() {
		t.Error("BoolValue(true): expected true")
	}

	bv2 := BoolValue(false)
	if bv2.AsBool() {
		t.Error("BoolValue(false): expected false")
	}

	fv := FloatValue(3.14)
	if fv.AsFloat() != 3.14 {
		t.Errorf("FloatValue: expected 3.14, got %f", fv.AsFloat())
	}

	byv := BytesValue([]byte{0xDE, 0xAD})
	if len(byv.AsBytes()) != 2 || byv.AsBytes()[0] != 0xDE {
		t.Errorf("BytesValue: unexpected data")
	}
}
