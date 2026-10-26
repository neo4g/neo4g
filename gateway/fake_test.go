package gateway

import (
	"context"
	"strings"
	"sync"

	"google.golang.org/grpc"

	"github.com/neo4g/neo4g/rpcpb"
)

type roleCall struct {
	Addr    string
	Role    string
	Primary string
	Gen     uint64
}

type queryCall struct {
	Addr  string
	Query string
}

type fakeNode struct {
	role string
	lsn  uint64
}

type fakeDialer struct {
	mu        sync.Mutex
	fail      map[string]error
	calls     []roleCall
	queries   []queryCall
	nodes     map[string]*fakeNode
	failWrite bool
}

func (d *fakeDialer) Client(addr string) (rpcpb.NodeClient, error) {
	return &fakeClient{d: d, addr: addr}, nil
}

type fakeClient struct {
	d    *fakeDialer
	addr string
}

func (c *fakeClient) Health(context.Context, *rpcpb.HealthRequest, ...grpc.CallOption) (*rpcpb.HealthResponse, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	n := c.d.nodes[c.addr]
	if n == nil {
		return &rpcpb.HealthResponse{Status: "ok", Lsn: 0}, nil
	}
	return &rpcpb.HealthResponse{Status: "ok", Role: n.role, Lsn: n.lsn}, nil
}

func (c *fakeClient) SetRole(ctx context.Context, req *rpcpb.SetRoleRequest, _ ...grpc.CallOption) (*rpcpb.SetRoleResponse, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if err := c.d.fail[c.addr]; err != nil {
		return nil, err
	}
	gen := rpcpb.GenerationFromContext(ctx)
	c.d.calls = append(c.d.calls, roleCall{
		Addr: c.addr, Role: req.GetRole(), Primary: req.GetPrimaryAddr(), Gen: gen,
	})
	n := c.d.nodes[c.addr]
	if n == nil {
		n = &fakeNode{}
		c.d.nodes[c.addr] = n
	}
	n.role = req.GetRole()
	return &rpcpb.SetRoleResponse{Status: "ok", Role: req.GetRole()}, nil
}

func (c *fakeClient) ExecuteQuery(_ context.Context, req *rpcpb.ExecuteQueryRequest, _ ...grpc.CallOption) (*rpcpb.ExecuteQueryResponse, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	c.d.queries = append(c.d.queries, queryCall{Addr: c.addr, Query: req.GetQuery()})
	if c.d.failWrite && strings.Contains(req.GetQuery(), "CREATE") {
		return &rpcpb.ExecuteQueryResponse{HttpStatus: 500, JsonBody: []byte(`{"error":"nope"}`)}, nil
	}
	if n := c.d.nodes[c.addr]; n != nil && strings.Contains(req.GetQuery(), "CREATE") {
		n.lsn += 5
	}
	return &rpcpb.ExecuteQueryResponse{HttpStatus: 200, JsonBody: []byte(`{"data":{}}`)}, nil
}

func (c *fakeClient) PullWAL(context.Context, *rpcpb.PullWALRequest, ...grpc.CallOption) (*rpcpb.PullWALResponse, error) {
	return &rpcpb.PullWALResponse{}, nil
}

func (c *fakeClient) Checksum(context.Context, *rpcpb.ChecksumRequest, ...grpc.CallOption) (*rpcpb.ChecksumResponse, error) {
	return &rpcpb.ChecksumResponse{}, nil
}
