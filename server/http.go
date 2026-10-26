package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
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
	repl        *Replicator

	ready     chan struct{}
	readyOnce sync.Once
	serveErr  error
	grpcSrv   atomic.Pointer[grpc.Server]
	boundHTTP string
	boundGRPC string
}

// NewServer opens the database engine and sets up the HTTP server.
func NewServer(cfg Config) (*Server, error) {
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

	if cfg.Role == "replica" && cfg.PrimaryAddr != "" {
		s.startReplicator()
	}

	return s, nil
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
	defer s.mu.Unlock()
	if s.repl != nil {
		s.repl.Stop()
	}
	s.repl = NewReplicator(s.engine, s.primaryAddr, s.cfg.ReplPollInterval)
	s.repl.Start()
	log.Printf("neo4g: replicator started, pulling from %s", s.primaryAddr)
}

func (s *Server) stopReplicator() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repl != nil {
		s.repl.Stop()
		s.repl = nil
		log.Println("neo4g: replicator stopped")
	}
}

func (s *Server) replicatorRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.repl != nil
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
