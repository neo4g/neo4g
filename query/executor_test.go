package query

import (
	"fmt"
	"sync"
	"testing"

	"github.com/seankohjs/neo4g/parser"
	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/disk"
	"github.com/seankohjs/neo4g/storage/index"
	"github.com/seankohjs/neo4g/storage/pool"
	"github.com/seankohjs/neo4g/storage/record"
)

// testStack spins up a full storage stack in a temp dir.
func testStack(t *testing.T) (storage.Store, storage.Scanner) {
	t.Helper()
	dir := t.TempDir()
	dm, err := disk.Open(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dm.Close() })

	p := pool.New(dm, 128)
	t.Cleanup(func() { p.FlushAll() })

	idx, err := index.NewManager(p, dm)
	if err != nil {
		t.Fatal(err)
	}
	rm, err := record.NewManager(p, dm, idx)
	if err != nil {
		t.Fatal(err)
	}
	return rm, rm
}

func testExecutor(t *testing.T) *Executor {
	t.Helper()
	store, scanner := testStack(t)
	return NewExecutor(store, scanner)
}

// execute is a shorthand: parse + execute.
func execute(t *testing.T, ex *Executor, q string, params map[string]interface{}) *Result {
	t.Helper()
	stmt, err := parser.Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}
	res, err := ex.Execute(stmt, params)
	if err != nil {
		t.Fatalf("Execute(%q): %v", q, err)
	}
	return res
}

func TestCreateAndMatchReturn(t *testing.T) {
	ex := testExecutor(t)

	// CREATE
	res := execute(t, ex, `CREATE (n:Person {name: "Alice", age: 30})`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 created row, got %d", len(res.Rows))
	}

	// MATCH + RETURN
	res = execute(t, ex, `MATCH (n:Person) RETURN n`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	row := res.Rows[0]
	n, ok := row["n"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected node map, got %T", row["n"])
	}
	props := n["properties"].(map[string]interface{})
	if props["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", props["name"])
	}
}

func TestCreateEdgeAndTraverse(t *testing.T) {
	ex := testExecutor(t)

	execute(t, ex, `CREATE (a:Person {name: "Alice"})`, nil)
	execute(t, ex, `CREATE (b:Person {name: "Bob"})`, nil)

	// Create edge via MATCH + CREATE
	execute(t, ex, `MATCH (a), (b) WHERE a.name = "Alice" AND b.name = "Bob" CREATE (a)-[:KNOWS {since: 2024}]->(b)`, nil)

	// Traverse
	res := execute(t, ex, `MATCH (n:Person)-[:KNOWS]->(m:Person) RETURN n, m`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 traversal row, got %d", len(res.Rows))
	}

	nMap := res.Rows[0]["n"].(map[string]interface{})
	mMap := res.Rows[0]["m"].(map[string]interface{})
	nProps := nMap["properties"].(map[string]interface{})
	mProps := mMap["properties"].(map[string]interface{})
	if nProps["name"] != "Alice" {
		t.Errorf("expected n=Alice, got %v", nProps["name"])
	}
	if mProps["name"] != "Bob" {
		t.Errorf("expected m=Bob, got %v", mProps["name"])
	}
}

func TestMatchWherePropertyFilter(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (a:Person {name: "Alice", age: 30})`, nil)
	execute(t, ex, `CREATE (b:Person {name: "Bob", age: 25})`, nil)

	res := execute(t, ex, `MATCH (n:Person) WHERE n.name = "Alice" RETURN n`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	n := res.Rows[0]["n"].(map[string]interface{})
	if n["properties"].(map[string]interface{})["name"] != "Alice" {
		t.Error("expected Alice")
	}
}

func TestMatchWhereIDFunction(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (a:Person {name: "Alice"})`, nil)
	execute(t, ex, `CREATE (b:Person {name: "Bob"})`, nil)

	// id(n) = 1 should give Alice (first created)
	res := execute(t, ex, `MATCH (n) WHERE id(n) = 1 RETURN n`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	n := res.Rows[0]["n"].(map[string]interface{})
	if n["properties"].(map[string]interface{})["name"] != "Alice" {
		t.Error("expected Alice for id=1")
	}
}

