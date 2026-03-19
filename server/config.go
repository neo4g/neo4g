package server

import (
	"os"
	"strconv"
	"strings"
)

// Config holds all tunables for the database engine.
type Config struct {
	DataDir    string // directory for data + WAL files
	DBFile     string // data file name (relative to DataDir)
	WALEnabled bool   // enable write-ahead log for crash recovery
	WALFile    string // WAL file name (relative to DataDir)
	PoolSize   int    // buffer pool frame count
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		DataDir:    "./data",
		DBFile:     "neo4g.db",
		WALEnabled: true,
		WALFile:    "neo4g.wal",
		PoolSize:   1024,
	}
}

// ConfigFromEnv builds a Config by reading environment variables,
// falling back to DefaultConfig for any unset value.
//
//	NEO4G_DATA_DIR    → DataDir
//	NEO4G_DB_FILE     → DBFile
//	NEO4G_WAL_ENABLED → WALEnabled ("true"/"false"/"1"/"0")
//	NEO4G_WAL_FILE    → WALFile
//	NEO4G_POOL_SIZE   → PoolSize
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

	return cfg
}
