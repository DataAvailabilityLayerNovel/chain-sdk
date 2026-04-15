package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Profile represents a deployment environment.
type Profile string

const (
	ProfileDev  Profile = "dev"
	ProfileTest Profile = "test"
	ProfileProd Profile = "prod"
)

// Config holds all configurable values for cosmos-exec-grpc.
type Config struct {
	// Server
	ListenAddr string `json:"listen_addr"`
	Home       string `json:"home"`
	InMemory   bool   `json:"in_memory"`

	// Execution
	BlockTime   time.Duration `json:"block_time"`
	QueryGasMax uint64        `json:"query_gas_max"`

	// Blob store limits
	MaxBlobSize       int `json:"max_blob_size"`
	MaxStoreTotalSize int `json:"max_store_total_size"`

	// Persistence
	PersistBlobs     bool   `json:"persist_blobs"`
	PersistTxResults bool   `json:"persist_tx_results"`
	DataDir          string `json:"data_dir"`

	// Logging
	LogLevel string `json:"log_level"`

	// Security
	AuthToken           string `json:"auth_token"`
	CORSAllowOrigin     string `json:"cors_allow_origin"`
	MaxRequestBodyBytes int64  `json:"max_request_body_bytes"`
	RateLimitRPS        int    `json:"rate_limit_rps"`
	ReadOnlyMode        bool   `json:"read_only_mode"`

	// Metrics
	MetricsEnabled bool   `json:"metrics_enabled"`
	MetricsAddr    string `json:"metrics_addr"`

	// Timeouts
	ReadTimeout  time.Duration `json:"read_timeout"`
	WriteTimeout time.Duration `json:"write_timeout"`
	IdleTimeout  time.Duration `json:"idle_timeout"`

	// Profile
	Profile Profile `json:"profile"`
}

// DefaultConfig returns config with dev-profile defaults.
func DefaultConfig() Config {
	return Config{
		ListenAddr:        "0.0.0.0:50051",
		Home:              ".cosmos-exec-grpc",
		InMemory:          false,
		BlockTime:         2 * time.Second,
		QueryGasMax:       50_000_000,
		MaxBlobSize:       4 * 1024 * 1024,       // 4 MB
		MaxStoreTotalSize: 256 * 1024 * 1024,      // 256 MB
		PersistBlobs:        false,
		PersistTxResults:    false,
		DataDir:             "",
		LogLevel:            "info",
		CORSAllowOrigin:     "*",
		MaxRequestBodyBytes: 10 * 1024 * 1024, // 10 MB
		RateLimitRPS:        0,                 // no limit in dev
		ReadOnlyMode:        false,
		MetricsEnabled:      false,
		MetricsAddr:         "127.0.0.1:9090",
		ReadTimeout:         30 * time.Second,
		WriteTimeout:        30 * time.Second,
		IdleTimeout:         120 * time.Second,
		Profile:             ProfileDev,
	}
}

// ForProfile returns config tuned for the given profile.
func ForProfile(p Profile) Config {
	cfg := DefaultConfig()
	cfg.Profile = p

	switch p {
	case ProfileTest:
		cfg.InMemory = true
		cfg.ListenAddr = "127.0.0.1:0"
		cfg.Home = ""
		cfg.LogLevel = "error"
		cfg.MaxStoreTotalSize = 16 * 1024 * 1024 // 16 MB
	case ProfileProd:
		cfg.PersistBlobs = true
		cfg.PersistTxResults = true
		cfg.LogLevel = "info"
		cfg.MaxStoreTotalSize = 1024 * 1024 * 1024 // 1 GB
		cfg.CORSAllowOrigin = ""                    // must be set explicitly
		cfg.RateLimitRPS = 100
		cfg.MetricsEnabled = true
		cfg.ReadTimeout = 15 * time.Second
		cfg.WriteTimeout = 15 * time.Second
		cfg.IdleTimeout = 60 * time.Second
	}

	return cfg
}

// LoadFromEnv overlays environment variables on top of existing config.
// Environment variables use the prefix COSMOS_EXEC_.
func (c *Config) LoadFromEnv() {
	if v := os.Getenv("COSMOS_EXEC_LISTEN_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("COSMOS_EXEC_HOME"); v != "" {
		c.Home = v
	}
	if v := os.Getenv("COSMOS_EXEC_IN_MEMORY"); v != "" {
		c.InMemory = parseBool(v)
	}
	if v := os.Getenv("COSMOS_EXEC_BLOCK_TIME"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.BlockTime = d
		}
	}
	if v := os.Getenv("COSMOS_EXEC_QUERY_GAS_MAX"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			c.QueryGasMax = n
		}
	}
	if v := os.Getenv("COSMOS_EXEC_MAX_BLOB_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.MaxBlobSize = n
		}
	}
	if v := os.Getenv("COSMOS_EXEC_MAX_STORE_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.MaxStoreTotalSize = n
		}
	}
	if v := os.Getenv("COSMOS_EXEC_PERSIST_BLOBS"); v != "" {
		c.PersistBlobs = parseBool(v)
	}
	if v := os.Getenv("COSMOS_EXEC_PERSIST_TX_RESULTS"); v != "" {
		c.PersistTxResults = parseBool(v)
	}
	if v := os.Getenv("COSMOS_EXEC_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("COSMOS_EXEC_LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
	if v := os.Getenv("COSMOS_EXEC_AUTH_TOKEN"); v != "" {
		c.AuthToken = v
	}
	if v := os.Getenv("COSMOS_EXEC_CORS_ORIGIN"); v != "" {
		c.CORSAllowOrigin = v
	}
	if v := os.Getenv("COSMOS_EXEC_MAX_BODY_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.MaxRequestBodyBytes = n
		}
	}
	if v := os.Getenv("COSMOS_EXEC_RATE_LIMIT_RPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.RateLimitRPS = n
		}
	}
	if v := os.Getenv("COSMOS_EXEC_READ_ONLY"); v != "" {
		c.ReadOnlyMode = parseBool(v)
	}
	if v := os.Getenv("COSMOS_EXEC_METRICS"); v != "" {
		c.MetricsEnabled = parseBool(v)
	}
	if v := os.Getenv("COSMOS_EXEC_METRICS_ADDR"); v != "" {
		c.MetricsAddr = v
	}
	if v := os.Getenv("COSMOS_EXEC_PROFILE"); v != "" {
		switch Profile(strings.ToLower(v)) {
		case ProfileDev, ProfileTest, ProfileProd:
			c.Profile = Profile(strings.ToLower(v))
		}
	}
}

// Validate checks required invariants.
func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if c.QueryGasMax == 0 {
		return fmt.Errorf("query_gas_max must be > 0")
	}
	if c.MaxBlobSize <= 0 {
		return fmt.Errorf("max_blob_size must be > 0")
	}
	if c.MaxStoreTotalSize <= 0 {
		return fmt.Errorf("max_store_total_size must be > 0")
	}
	return nil
}

// ResolveDataDir returns the effective data directory, defaulting to Home/data.
func (c *Config) ResolveDataDir() string {
	if c.DataDir != "" {
		return c.DataDir
	}
	if c.Home != "" {
		return c.Home + "/data"
	}
	return ""
}

func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "true" || s == "1" || s == "yes"
}
