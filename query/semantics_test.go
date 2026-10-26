package query

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/neo4g/neo4g/graph"
	"github.com/neo4g/neo4g/parser"
	"github.com/neo4g/neo4g/storage"
)

func nameSet(t *testing.T, res *Result, col string) map[string]bool {
	t.Helper()
	got := make(map[string]bool, len(res.Rows))
	for _, row := range res.Rows {
		s, ok := row[col].(string)
		if !ok {
			t.Fatalf("column %s = %#v", col, row[col])
		}
		got[s] = true
	}
	return got
}

func assertNames(t *testing.T, res *Result, col string, want ...string) {
	t.Helper()
	got := nameSet(t, res, col)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("missing %q in %v", name, got)
		}
	}
}

func createPerson(t *testing.T, ex *Executor, name string) {
	t.Helper()
	execute(t, ex, fmt.Sprintf(`CREATE (n:Person {name: %q})`, name), nil)
}

func createKnows(t *testing.T, ex *Executor, from, to string) {
	t.Helper()
	q := fmt.Sprintf(`MATCH (a), (b) WHERE a.name = %q AND b.name = %q CREATE (a)-[:KNOWS]->(b)`, from, to)
	execute(t, ex, q, nil)
}

func TestCrossProductEmptySide(t *testing.T) {
	person := []Row{{"b": &graph.Node{ID: 1}}}
	if got := crossProduct(person, nil); len(got) != 0 {
		t.Fatalf("empty right side produced %d rows", len(got))
	}
	if got := crossProduct(nil, person); len(got) != 0 {
		t.Fatalf("empty left side produced %d rows", len(got))
	}
}

func TestCrossProductJoinsSharedNames(t *testing.T) {
	left := []Row{
		{"a": &graph.Node{ID: 1}, "b": &graph.Node{ID: 2}},
		{"a": &graph.Node{ID: 1}, "b": &graph.Node{ID: 3}},
	}
	right := []Row{
		{"b": &graph.Node{ID: 3}, "c": &graph.Node{ID: 4}},
		{"b": &graph.Node{ID: 9}, "c": &graph.Node{ID: 5}},
	}
	got := crossProduct(left, right)
	if len(got) != 1 {
		t.Fatalf("expected 1 joined row, got %d (%v)", len(got), got)
	}
	b := got[0]["b"].(*graph.Node)
	c := got[0]["c"].(*graph.Node)
	if b.ID != 3 || c.ID != 4 {
		t.Fatalf("joined b=%d c=%d", b.ID, c.ID)
	}
}

func TestCommaMatchEmptySideDeletesNothing(t *testing.T) {
	ex := testExecutor(t)
	createPerson(t, ex, "Alice")
	createPerson(t, ex, "Bob")

	res := execute(t, ex, `MATCH (a:Missing), (b:Person) RETURN b.name`, nil)
	if len(res.Rows) != 0 {
		t.Fatalf("empty left pattern produced %d rows", len(res.Rows))
	}
	res = execute(t, ex, `MATCH (b:Person), (a:Missing) RETURN b.name`, nil)
	if len(res.Rows) != 0 {
		t.Fatalf("empty right pattern produced %d rows", len(res.Rows))
	}

	res = execute(t, ex, `MATCH (a:Missing), (b:Person) DELETE b`, nil)
	if len(res.Columns) != 1 || res.Columns[0] != "deleted" {
		t.Fatalf("columns = %v", res.Columns)
	}
	if got, ok := res.Rows[0]["deleted"].(int); !ok || got != 0 || len(res.Rows[0]) != 1 {
		t.Fatalf("deleted row = %#v", res.Rows[0])
	}

	res = execute(t, ex, `MATCH (n:Person) RETURN n.name`, nil)
	assertNames(t, res, "n.name", "Alice", "Bob")
}

