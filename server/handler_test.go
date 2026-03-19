package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	cfg := testConfig(t)
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(func() {
		ts.Close()
		srv.engine.Close()
	})
	return srv, ts
}

func postQuery(ts *httptest.Server, query string, params map[string]interface{}) (*http.Response, map[string]interface{}) {
	body := map[string]interface{}{"query": query}
	if params != nil {
		body["params"] = params
	}
	b, _ := json.Marshal(body)
	resp, _ := http.Post(ts.URL+"/api/query", "application/json", bytes.NewReader(b))
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	return resp, result
}

func TestQueryCreateAndReturn(t *testing.T) {
	_, ts := testServer(t)

	// CREATE a node
	resp, body := postQuery(ts, `CREATE (n:Person {name: "Alice", age: 30})`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CREATE: expected 200, got %d: %v", resp.StatusCode, body)
	}

	// MATCH + RETURN
	resp, body = postQuery(ts, `MATCH (n:Person) RETURN n`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MATCH: expected 200, got %d: %v", resp.StatusCode, body)
	}

	data := body["data"].(map[string]interface{})
	rows := data["rows"].([]interface{})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	row := rows[0].(map[string]interface{})
	n := row["n"].(map[string]interface{})
	props := n["properties"].(map[string]interface{})
	if props["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", props["name"])
	}
}

func TestQueryReturnProperties(t *testing.T) {
	_, ts := testServer(t)

	postQuery(ts, `CREATE (n:Person {name: "Bob", age: 25})`, nil)

	resp, body := postQuery(ts, `MATCH (n:Person) RETURN n.name, n.age`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	data := body["data"].(map[string]interface{})
	rows := data["rows"].([]interface{})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0].(map[string]interface{})
	if row["n.name"] != "Bob" {
		t.Errorf("expected Bob, got %v", row["n.name"])
	}
	// JSON numbers are float64
	if row["n.age"] != float64(25) {
		t.Errorf("expected 25, got %v (type %T)", row["n.age"], row["n.age"])
	}
}

func TestQueryWithParams(t *testing.T) {
	_, ts := testServer(t)

	postQuery(ts, `CREATE (n:Person {name: "Alice"})`, nil)
	postQuery(ts, `CREATE (n:Person {name: "Bob"})`, nil)

	resp, body := postQuery(ts, `MATCH (n:Person) WHERE n.name = $name RETURN n.name`, map[string]interface{}{"name": "Alice"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	data := body["data"].(map[string]interface{})
	rows := data["rows"].([]interface{})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0].(map[string]interface{})
	if row["n.name"] != "Alice" {
		t.Errorf("expected Alice, got %v", row["n.name"])
	}
}

func TestQueryEmptyQuery(t *testing.T) {
	_, ts := testServer(t)

	resp, body := postQuery(ts, "", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if body["error"] == nil {
		t.Error("expected error field in response")
	}
}

func TestQueryBadJSON(t *testing.T) {
	_, ts := testServer(t)

	resp, _ := http.Post(ts.URL+"/api/query", "application/json", bytes.NewReader([]byte("not json")))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestQueryParseError(t *testing.T) {
	_, ts := testServer(t)

	resp, body := postQuery(ts, `MATCH (n:Person RETURN n`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for parse error, got %d", resp.StatusCode)
	}
	errMsg, ok := body["error"].(string)
	if !ok || errMsg == "" {
		t.Error("expected parse error message")
	}
}

func TestConcurrentQueries(t *testing.T) {
	_, ts := testServer(t)

	// Seed data
	for i := range 10 {
		postQuery(ts, fmt.Sprintf(`CREATE (n:Item {idx: %d})`, i), nil)
	}

	const goroutines = 50
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, body := postQuery(ts, `MATCH (n:Item) RETURN n`, nil)
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("status %d: %v", resp.StatusCode, body)
				return
			}
			data := body["data"].(map[string]interface{})
			rows := data["rows"].([]interface{})
			if len(rows) != 10 {
				errs <- fmt.Errorf("expected 10 rows, got %d", len(rows))
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func TestGracefulShutdown(t *testing.T) {
	cfg := testConfig(t)
	cfg.ListenAddr = "127.0.0.1:0"

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	nid, err := srv.engine.Store().CreateNode([]string{"Test"}, nil)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	e2, err := Open(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer e2.Close()

	node, err := e2.Store().GetNode(nid)
	if err != nil {
		t.Fatalf("GetNode after reopen: %v", err)
	}
	if len(node.Labels) != 1 || node.Labels[0] != "Test" {
		t.Errorf("unexpected labels: %v", node.Labels)
	}
}
