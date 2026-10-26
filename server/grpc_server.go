package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/neo4g/neo4g/rpcpb"
	"github.com/neo4g/neo4g/storage/wal"
)

// nodeService implements the internal gRPC API.
type nodeService struct {
	rpcpb.UnimplementedNodeServer
	srv *Server
}

func (n *nodeService) Health(context.Context, *rpcpb.HealthRequest) (*rpcpb.HealthResponse, error) {
	h := n.srv.healthView()
	return &rpcpb.HealthResponse{
		Status:      h.Status,
		NodeId:      h.NodeID,
		Role:        h.Role,
		Lsn:         h.LSN,
		PrimaryAddr: h.PrimaryAddr,
	}, nil
}

func (n *nodeService) SetRole(_ context.Context, req *rpcpb.SetRoleRequest) (*rpcpb.SetRoleResponse, error) {
	if err := n.srv.applyRole(req.GetRole(), req.GetPrimaryAddr()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &rpcpb.SetRoleResponse{
		Status: "ok",
		NodeId: n.srv.cfg.NodeID,
		Role:   req.GetRole(),
	}, nil
}

func (n *nodeService) ExecuteQuery(_ context.Context, req *rpcpb.ExecuteQueryRequest) (*rpcpb.ExecuteQueryResponse, error) {
	params, err := decodeParams(req.GetParamsJson())
	if err != nil {
		return &rpcpb.ExecuteQueryResponse{
			HttpStatus: http.StatusBadRequest,
			JsonBody:   errorJSON("invalid params: " + err.Error()),
		}, nil
	}
	code, body := n.srv.executeQuery(req.GetQuery(), params)
	return &rpcpb.ExecuteQueryResponse{
		HttpStatus: int32(code),
		JsonBody:   body,
	}, nil
}

func decodeParams(b []byte) (map[string]interface{}, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var params map[string]interface{}
	if err := json.Unmarshal(b, &params); err != nil {
		return nil, err
	}
	return params, nil
}

func (n *nodeService) PullWAL(_ context.Context, req *rpcpb.PullWALRequest) (*rpcpb.PullWALResponse, error) {
	recs, err := n.srv.pullWAL(wal.LSN(req.GetFromLsn()))
	if err != nil {
		switch {
		case errors.Is(err, errNotPrimary):
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case errors.Is(err, errWALDisabled):
			return nil, status.Error(codes.Unavailable, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
	}
	return &rpcpb.PullWALResponse{Records: recs}, nil
}

func (n *nodeService) Checksum(context.Context, *rpcpb.ChecksumRequest) (*rpcpb.ChecksumResponse, error) {
	c, err := n.srv.checksumView()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &rpcpb.ChecksumResponse{
		Checksum:  c.Checksum,
		PageCount: c.PageCount,
		Lsn:       c.LSN,
	}, nil
}
