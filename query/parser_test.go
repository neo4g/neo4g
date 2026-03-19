package query

import "testing"

func TestParseCreateNode(t *testing.T) {
	stmt, err := Parse(`CREATE (n:Person {name: "Alice", age: 30})`)
	if err != nil {
		t.Fatal(err)
	}

	if stmt.Create == nil {
		t.Fatal("expected CREATE clause")
	}
	if len(stmt.Create.Patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(stmt.Create.Patterns))
	}

	pat := stmt.Create.Patterns[0]
	if len(pat.Elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(pat.Elements))
	}

	np, ok := pat.Elements[0].(*NodePattern)
	if !ok {
		t.Fatal("expected NodePattern")
	}
	if np.Variable != "n" {
		t.Errorf("variable: expected 'n', got %q", np.Variable)
	}
	if len(np.Labels) != 1 || np.Labels[0] != "Person" {
		t.Errorf("labels: expected [Person], got %v", np.Labels)
	}
	if len(np.Properties) != 2 {
		t.Errorf("expected 2 properties, got %d", len(np.Properties))
	}

	nameLit, ok := np.Properties["name"].(*LiteralExpr)
	if !ok || nameLit.Value != "Alice" {
		t.Errorf("name: expected 'Alice', got %v", np.Properties["name"])
	}
	ageLit, ok := np.Properties["age"].(*LiteralExpr)
	if !ok || ageLit.Value != int64(30) {
		t.Errorf("age: expected 30, got %v", np.Properties["age"])
	}
}

func TestParseMatchReturn(t *testing.T) {
	stmt, err := Parse(`MATCH (n:Person) RETURN n`)
	if err != nil {
		t.Fatal(err)
	}

	if stmt.Match == nil {
		t.Fatal("expected MATCH clause")
	}
	if len(stmt.Match.Patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(stmt.Match.Patterns))
	}

	np := stmt.Match.Patterns[0].Elements[0].(*NodePattern)
	if np.Variable != "n" || len(np.Labels) != 1 || np.Labels[0] != "Person" {
		t.Errorf("unexpected node pattern: %+v", np)
	}

	if stmt.Return == nil {
		t.Fatal("expected RETURN clause")
	}
	if len(stmt.Return.Items) != 1 {
		t.Fatalf("expected 1 return item, got %d", len(stmt.Return.Items))
	}

	ident, ok := stmt.Return.Items[0].Expr.(*IdentExpr)
	if !ok || ident.Name != "n" {
		t.Errorf("expected RETURN n, got %v", stmt.Return.Items[0].Expr)
	}
}

func TestParseMatchWhereReturn(t *testing.T) {
	stmt, err := Parse(`MATCH (n:Person) WHERE n.name = "Alice" RETURN n`)
	if err != nil {
		t.Fatal(err)
	}

	if stmt.Match.Where == nil {
		t.Fatal("expected WHERE clause")
	}

	bin, ok := stmt.Match.Where.(*BinaryExpr)
	if !ok || bin.Op != EQ {
		t.Fatalf("expected = comparison, got %T", stmt.Match.Where)
	}

	prop, ok := bin.Left.(*PropExpr)
	if !ok || prop.Variable != "n" || prop.Key != "name" {
		t.Errorf("expected n.name, got %v", bin.Left)
	}

	lit, ok := bin.Right.(*LiteralExpr)
	if !ok || lit.Value != "Alice" {
		t.Errorf("expected 'Alice', got %v", bin.Right)
	}
}