func TestCommaPatternIdentity(t *testing.T) {
	ex := testExecutor(t)
	for _, name := range []string{"Alice", "Bob", "Carol", "Dave"} {
		createPerson(t, ex, name)
	}
	createKnows(t, ex, "Alice", "Bob")
	createKnows(t, ex, "Carol", "Dave")

	res := execute(t, ex, `MATCH (a)-[:KNOWS]->(b), (b)-[:KNOWS]->(c) RETURN a.name`, nil)
	if len(res.Rows) != 0 {
		t.Fatalf("disjoint patterns produced %d rows: %v", len(res.Rows), res.Rows)
	}

	ex = testExecutor(t)
	for _, name := range []string{"Alice", "Bob", "Carol", "Dave", "Eve"} {
		createPerson(t, ex, name)
	}
	createKnows(t, ex, "Alice", "Bob")
	createKnows(t, ex, "Alice", "Carol")
	createKnows(t, ex, "Bob", "Dave")
	createKnows(t, ex, "Carol", "Eve")

	// (Alice)-[:KNOWS]->(Bob|Carol) joined with (Bob)->(Dave) and (Carol)->(Eve).
	res = execute(t, ex, `MATCH (a)-[:KNOWS]->(b), (b)-[:KNOWS]->(c) RETURN a.name, b.name, c.name`, nil)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 joined paths, got %d: %v", len(res.Rows), res.Rows)
	}
	got := map[string]bool{}
	for _, row := range res.Rows {
		got[fmt.Sprint(row["a.name"], ",", row["b.name"], ",", row["c.name"])] = true
	}
	for _, path := range []string{"Alice,Bob,Dave", "Alice,Carol,Eve"} {
		if !got[path] {
			t.Fatalf("missing %s in %v", path, got)
		}
	}
}

func TestPathVariableIdentity(t *testing.T) {
	ex := testExecutor(t)
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		createPerson(t, ex, name)
	}
	createKnows(t, ex, "Alice", "Bob")
	createKnows(t, ex, "Bob", "Alice")
	createKnows(t, ex, "Bob", "Carol")

	res := execute(t, ex, `MATCH (a)-[:KNOWS]->(b)-[:KNOWS]->(a) RETURN a.name, b.name`, nil)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 cycles, got %d: %v", len(res.Rows), res.Rows)
	}
	assertNames(t, res, "a.name", "Alice", "Bob")
}

func TestUndirectedAndAnonymousEndpoints(t *testing.T) {
	ex := testExecutor(t)
	createPerson(t, ex, "Alice")
	createPerson(t, ex, "Bob")
	createKnows(t, ex, "Alice", "Bob")

	res := execute(t, ex, `MATCH (n:Person {name: "Bob"})-[:KNOWS]-(m) RETURN m.name`, nil)
	assertNames(t, res, "m.name", "Alice")

	res = execute(t, ex, `MATCH (n:Person {name: "Bob"})<-[:KNOWS]-(m) RETURN m.name`, nil)
	assertNames(t, res, "m.name", "Alice")

	res = execute(t, ex, `MATCH (n:Person {name: "Alice"})-[:KNOWS]->(m) RETURN m.name`, nil)
	assertNames(t, res, "m.name", "Bob")

	res = execute(t, ex, `MATCH ()-[:KNOWS]->(b) RETURN b.name`, nil)
	assertNames(t, res, "b.name", "Bob")

	res = execute(t, ex, `MATCH (:Person {name: "Alice"})-[:KNOWS]->(b) RETURN b.name`, nil)
	assertNames(t, res, "b.name", "Bob")

	createPerson(t, ex, "Carol")
	createKnows(t, ex, "Bob", "Carol")
	res = execute(t, ex, `MATCH ()-[:KNOWS]->()-[:KNOWS]->(c) RETURN c.name`, nil)
	assertNames(t, res, "c.name", "Carol")
}

func TestPatternPropertyMaps(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice", age: 30})`, nil)
	execute(t, ex, `CREATE (n:Person {name: "Bob", age: 25})`, nil)
	execute(t, ex, `CREATE (n:Person {name: "Carol", age: 25})`, nil)

	res := execute(t, ex, `MATCH (n:Person {name: "Alice"}) RETURN n.name`, nil)
	assertNames(t, res, "n.name", "Alice")

	res = execute(t, ex, `MATCH (n:Person {name: $name}) RETURN n.name`, map[string]interface{}{"name": "Bob"})
	assertNames(t, res, "n.name", "Bob")

	execute(t, ex, `MATCH (a), (b) WHERE a.name = "Alice" AND b.name = "Bob" CREATE (a)-[:KNOWS {since: 2020}]->(b)`, nil)
	execute(t, ex, `MATCH (a), (b) WHERE a.name = "Alice" AND b.name = "Carol" CREATE (a)-[:KNOWS {since: 2024}]->(b)`, nil)

	res = execute(t, ex, `MATCH (a)-[:KNOWS {since: 2020}]->(b) RETURN b.name`, nil)
	assertNames(t, res, "b.name", "Bob")

	res = execute(t, ex, `MATCH (a)-[:KNOWS {since: $since}]->(b) RETURN b.name`, map[string]interface{}{"since": float64(2024)})
	assertNames(t, res, "b.name", "Carol")

	res = execute(t, ex, `MATCH (a)-[:KNOWS]->(b {name: "Bob"}) RETURN b.name`, nil)
	assertNames(t, res, "b.name", "Bob")

	res = execute(t, ex, `MATCH (a)-[:KNOWS]->(b {name: $name}) RETURN b.name`, map[string]interface{}{"name": "Carol"})
	assertNames(t, res, "b.name", "Carol")
}

