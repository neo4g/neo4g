// Package server provides the top-level database engine that wires all
// storage components together behind a single Open/Close lifecycle.
package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/disk"
	"github.com/seankohjs/neo4g/storage/index"
	"github.com/seankohjs/neo4g/storage/pool"
	"github.com/seankohjs/neo4g/storage/record"
	"github.com/seankohjs/neo4g/storage/txn"
	"github.com/seankohjs/neo4g/storage/wal"
)

// Engine is the top-level database handle. Create one with Open(),
// use Store()/Scanner() for graph operations, and call Close() when done.
type Engine struct {
	cfg  Config
	dm   *disk.Manager
	w    *wal.WAL
	pool *pool.Pool
	idx  *index.Manager
	rm   *record.Manager
	txm  *txn.Manager
}

// Open initialises every storage layer according to cfg and returns a
// ready-to-use Engine. If a WAL file from a previous crash is found,
// recovery runs automatically before the engine becomes usable.
func Open(cfg Config) (*Engine, error) {
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	e := &Engine{cfg: cfg}

	dm, err := disk.Open(filepath.Join(cfg.DataDir, cfg.DBFile))
	if err != nil {
		return nil, fmt.Errorf("open disk manager: %w", err)
	}
	e.dm = dm

	if cfg.WALEnabled {
		w, err := wal.Open(filepath.Join(cfg.DataDir, cfg.WALFile))
		if err != nil {
			dm.Close()
			return nil, fmt.Errorf("open WAL: %w", err)
		}
		e.w = w

		if redone, undone, err := txn.Recover(w, dm); err != nil {
			w.Close()
			dm.Close()
			return nil, fmt.Errorf("recovery: %w", err)
		} else if redone > 0 || undone > 0 {
			fmt.Printf("neo4g: recovery complete — %d redone, %d undone\n", redone, undone)
		}
	}

	e.pool = pool.New(dm, cfg.PoolSize)

	idx, err := index.LoadManager(e.pool, dm)
	if err != nil {
		e.closePartial()
		return nil, fmt.Errorf("load indexes: %w", err)
	}
	if idx == nil {
		idx, err = index.NewManager(e.pool, dm)
		if err != nil {
			e.closePartial()
			return nil, fmt.Errorf("create indexes: %w", err)
		}
	}
	e.idx = idx

	rm, err := record.NewManager(e.pool, dm, idx)
	if err != nil {
		e.closePartial()
		return nil, fmt.Errorf("create record manager: %w", err)
	}
	e.rm = rm

	if cfg.WALEnabled {
		e.txm = txn.NewManager(e.w, e.pool, dm)
		e.txm.NoSync = cfg.WALNoSync
		rm.SetTxManager(e.txm)
	}

	return e, nil
}

// Store returns the graph CRUD interface.
func (e *Engine) Store() storage.Store { return e.rm }

// Scanner returns the read-path scan/expand interface.
func (e *Engine) Scanner() storage.Scanner { return e.rm }

// Pool returns the underlying buffer pool.
func (e *Engine) Pool() *pool.Pool { return e.pool }

// WAL returns the underlying write-ahead log, or nil if WAL is disabled.
func (e *Engine) WAL() *wal.WAL { return e.w }

// DiskManager returns the underlying disk manager.
func (e *Engine) DiskManager() *disk.Manager { return e.dm }

// IndexManager returns the underlying index manager.
func (e *Engine) IndexManager() *index.Manager { return e.idx }

// Checkpoint forces a WAL checkpoint (flush + truncate). No-op if WAL is disabled.
func (e *Engine) Checkpoint() error {
	if e.txm == nil {
		return nil
	}
	return e.txm.Checkpoint()
}

// Close flushes all dirty pages, checkpoints the WAL, and releases resources.
func (e *Engine) Close() error {
	var errs []error

	if e.pool != nil {
		if err := e.pool.FlushAll(); err != nil {
			errs = append(errs, fmt.Errorf("flush pool: %w", err))
		}
	}

	if e.txm != nil {
		if err := e.txm.Checkpoint(); err != nil {
			errs = append(errs, fmt.Errorf("checkpoint: %w", err))
		}
	}

	if e.w != nil {
		if err := e.w.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close WAL: %w", err))
		}
	}

	if e.dm != nil {
		if err := e.dm.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close disk: %w", err))
		}
	}

	return errors.Join(errs...)
}

// closePartial is used during Open() error paths to tear down
// whatever was already initialised.
func (e *Engine) closePartial() {
	if e.w != nil {
		e.w.Close()
	}
	if e.dm != nil {
		e.dm.Close()
	}
}
