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

func TestQueryEndpoint(t *testing.T) {
	_, ts := testServer(t)

	resp, body := postQuery(ts, "MATCH (n:Person) RETURN n", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, body)
	}

	data := body["data"].(map[string]interface{})
	if data["query"] != "MATCH (n:Person) RETURN n" {
		t.Errorf("query echo mismatch: %v", data["query"])
	}
	if data["status"] != "query engine not connected yet" {
		t.Errorf("unexpected status: %v", data["status"])
	}
}

func TestQueryEndpointWithParams(t *testing.T) {
	_, ts := testServer(t)

	resp, body := postQuery(ts, "MATCH (n) WHERE n.name = $name RETURN n", map[string]interface{}{
		"name": "Alice",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	data := body["data"].(map[string]interface{})
	params := data["params"].(map[string]interface{})
	if params["name"] != "Alice" {
		t.Errorf("param mismatch: %v", params["name"])
	}
}

func TestQueryEndpointEmptyQuery(t *testing.T) {
	_, ts := testServer(t)

	resp, body := postQuery(ts, "", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if body["error"] == nil {
		t.Error("expected error field in response")
	}
}

func TestQueryEndpointBadJSON(t *testing.T) {
	_, ts := testServer(t)

	resp, _ := http.Post(ts.URL+"/api/query", "application/json", bytes.NewReader([]byte("not json")))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestConcurrentQueries(t *testing.T) {
	_, ts := testServer(t)

	const goroutines = 50
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := fmt.Sprintf("MATCH (n) WHERE n.id = %d RETURN n", i)
			resp, body := postQuery(ts, q, nil)
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("goroutine %d: status %d: %v", i, resp.StatusCode, body)
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