func TestAnchorRequiresEveryLabel(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Pat"})`, nil)
	execute(t, ex, `CREATE (n:Person:Employee {name: "Eve"})`, nil)
	execute(t, ex, `CREATE (n:Employee {name: "Ed"})`, nil)

	res := execute(t, ex, `MATCH (n:Person:Employee) RETURN n.name`, nil)
	assertNames(t, res, "n.name", "Eve")
}

func TestNumericEqualityWithJSONNumbers(t *testing.T) {
	if v := GoToValue(float64(30)); v.Type != graph.ValueTypeInt || v.AsInt() != 30 {
		t.Fatalf("GoToValue(30.0) = type %d %#v", v.Type, v)
	}
	if v := GoToValue(30.5); v.Type != graph.ValueTypeFloat {
		t.Fatalf("GoToValue(30.5) type = %d", v.Type)
	}

	node := &graph.Node{Properties: map[string]graph.Value{"age": graph.IntValue(30)}}
	if !matchFilters(node, []propFilter{{key: "age", val: float64(30)}}) {
		t.Fatal("float64 30 did not match stored int 30")
	}

	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice", age: 30})`, nil)
	execute(t, ex, `CREATE (n:Person {name: "Bob", age: 25})`, nil)

	res := execute(t, ex, `MATCH (n:Person) WHERE n.age = $age RETURN n.name`, map[string]interface{}{"age": float64(30)})
	assertNames(t, res, "n.name", "Alice")

	res = execute(t, ex, `MATCH (n:Person {age: $age}) RETURN n.name`, map[string]interface{}{"age": float64(30)})
	assertNames(t, res, "n.name", "Alice")

	res = execute(t, ex, `MATCH (n:Person) WHERE n.age = $age RETURN n.name`, map[string]interface{}{"age": float64(31)})
	if len(res.Rows) != 0 {
		t.Fatalf("expected no rows, got %d", len(res.Rows))
	}

	execute(t, ex, `CREATE (n:Person {name: "Cara", age: $age})`, map[string]interface{}{"age": float64(41)})
	res = execute(t, ex, `MATCH (n:Person) WHERE n.name = "Cara" RETURN n.age`, nil)
	if len(res.Rows) != 1 || res.Rows[0]["n.age"] != int64(41) {
		t.Fatalf("stored age = %#v", res.Rows)
	}
}

func TestMissingPropertyComparisonDoesNotAbort(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice"})`, nil)
	execute(t, ex, `CREATE (n:Person {name: "Alice", age: 21})`, nil)
	execute(t, ex, `CREATE (n:Person {name: "Bob", age: 40})`, nil)

	res := execute(t, ex, `MATCH (n) WHERE n.age > 18 AND n.name = "Alice" RETURN n.age`, nil)
	if len(res.Rows) != 1 || res.Rows[0]["n.age"] != int64(21) {
		t.Fatalf("age > 18 AND name: %#v", res.Rows)
	}

	res = execute(t, ex, `MATCH (n) WHERE n.name = "Alice" AND n.age > 18 RETURN n.age`, nil)
	if len(res.Rows) != 1 || res.Rows[0]["n.age"] != int64(21) {
		t.Fatalf("name AND age > 18: %#v", res.Rows)
	}

	res = execute(t, ex, `MATCH (n) WHERE n.age > 18 OR n.name = "Alice" RETURN n.name`, nil)
	if len(res.Rows) != 3 {
		t.Fatalf("OR returned %d rows, want 3 (both Alices and Bob)", len(res.Rows))
	}

	res = execute(t, ex, `MATCH (n) WHERE NOT n.age > 18 RETURN n.age`, nil)
	if len(res.Rows) != 1 || res.Rows[0]["n.age"] != nil {
		t.Fatalf("NOT age > 18 = %#v, want the node missing age", res.Rows)
	}
}