func TestParseEdgePattern(t *testing.T) {
	stmt, err := Parse(`MATCH (n)-[:KNOWS]->(m) RETURN n, m`)
	if err != nil {
		t.Fatal(err)
	}

	pat := stmt.Match.Patterns[0]
	if len(pat.Elements) != 3 {
		t.Fatalf("expected 3 elements (node-edge-node), got %d", len(pat.Elements))
	}

	n1 := pat.Elements[0].(*NodePattern)
	if n1.Variable != "n" {
		t.Errorf("first node: expected 'n', got %q", n1.Variable)
	}

	edge := pat.Elements[1].(*EdgePattern)
	if edge.Dir != DirRight {
		t.Errorf("expected DirRight, got %d", edge.Dir)
	}
	if len(edge.Types) != 1 || edge.Types[0] != "KNOWS" {
		t.Errorf("expected [:KNOWS], got %v", edge.Types)
	}

	n2 := pat.Elements[2].(*NodePattern)
	if n2.Variable != "m" {
		t.Errorf("second node: expected 'm', got %q", n2.Variable)
	}

	if len(stmt.Return.Items) != 2 {
		t.Errorf("expected 2 return items, got %d", len(stmt.Return.Items))
	}
}

func TestParseLeftArrowEdge(t *testing.T) {
	stmt, err := Parse(`MATCH (a)<-[:FOLLOWS]-(b) RETURN a`)
	if err != nil {
		t.Fatal(err)
	}

	edge := stmt.Match.Patterns[0].Elements[1].(*EdgePattern)
	if edge.Dir != DirLeft {
		t.Errorf("expected DirLeft, got %d", edge.Dir)
	}
	if len(edge.Types) != 1 || edge.Types[0] != "FOLLOWS" {
		t.Errorf("expected [:FOLLOWS], got %v", edge.Types)
	}
}

func TestParseUndirectedEdge(t *testing.T) {
	stmt, err := Parse(`MATCH (a)-[:KNOWS]-(b) RETURN a`)
	if err != nil {
		t.Fatal(err)
	}

	edge := stmt.Match.Patterns[0].Elements[1].(*EdgePattern)
	if edge.Dir != DirNone {
		t.Errorf("expected DirNone, got %d", edge.Dir)
	}
}

func TestParseIDFunction(t *testing.T) {
	stmt, err := Parse(`MATCH (n) WHERE id(n) = 1 RETURN n`)
	if err != nil {
		t.Fatal(err)
	}

	bin := stmt.Match.Where.(*BinaryExpr)
	fn, ok := bin.Left.(*FuncExpr)
	if !ok {
		t.Fatalf("expected FuncExpr, got %T", bin.Left)
	}
	if fn.Name != "id" {
		t.Errorf("expected 'id', got %q", fn.Name)
	}
	if len(fn.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(fn.Args))
	}
	arg, ok := fn.Args[0].(*IdentExpr)
	if !ok || arg.Name != "n" {
		t.Errorf("expected ident 'n', got %v", fn.Args[0])
	}
}

func TestParseSet(t *testing.T) {
	stmt, err := Parse(`MATCH (n) WHERE id(n) = 1 SET n.name = "Bob", n.age = 31`)
	if err != nil {
		t.Fatal(err)
	}

	if stmt.Set == nil {
		t.Fatal("expected SET clause")
	}
	if len(stmt.Set.Items) != 2 {
		t.Fatalf("expected 2 SET items, got %d", len(stmt.Set.Items))
	}

	s0 := stmt.Set.Items[0]
	if s0.Variable != "n" || s0.Property != "name" {
		t.Errorf("first SET item: expected n.name, got %s.%s", s0.Variable, s0.Property)
	}
	lit, ok := s0.Value.(*LiteralExpr)
	if !ok || lit.Value != "Bob" {
		t.Errorf("first SET value: expected 'Bob', got %v", s0.Value)
	}

	s1 := stmt.Set.Items[1]
	if s1.Variable != "n" || s1.Property != "age" {
		t.Errorf("second SET item: expected n.age, got %s.%s", s1.Variable, s1.Property)
	}
	lit2, ok := s1.Value.(*LiteralExpr)
	if !ok || lit2.Value != int64(31) {
		t.Errorf("second SET value: expected 31, got %v", s1.Value)
	}
}

func TestParseDelete(t *testing.T) {
	stmt, err := Parse(`MATCH (n) WHERE id(n) = 1 DELETE n`)
	if err != nil {
		t.Fatal(err)
	}

	if stmt.Delete == nil {
		t.Fatal("expected DELETE clause")
	}
	if len(stmt.Delete.Variables) != 1 || stmt.Delete.Variables[0] != "n" {
		t.Errorf("expected DELETE n, got %v", stmt.Delete.Variables)
	}
}

