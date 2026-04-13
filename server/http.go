package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/seankohjs/neo4g/query"
)

// Server wraps the database Engine with an HTTP API.
type Server struct {
	engine   *Engine
	executor *query.Executor
	http     *http.Server
	cfg      Config

	mu          sync.RWMutex
	role        string // "primary" or "replica"
	primaryAddr string // address of primary (used by replicas)
	repl        *Replicator
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

// ListenAndServe starts serving HTTP requests. It blocks until the server
// is shut down or encounters a fatal error.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Printf("neo4g: listening on %s (role=%s, node=%s)", ln.Addr(), s.Role(), s.cfg.NodeID)
	err = s.http.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully drains HTTP connections, checkpoints the WAL,
// and closes the database engine.
func (s *Server) Shutdown(ctx context.Context) error {
	s.stopReplicator()

	var errs []error

	if err := s.http.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}

	if err := s.engine.Close(); err != nil {
		errs = append(errs, fmt.Errorf("engine close: %w", err))
	}

	return errors.Join(errs...)
}
