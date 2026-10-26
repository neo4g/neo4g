package rpcpb

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"
)

// GenerationHeader carries the gateway's monotonic role generation.
// SetRole rejects a value older than the generation the node already
// accepted. It is gRPC metadata so the existing Node messages stay put.
const GenerationHeader = "x-neo4g-generation"

// WithGeneration attaches a role generation to an outgoing SetRole call.
func WithGeneration(ctx context.Context, generation uint64) context.Context {
	return metadata.AppendToOutgoingContext(ctx, GenerationHeader, strconv.FormatUint(generation, 10))
}

// GenerationFromContext reads the role generation from incoming metadata,
// then outgoing metadata. Outgoing metadata matters for in-process fakes
// that never cross a gRPC transport.
func GenerationFromContext(ctx context.Context) uint64 {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if md.Get(GenerationHeader) != nil {
			return generationFromMD(md)
		}
	}
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		return generationFromMD(md)
	}
	return 0
}

// GenerationFromHeader reads the generation a node attached to its Health headers.
func GenerationFromHeader(md metadata.MD) uint64 {
	return generationFromMD(md)
}

func generationFromMD(md metadata.MD) uint64 {
	vals := md.Get(GenerationHeader)
	if len(vals) == 0 {
		return 0
	}
	n, err := strconv.ParseUint(vals[len(vals)-1], 10, 64)
	if err != nil {
		return 0
	}
	return n
}
