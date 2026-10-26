package server

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all tunables for the database engine and HTTP server.
type Config struct {
	// Storage
	DataDir    string // directory for data + WAL files
	DBFile     string // data file name (relative to DataDir)
	WALEnabled bool   // enable write-ahead log for crash recovery
	WALFile    string // WAL file name (relative to DataDir)
	PoolSize   int    // buffer pool frame count

	// WAL
	WALNoSync bool // skip fsync per commit (faster writes, small durability risk)

	// HTTP
	ListenAddr      string        // address to bind the public HTTP server
	ReadTimeout     time.Duration // max duration for reading request
	WriteTimeout    time.Duration // max duration for writing response
	ShutdownTimeout time.Duration // max duration for graceful shutdown drain

	// Internal gRPC (gateway and replica traffic)
	GRPCAddr string // address to bind the internal gRPC server

	// Replication
	NodeID           string        // unique node identifier (e.g. "node1")
	Role             string        // "primary" or "replica"
	PrimaryAddr      string        // gRPC host:port of the primary (replicas pull WAL from here)
	ReplPollInterval time.Duration // how often replicas poll for WAL records
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		DataDir:          "./data",
		DBFile:           "neo4g.db",
		WALEnabled:       true,
		WALFile:          "neo4g.wal",
		PoolSize:         1024,
		ListenAddr:       ":7474",
		GRPCAddr:         ":9474",
		ReadTimeout:      30 * time.Second,
		WriteTimeout:     30 * time.Second,
		ShutdownTimeout:  10 * time.Second,
		NodeID:           "node1",
		Role:             "primary",
		ReplPollInterval: 500 * time.Millisecond,
	}
}

// ConfigFromEnv builds a Config by reading environment variables,
// falling back to DefaultConfig for any unset value.
//
//	NEO4G_DATA_DIR         → DataDir
//	NEO4G_DB_FILE          → DBFile
//	NEO4G_WAL_ENABLED      → WALEnabled ("true"/"false"/"1"/"0")
//	NEO4G_WAL_FILE         → WALFile
//	NEO4G_POOL_SIZE        → PoolSize
//	NEO4G_LISTEN_ADDR      → ListenAddr
//	NEO4G_GRPC_ADDR        → GRPCAddr
//	NEO4G_READ_TIMEOUT     → ReadTimeout (Go duration string, e.g. "30s")
//	NEO4G_WRITE_TIMEOUT    → WriteTimeout
//	NEO4G_SHUTDOWN_TIMEOUT → ShutdownTimeout
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("NEO4G_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("NEO4G_DB_FILE"); v != "" {
		cfg.DBFile = v
	}
	if v := os.Getenv("NEO4G_WAL_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "false", "0", "no", "off":
			cfg.WALEnabled = false
		}
	}
	if v := os.Getenv("NEO4G_WAL_FILE"); v != "" {
		cfg.WALFile = v
	}
	if v := os.Getenv("NEO4G_POOL_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.PoolSize = n
		}
	}
	if v := os.Getenv("NEO4G_WAL_NOSYNC"); v != "" {
		switch strings.ToLower(v) {
		case "true", "1", "yes", "on":
			cfg.WALNoSync = true
		}
	}
	if v := os.Getenv("NEO4G_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("NEO4G_GRPC_ADDR"); v != "" {
		cfg.GRPCAddr = v
	}
	if v := os.Getenv("NEO4G_READ_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ReadTimeout = d
		}
	}
	if v := os.Getenv("NEO4G_WRITE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.WriteTimeout = d
		}
	}
	if v := os.Getenv("NEO4G_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ShutdownTimeout = d
		}
	}

	if v := os.Getenv("NEO4G_NODE_ID"); v != "" {
		cfg.NodeID = v
	}
	if v := os.Getenv("NEO4G_ROLE"); v != "" {
		cfg.Role = strings.ToLower(v)
	}
	if v := os.Getenv("NEO4G_PRIMARY_ADDR"); v != "" {
		cfg.PrimaryAddr = v
	}
	if v := os.Getenv("NEO4G_REPL_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ReplPollInterval = d
		}
	}

	return cfg
}
