package rpcpb

import "testing"

func TestTarget(t *testing.T) {
	got, err := Target("http://127.0.0.1:9474/ignored")
	if err != nil {
		t.Fatal(err)
	}
	if got != "127.0.0.1:9474" {
		t.Fatalf("got %q", got)
	}
	if _, err := Target("localhost"); err == nil {
		t.Fatal("expected error for address without a port")
	}
}