func TestSet(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice", age: 30})`, nil)

	execute(t, ex, `MATCH (n:Person) WHERE n.name = "Alice" SET n.name = "Alicia", n.age = 31`, nil)

	res := execute(t, ex, `MATCH (n:Person) WHERE n.name = "Alicia" RETURN n.name, n.age`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row after SET, got %d", len(res.Rows))
	}
	if res.Rows[0]["n.name"] != "Alicia" {
		t.Errorf("expected Alicia, got %v", res.Rows[0]["n.name"])
	}
	if res.Rows[0]["n.age"] != int64(31) {
		t.Errorf("expected 31, got %v", res.Rows[0]["n.age"])
	}
}

func TestDeleteNode(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice"})`, nil)
	execute(t, ex, `CREATE (n:Person {name: "Bob"})`, nil)

	execute(t, ex, `MATCH (n:Person) WHERE n.name = "Alice" DELETE n`, nil)

	res := execute(t, ex, `MATCH (n:Person) RETURN n`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 remaining row, got %d", len(res.Rows))
	}
	n := res.Rows[0]["n"].(map[string]interface{})
	if n["properties"].(map[string]interface{})["name"] != "Bob" {
		t.Error("expected Bob to survive")
	}
}

func TestDeleteEdge(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (a:Person {name: "Alice"})`, nil)
	execute(t, ex, `CREATE (b:Person {name: "Bob"})`, nil)
	execute(t, ex, `MATCH (a), (b) WHERE a.name = "Alice" AND b.name = "Bob" CREATE (a)-[:KNOWS]->(b)`, nil)

	// Delete the edge
	execute(t, ex, `MATCH (a)-[e:KNOWS]->(b) WHERE a.name = "Alice" DELETE e`, nil)

	// Verify no more edges
	res := execute(t, ex, `MATCH (a:Person)-[:KNOWS]->(b:Person) RETURN a, b`, nil)
	if len(res.Rows) != 0 {
		t.Errorf("expected 0 rows after edge delete, got %d", len(res.Rows))
	}
}

func TestReturnStar(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice"})`, nil)

	res := execute(t, ex, `MATCH (n:Person) RETURN *`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if len(res.Columns) != 1 || res.Columns[0] != "n" {
		t.Errorf("expected columns=[n], got %v", res.Columns)
	}
}

func TestReturnProperties(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice", age: 30})`, nil)

	res := execute(t, ex, `MATCH (n:Person) RETURN n.name, n.age`, nil)
	if len(res.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(res.Columns))
	}
	if res.Rows[0]["n.name"] != "Alice" {
		t.Errorf("expected Alice, got %v", res.Rows[0]["n.name"])
	}
	if res.Rows[0]["n.age"] != int64(30) {
		t.Errorf("expected 30, got %v", res.Rows[0]["n.age"])
	}
}

func TestReturnAlias(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice"})`, nil)

	res := execute(t, ex, `MATCH (n:Person) RETURN n.name AS personName`, nil)
	if len(res.Columns) != 1 || res.Columns[0] != "personName" {
		t.Errorf("expected columns=[personName], got %v", res.Columns)
	}
	if res.Rows[0]["personName"] != "Alice" {
		t.Errorf("expected Alice, got %v", res.Rows[0]["personName"])
	}
}

func TestMatchWithParam(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice"})`, nil)
	execute(t, ex, `CREATE (n:Person {name: "Bob"})`, nil)

	res := execute(t, ex, `MATCH (n:Person) WHERE n.name = $name RETURN n`, map[string]interface{}{"name": "Bob"})
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	n := res.Rows[0]["n"].(map[string]interface{})
	if n["properties"].(map[string]interface{})["name"] != "Bob" {
		t.Error("expected Bob")
	}
}

