package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"

	"github.com/neo4g/neo4g/query"
	"github.com/neo4g/neo4g/rpcpb"
)

// Server wraps the database Engine with an HTTP API.
type Server struct {
	engine   *Engine
	executor *query.Executor
	http     *http.Server
	cfg      Config

	mu          sync.RWMutex
	role        string // "primary" or "replica"
	primaryAddr string // gRPC address of the primary (used by replicas)
	fenceGen    uint64 // highest role generation accepted from the gateway
	repl        *Replicator
	tipMu       sync.Mutex // serializes applied.lsn updates on the primary

	ready     chan struct{}
	readyOnce sync.Once
	serveErr  error
	grpcSrv   atomic.Pointer[grpc.Server]
	boundHTTP string
	boundGRPC string
}

// NewServer opens the database engine and sets up the HTTP server.
func NewServer(cfg Config) (*Server, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	engine, err := Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("open engine: %w", err)
	}

	s := &Server{
		engine:      engine,
		executor:    query.NewExecutor(engine.Store(), engine.Scanner()),
		cfg:         cfg,
		role:        cfg.Role,
		primaryAddr: cfg.PrimaryAddr,
		ready:       make(chan struct{}),
	}

	s.http = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      corsMiddleware(s.routes()),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	if cfg.Role == "replica" {
		s.startReplicator()
	} else {
		s.continuePrimaryLSN()
	}

	return s, nil
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Role == "" {
		cfg.Role = "primary"
	}
	switch cfg.Role {
	case "primary", "replica":
	default:
		return cfg, fmt.Errorf("invalid role %q: NEO4G_ROLE must be \"primary\" or \"replica\"", cfg.Role)
	}
	if cfg.Role == "replica" && strings.TrimSpace(cfg.PrimaryAddr) == "" {
		return cfg, fmt.Errorf("NEO4G_PRIMARY_ADDR is required when NEO4G_ROLE=replica")
	}
	return cfg, nil
}

// Engine returns the underlying database engine for direct programmatic use.
func (s *Server) Engine() *Engine { return s.engine }

// Role returns the current replication role of this node.
func (s *Server) Role() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.role
}

// SetRole updates the replication role.
func (s *Server) SetRole(role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.role = role
}

// SetPrimaryAddr updates the primary address used for WAL pull replication.
func (s *Server) SetPrimaryAddr(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.primaryAddr = addr
}

func (s *Server) getPrimaryAddr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.primaryAddr
}

func (s *Server) startReplicator() {
	s.mu.Lock()
	old := s.repl
	s.repl = nil
	addr := s.primaryAddr
	interval := s.cfg.ReplPollInterval
	s.mu.Unlock()
	if old != nil {
		old.Stop()
	}
	if addr == "" {
		return
	}
	repl := NewReplicator(s.engine, addr, interval)
	s.mu.Lock()
	if s.role != "replica" || s.primaryAddr != addr {
		s.mu.Unlock()
		return
	}
	s.repl = repl
	s.mu.Unlock()
	repl.Start()
	log.Printf("neo4g: replicator started, pulling from %s", addr)
}

func (s *Server) stopReplicator() {
	s.mu.Lock()
	repl := s.repl
	s.repl = nil
	s.mu.Unlock()
	if repl != nil {
		repl.Stop()
		log.Println("neo4g: replicator stopped")
	}
}

func (s *Server) currentRepl() *Replicator {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.repl
}

func (s *Server) replicationErr() error {
	repl := s.currentRepl()
	if repl == nil {
		return nil
	}
	return repl.Err()
}

func (s *Server) fenceGeneration() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fenceGen
}

// ListenAndServe starts the public HTTP server and the internal gRPC server.
// It blocks until HTTP is shut down or encounters a fatal error.
func (s *Server) ListenAndServe() error {
	httpLn, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		err = fmt.Errorf("listen http: %w", err)
		s.markReady(err)
		return err
	}
	grpcAddr := s.cfg.GRPCAddr
	if grpcAddr == "" {
		grpcAddr = ":9474"
	}
	grpcLn, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		httpLn.Close()
		err = fmt.Errorf("listen grpc: %w", err)
		s.markReady(err)
		return err
	}

	gs := grpc.NewServer()
	rpcpb.RegisterNodeServer(gs, &nodeService{srv: s})
	s.grpcSrv.Store(gs)
	s.boundHTTP = httpLn.Addr().String()
	s.boundGRPC = grpcLn.Addr().String()
	s.markReady(nil)

	go func() {
		if err := gs.Serve(grpcLn); err != nil {
			log.Printf("neo4g: grpc server stopped: %v", err)
		}
	}()

	log.Printf("neo4g: listening on %s (role=%s, node=%s)", httpLn.Addr(), s.Role(), s.cfg.NodeID)
	log.Printf("neo4g: grpc on %s", grpcLn.Addr())
	err = s.http.Serve(httpLn)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) markReady(err error) {
	s.readyOnce.Do(func() {
		s.serveErr = err
		close(s.ready)
	})
}

// WaitReady blocks until both listeners are bound, or ctx ends.
func (s *Server) WaitReady(ctx context.Context) error {
	select {
	case <-s.ready:
		return s.serveErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BoundHTTPAddr returns the address HTTP is serving on. Valid after WaitReady.
func (s *Server) BoundHTTPAddr() string { return s.boundHTTP }

// BoundGRPCAddr returns the address the internal gRPC server is serving on.
// Valid after WaitReady.
func (s *Server) BoundGRPCAddr() string { return s.boundGRPC }

// Shutdown stops replication, drains gRPC and HTTP, checkpoints the WAL,
// and closes the database engine.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.Role() == "primary" {
		s.notePrimaryTip()
	}
	s.stopReplicator()

	var errs []error

	if gs := s.grpcSrv.Load(); gs != nil {
		stopped := make(chan struct{})
		go func() {
			gs.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-ctx.Done():
			gs.Stop()
		}
	}

	if err := s.http.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}

	if err := s.engine.Close(); err != nil {
		errs = append(errs, fmt.Errorf("engine close: %w", err))
	}

	return errors.Join(errs...)
}
