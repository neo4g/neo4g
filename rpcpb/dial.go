package rpcpb

import (
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Target normalizes an internal node address to host:port.
// A leading http:// or https:// scheme is stripped; gRPC does not use it.
func Target(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		addr = addr[:i]
	}
	if addr == "" || strings.IndexByte(addr, ':') < 0 {
		return "", fmt.Errorf("grpc address %q must be host:port", addr)
	}
	return addr, nil
}

// Dial opens a lazy, insecure connection to a node's internal gRPC address.
func Dial(addr string) (*grpc.ClientConn, error) {
	target, err := Target(addr)
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
}