func TestParseMatchCreateEdge(t *testing.T) {
	stmt, err := Parse(`MATCH (a), (b) WHERE id(a) = 1 AND id(b) = 2 CREATE (a)-[:KNOWS]->(b)`)
	if err != nil {
		t.Fatal(err)
	}

	if stmt.Match == nil {
		t.Fatal("expected MATCH clause")
	}
	if len(stmt.Match.Patterns) != 2 {
		t.Fatalf("expected 2 MATCH patterns, got %d", len(stmt.Match.Patterns))
	}

	// Verify WHERE has AND
	bin, ok := stmt.Match.Where.(*BinaryExpr)
	if !ok || bin.Op != AND {
		t.Fatalf("expected AND, got %T", stmt.Match.Where)
	}

	if stmt.Create == nil {
		t.Fatal("expected CREATE clause")
	}
	pat := stmt.Create.Patterns[0]
	if len(pat.Elements) != 3 {
		t.Fatalf("expected 3 elements in CREATE pattern, got %d", len(pat.Elements))
	}

	edge := pat.Elements[1].(*EdgePattern)
	if edge.Dir != DirRight || len(edge.Types) != 1 || edge.Types[0] != "KNOWS" {
		t.Errorf("unexpected edge: %+v", edge)
	}
}

func TestParseReturnStar(t *testing.T) {
	stmt, err := Parse(`MATCH (n:Person) RETURN *`)
	if err != nil {
		t.Fatal(err)
	}
	if stmt.Return == nil || !stmt.Return.Star {
		t.Fatal("expected RETURN *")
	}
}

func TestParseReturnProperties(t *testing.T) {
	stmt, err := Parse(`MATCH (n:Person) RETURN n.name, n.age`)
	if err != nil {
		t.Fatal(err)
	}

	if len(stmt.Return.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(stmt.Return.Items))
	}

	p0, ok := stmt.Return.Items[0].Expr.(*PropExpr)
	if !ok || p0.Variable != "n" || p0.Key != "name" {
		t.Errorf("expected n.name, got %v", stmt.Return.Items[0].Expr)
	}
	p1, ok := stmt.Return.Items[1].Expr.(*PropExpr)
	if !ok || p1.Variable != "n" || p1.Key != "age" {
		t.Errorf("expected n.age, got %v", stmt.Return.Items[1].Expr)
	}
}

func TestParseReturnAlias(t *testing.T) {
	stmt, err := Parse(`MATCH (n:Person) RETURN n.name AS personName`)
	if err != nil {
		t.Fatal(err)
	}

	if stmt.Return.Items[0].Alias != "personName" {
		t.Errorf("expected alias 'personName', got %q", stmt.Return.Items[0].Alias)
	}
}

func TestParseAndOrNot(t *testing.T) {
	stmt, err := Parse(`MATCH (n) WHERE n.a = 1 AND n.b = 2 OR NOT n.c = 3 RETURN n`)
	if err != nil {
		t.Fatal(err)
	}

	// Should be OR at top level: (a=1 AND b=2) OR (NOT c=3)
	or, ok := stmt.Match.Where.(*BinaryExpr)
	if !ok || or.Op != OR {
		t.Fatalf("expected OR at top, got %T", stmt.Match.Where)
	}
	and, ok := or.Left.(*BinaryExpr)
	if !ok || and.Op != AND {
		t.Fatalf("expected AND on left, got %T", or.Left)
	}
	not, ok := or.Right.(*UnaryExpr)
	if !ok || not.Op != NOT {
		t.Fatalf("expected NOT on right, got %T", or.Right)
	}
}

func TestParseParam(t *testing.T) {
	stmt, err := Parse(`MATCH (n) WHERE n.name = $name RETURN n`)
	if err != nil {
		t.Fatal(err)
	}

	bin := stmt.Match.Where.(*BinaryExpr)
	param, ok := bin.Right.(*ParamExpr)
	if !ok || param.Name != "name" {
		t.Errorf("expected $name param, got %v", bin.Right)
	}
}

