package gateway

import (
	"os"
	"strings"
	"time"
)

// Config holds all tunables for the gateway.
type Config struct {
	ListenAddr        string
	Nodes             []NodeConfig
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	ElectionDelay     time.Duration
}

// NodeConfig identifies a single Neo4g node.
type NodeConfig struct {
	ID   string
	Addr string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		ListenAddr:        ":7480",
		HeartbeatInterval: 2 * time.Second,
		HeartbeatTimeout:  6 * time.Second,
		ElectionDelay:     1 * time.Second,
	}
}

// ConfigFromEnv builds a Config by reading environment variables.
//
//	NEO4G_GW_LISTEN_ADDR      → ListenAddr
//	NEO4G_GW_NODES            → Nodes (comma-separated id=addr pairs)
//	NEO4G_GW_HEARTBEAT_INTERVAL → HeartbeatInterval
//	NEO4G_GW_HEARTBEAT_TIMEOUT  → HeartbeatTimeout
//	NEO4G_GW_ELECTION_DELAY     → ElectionDelay
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("NEO4G_GW_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}

	if v := os.Getenv("NEO4G_GW_NODES"); v != "" {
		cfg.Nodes = parseNodes(v)
	}

	if v := os.Getenv("NEO4G_GW_HEARTBEAT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.HeartbeatInterval = d
		}
	}
	if v := os.Getenv("NEO4G_GW_HEARTBEAT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.HeartbeatTimeout = d
		}
	}
	if v := os.Getenv("NEO4G_GW_ELECTION_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ElectionDelay = d
		}
	}

	return cfg
}

// consecutiveFailures is how many missed heartbeats mark a node dead.
// HeartbeatTimeout is the detection window; a single failure is not enough.
func (c Config) consecutiveFailures() int {
	interval := c.HeartbeatInterval
	if interval <= 0 {
		interval = time.Second
	}
	timeout := c.HeartbeatTimeout
	if timeout <= 0 {
		timeout = 3 * interval
	}
	n := int((timeout + interval - 1) / interval)
	if n < 2 {
		n = 2
	}
	return n
}

// parseNodes parses "node1=host:port,node2=host:port" into NodeConfig slice.
// Addresses are internal gRPC endpoints (host:port).
func parseNodes(s string) []NodeConfig {
	var nodes []NodeConfig
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		nodes = append(nodes, NodeConfig{
			ID:   strings.TrimSpace(parts[0]),
			Addr: strings.TrimSpace(parts[1]),
		})
	}
	return nodes
}
