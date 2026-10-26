package gateway

import (
	"errors"
	"fmt"
	"sync"

	"google.golang.org/grpc"

	"github.com/neo4g/neo4g/rpcpb"
)

// Dialer caches one gRPC connection per node address.
type Dialer struct {
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

// NewDialer returns an empty connection cache.
func NewDialer() *Dialer {
	return &Dialer{conns: make(map[string]*grpc.ClientConn)}
}

// Client returns a Node client for addr, dialing on first use.
func (d *Dialer) Client(addr string) (rpcpb.NodeClient, error) {
	target, err := rpcpb.Target(addr)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	conn, ok := d.conns[target]
	if !ok {
		conn, err = rpcpb.Dial(target)
		if err != nil {
			return nil, err
		}
		d.conns[target] = conn
	}
	return rpcpb.NewNodeClient(conn), nil
}

// Close closes every cached connection.
func (d *Dialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var errs []error
	for addr, conn := range d.conns {
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", addr, err))
		}
	}
	d.conns = map[string]*grpc.ClientConn{}
	return errors.Join(errs...)
}