func TestConcurrentReads(t *testing.T) {
	ex := testExecutor(t)
	for i := 0; i < 10; i++ {
		execute(t, ex, fmt.Sprintf(`CREATE (n:Item {idx: %d})`, i), nil)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	stmt, _ := parser.Parse(`MATCH (n:Item) RETURN n`)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := ex.Execute(stmt, nil)
			if err != nil {
				errs <- err
				return
			}
			if len(res.Rows) != 10 {
				errs <- fmt.Errorf("expected 10 rows, got %d", len(res.Rows))
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func TestErrorUnboundVariable(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice"})`, nil)

	stmt, err := parser.Parse(`MATCH (n:Person) WHERE x.name = "Alice" RETURN n`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ex.Execute(stmt, nil)
	if err == nil {
		t.Fatal("expected error for unbound variable x")
	}
}

func TestErrorUnknownFunction(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice"})`, nil)

	stmt, err := parser.Parse(`MATCH (n:Person) WHERE foo(n) = 1 RETURN n`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ex.Execute(stmt, nil)
	if err == nil {
		t.Fatal("expected error for unknown function foo")
	}
}

func TestMatchEmptyResult(t *testing.T) {
	ex := testExecutor(t)
	res := execute(t, ex, `MATCH (n:NonExistent) RETURN n`, nil)
	if len(res.Rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(res.Rows))
	}
}

func TestMultipleLabels(t *testing.T) {
	ex := testExecutor(t)
	// ScanNodes filters by all labels provided, so create with multiple labels
	store, _ := testStack(t)
	_ = store
	execute(t, ex, `CREATE (n:Person {name: "Alice"})`, nil)
	execute(t, ex, `CREATE (n:Animal {name: "Rex"})`, nil)

	res := execute(t, ex, `MATCH (n:Person) RETURN n`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 Person, got %d", len(res.Rows))
	}

	res = execute(t, ex, `MATCH (n:Animal) RETURN n`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 Animal, got %d", len(res.Rows))
	}
}

func TestCreateReturnsNodeData(t *testing.T) {
	ex := testExecutor(t)
	res := execute(t, ex, `CREATE (n:Person {name: "Alice", age: 30})`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 created, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	labels, ok := row["labels"].([]string)
	if !ok {
		t.Fatalf("expected labels, got %T", row["labels"])
	}
	if len(labels) != 1 || labels[0] != "Person" {
		t.Errorf("expected [Person], got %v", labels)
	}
}

func TestComparisonOperators(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (n:Person {name: "Alice", age: 30})`, nil)
	execute(t, ex, `CREATE (n:Person {name: "Bob", age: 25})`, nil)
	execute(t, ex, `CREATE (n:Person {name: "Charlie", age: 35})`, nil)

	res := execute(t, ex, `MATCH (n:Person) WHERE n.age > 28 RETURN n.name`, nil)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows for age > 28, got %d", len(res.Rows))
	}

	res = execute(t, ex, `MATCH (n:Person) WHERE n.age <= 25 RETURN n.name`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for age <= 25, got %d", len(res.Rows))
	}
	if res.Rows[0]["n.name"] != "Bob" {
		t.Errorf("expected Bob, got %v", res.Rows[0]["n.name"])
	}
}

func TestMatchMultiplePatternsParallel(t *testing.T) {
	ex := testExecutor(t)
	execute(t, ex, `CREATE (a:Person {name: "Alice"})`, nil)
	execute(t, ex, `CREATE (b:Person {name: "Bob"})`, nil)

	// MATCH (a), (b) WHERE ... produces cross product
	res := execute(t, ex, `MATCH (a), (b) WHERE a.name = "Alice" AND b.name = "Bob" RETURN a.name, b.name`, nil)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row from cross product, got %d", len(res.Rows))
	}
	if res.Rows[0]["a.name"] != "Alice" || res.Rows[0]["b.name"] != "Bob" {
		t.Errorf("unexpected row: %v", res.Rows[0])
	}
}