func TestWriteResultColumnsMatchRowKeys(t *testing.T) {
	ex := testExecutor(t)
	created := execute(t, ex, `CREATE (n:Person {name: "Alice"})`, nil)
	if len(created.Columns) != 1 || created.Columns[0] != "created" || len(created.Rows[0]) != 1 {
		t.Fatalf("create result columns=%v row=%#v", created.Columns, created.Rows[0])
	}
	if _, ok := created.Rows[0]["created"].(map[string]interface{}); !ok {
		t.Fatalf("created cell = %#v", created.Rows[0]["created"])
	}

	updated := execute(t, ex, `MATCH (n:Person) SET n.name = "Alicia"`, nil)
	if len(updated.Columns) != 1 || updated.Columns[0] != "updated" || len(updated.Rows[0]) != 1 {
		t.Fatalf("set result columns=%v row=%#v", updated.Columns, updated.Rows[0])
	}
	if updated.Rows[0]["updated"] != 1 {
		t.Fatalf("updated = %#v", updated.Rows[0])
	}
	if _, ok := updated.Rows[0]["count"]; ok {
		t.Fatal("SET row still uses count")
	}

	deleted := execute(t, ex, `MATCH (n:Person) DELETE n`, nil)
	if len(deleted.Columns) != 1 || deleted.Columns[0] != "deleted" || len(deleted.Rows[0]) != 1 {
		t.Fatalf("delete result columns=%v row=%#v", deleted.Columns, deleted.Rows[0])
	}
	if deleted.Rows[0]["deleted"] != 1 {
		t.Fatalf("deleted = %#v", deleted.Rows[0])
	}
}