func TestParseEdgeWithVariable(t *testing.T) {
	stmt, err := Parse(`MATCH (n)-[e:KNOWS]->(m) WHERE id(n) = 1 DELETE e`)
	if err != nil {
		t.Fatal(err)
	}

	edge := stmt.Match.Patterns[0].Elements[1].(*EdgePattern)
	if edge.Variable != "e" {
		t.Errorf("expected edge variable 'e', got %q", edge.Variable)
	}

	if stmt.Delete == nil || len(stmt.Delete.Variables) != 1 || stmt.Delete.Variables[0] != "e" {
		t.Errorf("expected DELETE e, got %v", stmt.Delete)
	}
}

func TestParseEdgeWithProps(t *testing.T) {
	stmt, err := Parse(`CREATE (a)-[:KNOWS {since: 2024}]->(b)`)
	if err != nil {
		t.Fatal(err)
	}

	edge := stmt.Create.Patterns[0].Elements[1].(*EdgePattern)
	if len(edge.Properties) != 1 {
		t.Fatalf("expected 1 edge property, got %d", len(edge.Properties))
	}
	lit, ok := edge.Properties["since"].(*LiteralExpr)
	if !ok || lit.Value != int64(2024) {
		t.Errorf("expected since=2024, got %v", edge.Properties["since"])
	}
}

func TestParseMultiLabel(t *testing.T) {
	stmt, err := Parse(`CREATE (n:Person:Employee {name: "Bob"})`)
	if err != nil {
		t.Fatal(err)
	}

	np := stmt.Create.Patterns[0].Elements[0].(*NodePattern)
	if len(np.Labels) != 2 || np.Labels[0] != "Person" || np.Labels[1] != "Employee" {
		t.Errorf("expected [Person, Employee], got %v", np.Labels)
	}
}

func TestParseErrorEmpty(t *testing.T) {
	_, err := Parse("")
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestParseErrorMissingParen(t *testing.T) {
	_, err := Parse(`MATCH (n:Person RETURN n`)
	if err == nil {
		t.Fatal("expected error for missing closing paren")
	}
}

func TestParseErrorUnexpectedToken(t *testing.T) {
	_, err := Parse(`MATCH`)
	if err == nil {
		t.Fatal("expected error for bare MATCH")
	}
}

func TestParseErrorMatchAlone(t *testing.T) {
	_, err := Parse(`MATCH (n)`)
	if err == nil {
		t.Fatal("expected error for MATCH without action clause")
	}
}

func TestParseBoolLiterals(t *testing.T) {
	stmt, err := Parse(`MATCH (n) WHERE n.active = true RETURN n`)
	if err != nil {
		t.Fatal(err)
	}

	bin := stmt.Match.Where.(*BinaryExpr)
	lit, ok := bin.Right.(*LiteralExpr)
	if !ok || lit.Value != true {
		t.Errorf("expected true, got %v", bin.Right)
	}
}

func TestParseNullLiteral(t *testing.T) {
	stmt, err := Parse(`MATCH (n) WHERE n.email = null RETURN n`)
	if err != nil {
		t.Fatal(err)
	}

	bin := stmt.Match.Where.(*BinaryExpr)
	lit, ok := bin.Right.(*LiteralExpr)
	if !ok || lit.Value != nil {
		t.Errorf("expected nil, got %v", bin.Right)
	}
}

func TestParseComparisonOps(t *testing.T) {
	ops := []struct {
		sym string
		tok TokenType
	}{
		{"<>", NEQ}, {"<", LT}, {">", GT}, {"<=", LTE}, {">=", GTE},
	}
	for _, op := range ops {
		stmt, err := Parse(`MATCH (n) WHERE n.x ` + op.sym + ` 5 RETURN n`)
		if err != nil {
			t.Fatalf("op %s: %v", op.sym, err)
		}
		bin := stmt.Match.Where.(*BinaryExpr)
		if bin.Op != op.tok {
			t.Errorf("op %s: expected %s, got %s", op.sym, op.tok, bin.Op)
		}
	}
}
