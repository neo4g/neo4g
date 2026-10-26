package gateway

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testElection(t *testing.T, nodes []NodeConfig, dial *fakeDialer) (*Cluster, *Election) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.ElectionDelay = 0
	cfg.HeartbeatInterval = 10 * time.Millisecond
	cfg.HeartbeatTimeout = 30 * time.Millisecond
	c := NewCluster(nodes)
	e := NewElection(cfg, c, dial)
	return c, e
}

func TestElectionPromotesHighestAppliedLSN(t *testing.T) {
	dial := &fakeDialer{nodes: map[string]*fakeNode{
		"a:1": {role: "replica", lsn: 10},
		"b:1": {role: "replica", lsn: 80},
		"c:1": {role: "replica", lsn: 40},
	}}
	c, e := testElection(t, []NodeConfig{
		{ID: "a", Addr: "a:1"},
		{ID: "b", Addr: "b:1"},
		{ID: "c", Addr: "c:1"},
	}, dial)
	c.UpdateHealth("a", true, "replica", 10, 0)
	c.UpdateHealth("b", true, "replica", 80, 0)
	c.UpdateHealth("c", true, "replica", 40, 0)

	e.Run()

	if got := c.PrimaryID(); got != "b" {
		t.Fatalf("primary = %q, want b", got)
	}
	if len(dial.calls) == 0 || dial.calls[0].Role != "primary" || dial.calls[0].Addr != "b:1" {
		t.Fatalf("first SetRole = %+v, want promote b", dial.calls)
	}
	if dial.calls[0].Gen == 0 {
		t.Fatal("promotion missing generation")
	}
}

func TestStaleSelfReportedPrimaryIsIgnored(t *testing.T) {
	c, e := testElection(t, []NodeConfig{
		{ID: "a", Addr: "a:1"},
		{ID: "b", Addr: "b:1"},
	}, &fakeDialer{nodes: map[string]*fakeNode{}})
	c.SetPrimary("a")
	c.UpdateHealth("a", true, "primary", 5, 1)
	c.UpdateHealth("b", true, "primary", 9, 2)
	if got := c.PrimaryID(); got != "a" {
		t.Fatalf("self-report stole primary: %s", got)
	}
	if c.NodeByID("b").ReportedRole != "primary" {
		t.Fatalf("reported role = %s", c.NodeByID("b").ReportedRole)
	}

	e.Demote("b")
	e.Demote("a")
	d := e.dialer.(*fakeDialer)
	if len(d.calls) != 1 || d.calls[0].Addr != "b:1" || d.calls[0].Role != "replica" {
		t.Fatalf("demote calls = %+v", d.calls)
	}
	if d.calls[0].Gen <= 2 {
		t.Fatalf("demote generation %d, want > 2", d.calls[0].Gen)
	}
}

func TestElectionDoesNotReplaceUnfencedPrimary(t *testing.T) {
	dial := &fakeDialer{
		fail: map[string]error{"a:1": errors.New("still writable")},
		nodes: map[string]*fakeNode{
			"a:1": {role: "primary", lsn: 5},
			"b:1": {role: "primary", lsn: 20},
		},
	}
	c, e := testElection(t, []NodeConfig{
		{ID: "a", Addr: "a:1"},
		{ID: "b", Addr: "b:1"},
	}, dial)
	c.UpdateHealth("a", true, "primary", 5, 0)
	c.UpdateHealth("b", true, "primary", 20, 0)

	e.Run()

	if got := c.PrimaryID(); got != "" {
		t.Fatalf("primary replaced without a fence: %s", got)
	}
	for _, call := range dial.calls {
		if call.Role == "primary" {
			t.Fatalf("promoted despite fence failure: %+v", dial.calls)
		}
	}
}

func TestElectionRefusesUnknownAppliedLSN(t *testing.T) {
	c, e := testElection(t, []NodeConfig{
		{ID: "a", Addr: "a:1"},
		{ID: "b", Addr: "b:1"},
	}, &fakeDialer{nodes: map[string]*fakeNode{}})
	c.mu.Lock()
	c.nodes["a"].Alive = true
	c.nodes["a"].LSNKnown = false
	c.nodes["b"].Alive = true
	c.nodes["b"].LSNKnown = true
	c.nodes["b"].LSN = 3
	c.mu.Unlock()

	if _, ok := c.ElectionCandidates(); ok {
		t.Fatal("candidates ok with an unknown cursor")
	}
	e.Run()
	if c.PrimaryID() != "" {
		t.Fatalf("elected with an unknown cursor: %s", c.PrimaryID())
	}
}

func TestNoteFailureRequiresConsecutiveMisses(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.consecutiveFailures(); got < 2 {
		t.Fatalf("threshold %d", got)
	}
	c := NewCluster([]NodeConfig{{ID: "a", Addr: "a:1"}})
	c.UpdateHealth("a", true, "primary", 1, 1)
	c.SetPrimary("a")
	threshold := cfg.consecutiveFailures()
	for i := 1; i < threshold; i++ {
		c.NoteFailure("a", threshold)
		if !c.NodeByID("a").Alive || c.IsPrimaryDead() {
			t.Fatalf("dead after %d/%d failures", i, threshold)
		}
	}
	c.NoteFailure("a", threshold)
	if !c.IsPrimaryDead() {
		t.Fatal("primary still alive after timeout window")
	}
}

func TestShutdownDropsElectionRPCs(t *testing.T) {
	dial := &fakeDialer{nodes: map[string]*fakeNode{}}
	_, e := testElection(t, []NodeConfig{{ID: "a", Addr: "a:1"}}, dial)
	e.Shutdown()
	e.Trigger()
	e.TriggerDemote("a")
	if len(dial.calls) != 0 {
		t.Fatalf("SetRole after shutdown: %+v", dial.calls)
	}
}

func TestReadNodeRequiresClientLSN(t *testing.T) {
	c := NewCluster([]NodeConfig{
		{ID: "p", Addr: "p"},
		{ID: "r1", Addr: "r1"},
		{ID: "r2", Addr: "r2"},
	})
	c.SetPrimary("p")
	c.UpdateHealth("p", true, "primary", 100, 1)
	c.UpdateHealth("r1", true, "replica", 10, 1)
	c.UpdateHealth("r2", true, "replica", 50, 1)

	if got := c.ReadNode(40); got == nil || got.ID != "r2" {
		t.Fatalf("ReadNode(40) = %+v", got)
	}
	if got := c.ReadNode(80); got == nil || got.ID != "p" {
		t.Fatalf("ReadNode(80) = %+v, want primary", got)
	}
	if got := c.ReadNode(0); got == nil || !strings.HasPrefix(got.ID, "r") {
		t.Fatalf("ReadNode(0) = %+v, want a replica", got)
	}
}
