package query

import (
	"testing"

	"github.com/neo4g/neo4g/graph"
	"github.com/neo4g/neo4g/parser"
)

func testNode() *graph.Node {
	return &graph.Node{
		ID:     1,
		Labels: []string{"Person"},
		Properties: map[string]graph.Value{
			"name": graph.StringValue("Alice"),
			"age":  graph.IntValue(30),
		},
	}
}

func testEdge() *graph.Edge {
	return &graph.Edge{
		ID:       10,
		Type:     "KNOWS",
		SourceID: 1,
		TargetID: 2,
		Properties: map[string]graph.Value{
			"since": graph.IntValue(2024),
		},
	}
}

func TestEvalLiteral(t *testing.T) {
	cases := []struct {
		expr parser.Expr
		want interface{}
	}{
		{&parser.LiteralExpr{Value: int64(42)}, int64(42)},
		{&parser.LiteralExpr{Value: 3.14}, 3.14},
		{&parser.LiteralExpr{Value: "hello"}, "hello"},
		{&parser.LiteralExpr{Value: true}, true},
		{&parser.LiteralExpr{Value: nil}, nil},
	}
	for _, c := range cases {
		got, err := evalExpr(c.expr, nil, nil)
		if err != nil {
			t.Errorf("evalExpr(%v): %v", c.expr, err)
		}
		if got != c.want {
			t.Errorf("evalExpr(%v) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestEvalIdent(t *testing.T) {
	row := Row{"n": testNode()}
	got, err := evalExpr(&parser.IdentExpr{Name: "n"}, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != row["n"] {
		t.Errorf("expected node, got %v", got)
	}
}

func TestEvalIdentUnbound(t *testing.T) {
	_, err := evalExpr(&parser.IdentExpr{Name: "x"}, Row{}, nil)
	if err == nil {
		t.Fatal("expected error for unbound variable")
	}
}

func TestEvalPropNode(t *testing.T) {
	row := Row{"n": testNode()}
	got, err := evalExpr(&parser.PropExpr{Variable: "n", Key: "name"}, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Alice" {
		t.Errorf("expected Alice, got %v", got)
	}
}

func TestEvalPropEdge(t *testing.T) {
	row := Row{"e": testEdge()}
	got, err := evalExpr(&parser.PropExpr{Variable: "e", Key: "since"}, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(2024) {
		t.Errorf("expected 2024, got %v", got)
	}
}

func TestEvalPropMissing(t *testing.T) {
	row := Row{"n": testNode()}
	got, err := evalExpr(&parser.PropExpr{Variable: "n", Key: "nonexistent"}, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing prop, got %v", got)
	}
}

func TestEvalParam(t *testing.T) {
	params := map[string]interface{}{"name": "Bob"}
	got, err := evalExpr(&parser.ParamExpr{Name: "name"}, nil, params)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bob" {
		t.Errorf("expected Bob, got %v", got)
	}
}

func TestEvalParamUnbound(t *testing.T) {
	_, err := evalExpr(&parser.ParamExpr{Name: "x"}, nil, map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for unbound param")
	}
}

func TestEvalFuncID(t *testing.T) {
	row := Row{"n": testNode(), "e": testEdge()}

	got, err := evalExpr(&parser.FuncExpr{Name: "id", Args: []parser.Expr{&parser.IdentExpr{Name: "n"}}}, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(1) {
		t.Errorf("expected 1, got %v", got)
	}

	got, err = evalExpr(&parser.FuncExpr{Name: "id", Args: []parser.Expr{&parser.IdentExpr{Name: "e"}}}, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(10) {
		t.Errorf("expected 10, got %v", got)
	}
}

func TestEvalFuncIDWrongArgs(t *testing.T) {
	_, err := evalExpr(&parser.FuncExpr{Name: "id", Args: nil}, Row{}, nil)
	if err == nil {
		t.Fatal("expected error for wrong arg count")
	}
}

func TestEvalFuncUnknown(t *testing.T) {
	_, err := evalExpr(&parser.FuncExpr{Name: "foo", Args: nil}, Row{}, nil)
	if err == nil {
		t.Fatal("expected error for unknown function")
	}
}

func TestEvalCompareEQ(t *testing.T) {
	row := Row{"n": testNode()}
	expr := &parser.BinaryExpr{
		Op:    parser.EQ,
		Left:  &parser.PropExpr{Variable: "n", Key: "name"},
		Right: &parser.LiteralExpr{Value: "Alice"},
	}
	got, err := evalExpr(expr, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Errorf("expected true, got %v", got)
	}
}

func TestEvalCompareNEQ(t *testing.T) {
	row := Row{"n": testNode()}
	expr := &parser.BinaryExpr{
		Op:    parser.NEQ,
		Left:  &parser.PropExpr{Variable: "n", Key: "age"},
		Right: &parser.LiteralExpr{Value: int64(99)},
	}
	got, err := evalExpr(expr, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Errorf("expected true, got %v", got)
	}
}

func TestEvalCompareLTGT(t *testing.T) {
	row := Row{"n": testNode()}
	cases := []struct {
		op   parser.TokenType
		val  int64
		want bool
	}{
		{parser.LT, 31, true},
		{parser.GT, 29, true},
		{parser.LTE, 30, true},
		{parser.GTE, 30, true},
		{parser.LT, 30, false},
		{parser.GT, 30, false},
	}
	for _, c := range cases {
		expr := &parser.BinaryExpr{
			Op:    c.op,
			Left:  &parser.PropExpr{Variable: "n", Key: "age"},
			Right: &parser.LiteralExpr{Value: c.val},
		}
		got, err := evalExpr(expr, row, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("age %s %d: expected %v, got %v", c.op, c.val, c.want, got)
		}
	}
}

func TestEvalAND(t *testing.T) {
	row := Row{"n": testNode()}
	expr := &parser.BinaryExpr{
		Op: parser.AND,
		Left: &parser.BinaryExpr{
			Op:    parser.EQ,
			Left:  &parser.PropExpr{Variable: "n", Key: "name"},
			Right: &parser.LiteralExpr{Value: "Alice"},
		},
		Right: &parser.BinaryExpr{
			Op:    parser.EQ,
			Left:  &parser.PropExpr{Variable: "n", Key: "age"},
			Right: &parser.LiteralExpr{Value: int64(30)},
		},
	}
	got, err := evalExpr(expr, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Errorf("expected true, got %v", got)
	}
}

func TestEvalOR(t *testing.T) {
	row := Row{"n": testNode()}
	expr := &parser.BinaryExpr{
		Op: parser.OR,
		Left: &parser.BinaryExpr{
			Op:    parser.EQ,
			Left:  &parser.PropExpr{Variable: "n", Key: "name"},
			Right: &parser.LiteralExpr{Value: "Bob"},
		},
		Right: &parser.BinaryExpr{
			Op:    parser.EQ,
			Left:  &parser.PropExpr{Variable: "n", Key: "age"},
			Right: &parser.LiteralExpr{Value: int64(30)},
		},
	}
	got, err := evalExpr(expr, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Errorf("expected true, got %v", got)
	}
}

func TestEvalNOT(t *testing.T) {
	row := Row{"n": testNode()}
	expr := &parser.UnaryExpr{
		Op: parser.NOT,
		Operand: &parser.BinaryExpr{
			Op:    parser.EQ,
			Left:  &parser.PropExpr{Variable: "n", Key: "name"},
			Right: &parser.LiteralExpr{Value: "Bob"},
		},
	}
	got, err := evalExpr(expr, row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Errorf("expected true, got %v", got)
	}
}

func TestEvalNullComparison(t *testing.T) {
	got, err := evalExpr(&parser.BinaryExpr{
		Op:    parser.EQ,
		Left:  &parser.LiteralExpr{Value: nil},
		Right: &parser.LiteralExpr{Value: nil},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Errorf("null = null should be true, got %v", got)
	}
}

func TestEvalANDShortCircuit(t *testing.T) {
	expr := &parser.BinaryExpr{
		Op:    parser.AND,
		Left:  &parser.LiteralExpr{Value: false},
		Right: &parser.IdentExpr{Name: "nonexistent"},
	}
	got, err := evalExpr(expr, Row{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != false {
		t.Errorf("expected false from short-circuit, got %v", got)
	}
}

func TestEvalORShortCircuit(t *testing.T) {
	expr := &parser.BinaryExpr{
		Op:    parser.OR,
		Left:  &parser.LiteralExpr{Value: true},
		Right: &parser.IdentExpr{Name: "nonexistent"},
	}
	got, err := evalExpr(expr, Row{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Errorf("expected true from short-circuit, got %v", got)
	}
}