func TestAnchorIDLookupSkipsScan(t *testing.T) {
	ada := &graph.Node{
		ID:         7,
		Labels:     []string{"Person"},
		Properties: map[string]graph.Value{"name": graph.StringValue("Ada")},
	}
	m := &memGraph{nodes: []*graph.Node{ada}, scanErr: fmt.Errorf("scan should not run")}
	ex := NewExecutor(m, m)

	run := func(t *testing.T, q string, params map[string]interface{}) (*Result, error) {
		t.Helper()
		m.scans, m.gets = 0, 0
		stmt, err := parser.Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		return ex.Execute(stmt, params)
	}

	res, err := run(t, `MATCH (n) WHERE id(n) = 7 RETURN n.name`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.scans != 0 || m.gets != 1 {
		t.Fatalf("scans=%d gets=%d, want 0 scans and 1 get", m.scans, m.gets)
	}
	assertNames(t, res, "n.name", "Ada")

	res, err = run(t, `MATCH (n) WHERE id(n) = $id RETURN n.name`, map[string]interface{}{"id": float64(7)})
	if err != nil {
		t.Fatal(err)
	}
	if m.scans != 0 || m.gets != 1 {
		t.Fatalf("param lookup scans=%d gets=%d", m.scans, m.gets)
	}
	assertNames(t, res, "n.name", "Ada")

	res, err = run(t, `MATCH (n) WHERE id(n) = 8 RETURN n.name`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 || m.scans != 0 {
		t.Fatalf("missing id rows=%d scans=%d err path should be empty", len(res.Rows), m.scans)
	}

	res, err = run(t, `MATCH (n:Animal) WHERE id(n) = 7 RETURN n.name`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 || m.scans != 0 {
		t.Fatalf("label mismatch rows=%d scans=%d", len(res.Rows), m.scans)
	}

	res, err = run(t, `MATCH (n) WHERE id(n) = 7 AND id(n) = 8 RETURN n`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 || m.scans != 0 || m.gets != 0 {
		t.Fatalf("conflicting ids rows=%d scans=%d gets=%d", len(res.Rows), m.scans, m.gets)
	}
}

func TestExpandSurfacesGetNodeErrors(t *testing.T) {
	alice := &graph.Node{ID: 1, Labels: []string{"Person"}, Properties: map[string]graph.Value{"name": graph.StringValue("Alice")}}
	edge := &graph.Edge{ID: 1, Type: "KNOWS", SourceID: 1, TargetID: 99}
	m := &memGraph{
		nodes:  []*graph.Node{alice},
		edges:  []*graph.Edge{edge},
		getErr: fmt.Errorf("disk read failed"),
	}
	ex := NewExecutor(m, m)

	stmt, err := parser.Parse(`MATCH (a)-[:KNOWS]->(b) RETURN b`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Execute(stmt, nil); err == nil || !strings.Contains(err.Error(), "disk read failed") {
		t.Fatalf("expected GetNode failure, got %v", err)
	}

	m.gets = 0
	stmt, err = parser.Parse(`MATCH ()-[:KNOWS]->() RETURN *`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ex.Execute(stmt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.gets != 0 {
		t.Fatalf("unbound destination called GetNode %d times", m.gets)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
}

func TestBoundCreateHonorsDirectionAndRowExprs(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice", age: 30})`, nil)
	createPerson(t, ex, "Bob")

	execute(t, ex, `MATCH (a), (b) WHERE a.name = "Alice" AND b.name = "Bob" CREATE (a)<-[:KNOWS {since: a.age}]-(b)`, nil)
	res := execute(t, ex, `MATCH (n {name: "Bob"})-[:KNOWS]->(m) RETURN m.name`, nil)
	assertNames(t, res, "m.name", "Alice")

	res = execute(t, ex, `MATCH (b)-[e:KNOWS]->(a) WHERE b.name = "Bob" RETURN e`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(res.Rows))
	}
	edge := res.Rows[0]["e"].(map[string]interface{})
	if edge["properties"].(map[string]interface{})["since"] != int64(30) {
		t.Fatalf("since = %#v", edge["properties"])
	}
}

func TestPathCreateDoesNotWritePartialNodes(t *testing.T) {
	ex := testExecutor(t)
	stmt, err := parser.Parse(`CREATE (a:Person)-[:KNOWS]->(b:Person)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Execute(stmt, nil); err == nil {
		t.Fatal("expected unbound path CREATE to fail")
	}
	res := execute(t, ex, `MATCH (n) RETURN n`, nil)
	if len(res.Rows) != 0 {
		t.Fatalf("path CREATE wrote %d nodes", len(res.Rows))
	}
}

// memGraph is a Store and Scanner for lookup tests that must not scan.
type memGraph struct {
	mu      sync.Mutex
	nodes   []*graph.Node
	edges   []*graph.Edge
	scans   int
	gets    int
	scanErr error
	getErr  error
}

func (m *memGraph) CreateNode(labels []string, props map[string]graph.Value) (graph.NodeID, error) {
	return 0, fmt.Errorf("not implemented")
}
func (m *memGraph) GetNode(id graph.NodeID) (*graph.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	if m.getErr != nil {
		return nil, m.getErr
	}
	for _, n := range m.nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, fmt.Errorf("node %d not found", id)
}
func (m *memGraph) UpdateNode(id graph.NodeID, props map[string]graph.Value) error {
	return fmt.Errorf("not implemented")
}
func (m *memGraph) DeleteNode(id graph.NodeID) error { return fmt.Errorf("not implemented") }
func (m *memGraph) CreateEdge(typ string, src, dst graph.NodeID, props map[string]graph.Value) (graph.EdgeID, error) {
	return 0, fmt.Errorf("not implemented")
}
func (m *memGraph) GetEdge(id graph.EdgeID) (*graph.Edge, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *memGraph) GetEdges(nodeID graph.NodeID, dir graph.Direction) ([]*graph.Edge, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *memGraph) DeleteEdge(id graph.EdgeID) error { return fmt.Errorf("not implemented") }

func (m *memGraph) ScanNodes(labels []string) ([]*graph.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scans++
	if m.scanErr != nil {
		return nil, m.scanErr
	}
	return append([]*graph.Node(nil), m.nodes...), nil
}
func (m *memGraph) ScanEdges(typ string) ([]*graph.Edge, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *memGraph) Expand(nodeID graph.NodeID, dir graph.Direction, edgeType string) ([]*graph.Edge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*graph.Edge
	for _, e := range m.edges {
		if edgeType != "" && e.Type != edgeType {
			continue
		}
		switch dir {
		case graph.Outgoing:
			if e.SourceID == nodeID {
				out = append(out, e)
			}
		case graph.Incoming:
			if e.TargetID == nodeID {
				out = append(out, e)
			}
		default:
			if e.SourceID == nodeID || e.TargetID == nodeID {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

var _ storage.Store = (*memGraph)(nil)
var _ storage.Scanner = (*memGraph)(nil)
