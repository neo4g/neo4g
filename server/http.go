package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
)

// Server wraps the database Engine with an HTTP API.
type Server struct {
	engine *Engine
	http   *http.Server
	cfg    Config
}

// NewServer opens the database engine and sets up the HTTP server.
func NewServer(cfg Config) (*Server, error) {
	engine, err := Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("open engine: %w", err)
	}

	s := &Server{
		engine: engine,
		cfg:    cfg,
	}

	s.http = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      s.routes(),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	return s, nil
}

// Engine returns the underlying database engine for direct programmatic use.
func (s *Server) Engine() *Engine { return s.engine }

// ListenAndServe starts serving HTTP requests. It blocks until the server
// is shut down or encounters a fatal error.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Printf("neo4g: listening on %s", ln.Addr())
	err = s.http.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully drains HTTP connections, checkpoints the WAL,
// and closes the database engine.
func (s *Server) Shutdown(ctx context.Context) error {
	var errs []error

	if err := s.http.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}

	if err := s.engine.Close(); err != nil {
		errs = append(errs, fmt.Errorf("engine close: %w", err))
	}

	return errors.Join(errs...)
}
