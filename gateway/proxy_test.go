package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAddrIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.RemoteAddr = "9.9.9.9:1234"
	if got := clientAddr(r); got != "9.9.9.9" {
		t.Fatalf("client = %q", got)
	}
}

func TestReadsWaitForAppliedLSN(t *testing.T) {
	dial := &fakeDialer{nodes: map[string]*fakeNode{
		"p": {role: "primary", lsn: 4},
		"r": {role: "replica", lsn: 4},
	}}
	c := NewCluster([]NodeConfig{{ID: "p", Addr: "p"}, {ID: "r", Addr: "r"}})
	c.SetPrimary("p")
	c.UpdateHealth("p", true, "primary", 4, 1)
	c.UpdateHealth("r", true, "replica", 4, 1)
	g := New(DefaultConfig(), c, dial)

	post := func(query string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(`{"query":`+quote(query)+`}`))
		req.RemoteAddr = "10.1.1.1:9"
		req.Header.Set("X-Forwarded-For", "8.8.8.8")
		rr := httptest.NewRecorder()
		g.handleQuery(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d body %s", query, rr.Code, rr.Body.String())
		}
	}

	post(`CREATE (n:Person {name: "Ada"})`)
	post(`MATCH (n:Person) RETURN n.name`)
	if got := dial.queries[len(dial.queries)-1].Addr; got != "p" {
		t.Fatalf("read after write went to %s, want primary", got)
	}

	c.UpdateHealth("r", true, "replica", 9, 1)
	post(`MATCH (n:Person) RETURN n.name`)
	if got := dial.queries[len(dial.queries)-1].Addr; got != "r" {
		t.Fatalf("caught-up read went to %s, want replica", got)
	}
}

func TestFailedWriteDoesNotPinReads(t *testing.T) {
	dial := &fakeDialer{
		failWrite: true,
		nodes: map[string]*fakeNode{
			"p": {role: "primary", lsn: 10},
			"r": {role: "replica", lsn: 1},
		},
	}
	c := NewCluster([]NodeConfig{{ID: "p", Addr: "p"}, {ID: "r", Addr: "r"}})
	c.SetPrimary("p")
	c.UpdateHealth("p", true, "primary", 10, 1)
	c.UpdateHealth("r", true, "replica", 1, 1)
	g := New(DefaultConfig(), c, dial)

	req := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(`{"query":"CREATE (n:Person {name: \"Ada\"})"}`))
	req.RemoteAddr = "10.2.2.2:9"
	rr := httptest.NewRecorder()
	g.handleQuery(rr, req)

	dial.failWrite = false
	req = httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(`{"query":"MATCH (n:Person) RETURN n.name"}`))
	req.RemoteAddr = "10.2.2.2:9"
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	rr = httptest.NewRecorder()
	g.handleQuery(rr, req)
	if got := dial.queries[len(dial.queries)-1].Addr; got != "r" {
		t.Fatalf("read after failed write went to %s, want replica", got)
	}
}

func TestGatewayHealthNoLeader(t *testing.T) {
	g := New(DefaultConfig(), NewCluster([]NodeConfig{{ID: "a", Addr: "a:1"}}), &fakeDialer{})
	rr := httptest.NewRecorder()
	g.handleHealth(rr, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if !strings.Contains(rr.Body.String(), `"status":"no_leader"`) {
		t.Fatalf("body %s", rr.Body.String())
	}
}

func TestQueryBodyLimit(t *testing.T) {
	g := New(DefaultConfig(), NewCluster(nil), &fakeDialer{})
	req := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(strings.Repeat("x", 2<<20)))
	rr := httptest.NewRecorder()
	g.handleQuery(rr, req)
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", rr.Code)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
