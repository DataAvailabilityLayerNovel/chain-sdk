// Package devchain provides a local chain runner for development and testing.
// This is NOT part of the stable SDK API — it wraps the run-cosmos-wasm-nodes.go
// script and is intended for integration tests and local dev only.
package devchain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Default endpoint URLs used by Config when no overrides are provided.
// These match the ports used by the local dev runner (run-cosmos-wasm-nodes.go).
const (
	DefaultSequencerRPCURL  = "http://127.0.0.1:38331"
	DefaultFullNodeRPCURL   = "http://127.0.0.1:48331"
	DefaultSequencerExecURL = "http://127.0.0.1:50051"
	DefaultFullNodeExecURL  = "http://127.0.0.1:50052"
)

// Config holds configuration for launching a local DAL chain.
type Config struct {
	ProjectRoot    string
	ChainName      string
	Namespace      string
	DABridgeRPC    string
	DAAuthToken    string
	CleanOnStart   bool
	CleanOnExit    bool
	LogLevel       string
	BlockTime      time.Duration
	SubmitInterval time.Duration
	Stdout         io.Writer
	Stderr         io.Writer

	// Endpoint overrides — when empty, defaults are used.
	SequencerRPC     string
	FullNodeRPC      string
	SequencerExecURL string
	FullNodeExecURL  string
}

// Endpoints contains the resolved endpoint URLs for the running chain.
type Endpoints struct {
	SequencerRPC     string
	FullNodeRPC      string
	SequencerExecAPI string
	FullNodeExecAPI  string
}

// Process represents a running local chain process.
type Process struct {
	Cmd       *exec.Cmd
	Config    Config
	Endpoints Endpoints
}

// DefaultConfig returns a Config with sensible defaults for local development.
func DefaultConfig(projectRoot string) Config {
	return Config{
		ProjectRoot:    projectRoot,
		ChainName:      "cosmos-wasm-local",
		Namespace:      "rollup",
		CleanOnStart:   true,
		CleanOnExit:    false,
		LogLevel:       "info",
		BlockTime:      2 * time.Second,
		SubmitInterval: 8 * time.Second,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
	}
}

// Validate checks that the config has all required fields.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ProjectRoot) == "" {
		return errors.New("project root is required")
	}
	if strings.TrimSpace(c.ChainName) == "" {
		return errors.New("chain name is required")
	}
	if strings.TrimSpace(c.Namespace) == "" {
		return errors.New("namespace is required")
	}
	if strings.TrimSpace(c.DABridgeRPC) == "" {
		return errors.New("DA bridge RPC is required")
	}
	if c.BlockTime <= 0 {
		return errors.New("block time must be greater than 0")
	}
	if c.SubmitInterval <= 0 {
		return errors.New("submit interval must be greater than 0")
	}
	if _, err := os.Stat(filepath.Join(c.ProjectRoot, "scripts", "run-cosmos-wasm-nodes.go")); err != nil {
		return fmt.Errorf("scripts/run-cosmos-wasm-nodes.go not found in project root: %w", err)
	}

	return nil
}

// Stop gracefully shuts the chain down.
//
// VI: gửi SIGINT để runner tự cancel context và tear down mọi child process
// (sequencer/full node/exec), chờ tối đa 10s rồi mới ép Kill nếu runner bướng.
// KHÔNG dùng Kill() thẳng: SIGKILL không bắt được → runner chết tức thì, child
// thành mồ côi và chain vẫn chạy.
func (p *Process) Stop() error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}

	// PID của runner = PGID của group (vì Setpgid → child là group leader). Gửi
	// signal với PID ÂM = signal cả group: chạm trực tiếp runner (bỏ qua lớp
	// `go run` không forward signal) lẫn sequencer/full node/exec.
	pgid := p.Cmd.Process.Pid

	if err := syscall.Kill(-pgid, syscall.SIGINT); err != nil {
		// Group đã chết / không gửi được → ép kill cho chắc rồi thôi.
		_ = p.Cmd.Process.Kill()
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- p.Cmd.Wait() }()

	select {
	case <-done:
		// Runner đã thoát; exit non-zero do signal là bình thường → coi như OK.
		return nil
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-pgid, syscall.SIGKILL) // bướng → ép kill cả group.
		return nil
	}
}

// Start launches a local DAL chain and waits for it to become healthy.
func Start(ctx context.Context, cfg Config) (*Process, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "go", BuildRunnerArgs(cfg)...)
	// Đặt runner + toàn bộ con cháu vào MỘT process group riêng. Lý do: runner
	// chạy qua `go run`, mà `go run` KHÔNG forward signal xuống binary thật nó
	// compile ra — signal thẳng vào cmd.Process (chính là tiến trình `go run`)
	// sẽ không tới runner → runner cùng sequencer/full node bị mồ côi. Có group
	// riêng, ta signal cả group (PID âm) để chạm trực tiếp runner + mọi child.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Khi ctx bị hủy (vd Ctrl+C lúc đang boot): gửi SIGINT cho CẢ GROUP thay vì
	// SIGKILL mặc định. Runner bắt SIGINT → cancel context của nó → tear down
	// child. WaitDelay cho 10s dọn dẹp trước khi Go ép kill.
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }
	cmd.WaitDelay = 10 * time.Second
	cmd.Dir = cfg.ProjectRoot
	cmd.Env = append(os.Environ(),
		"DA_BRIDGE_RPC="+cfg.DABridgeRPC,
		"DA_RPC="+cfg.DABridgeRPC,
		"DA_NAMESPACE="+cfg.Namespace,
		"DA_AUTH_TOKEN="+cfg.DAAuthToken,
	)
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start DAL chain runner: %w", err)
	}

	endpoints := Endpoints{
		SequencerRPC:     orDefault(cfg.SequencerRPC, DefaultSequencerRPCURL),
		FullNodeRPC:      orDefault(cfg.FullNodeRPC, DefaultFullNodeRPCURL),
		SequencerExecAPI: orDefault(cfg.SequencerExecURL, DefaultSequencerExecURL),
		FullNodeExecAPI:  orDefault(cfg.FullNodeExecURL, DefaultFullNodeExecURL),
	}

	if err := waitForLive(ctx, endpoints.SequencerRPC+"/health/live", 120*time.Second); err != nil {
		return nil, fmt.Errorf("sequencer not ready: %w", err)
	}
	if err := waitForLive(ctx, endpoints.FullNodeRPC+"/health/live", 120*time.Second); err != nil {
		return nil, fmt.Errorf("full node not ready: %w", err)
	}

	return &Process{Cmd: cmd, Config: cfg, Endpoints: endpoints}, nil
}

// BuildRunnerArgs constructs the go run arguments for the chain runner script.
func BuildRunnerArgs(cfg Config) []string {
	return []string{
		"run",
		"-tags",
		"run_cosmos_wasm",
		"./scripts/run-cosmos-wasm-nodes.go",
		"--chain-id",
		cfg.ChainName,
		"--clean-on-start=" + strconv.FormatBool(cfg.CleanOnStart),
		"--clean-on-exit=" + strconv.FormatBool(cfg.CleanOnExit),
		"--log-level",
		cfg.LogLevel,
		"--block-time",
		cfg.BlockTime.String(),
		"--submit-interval",
		cfg.SubmitInterval.String(),
	}
}

func waitForLive(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			resp, doErr := client.Do(req)
			if doErr == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}

	return fmt.Errorf("timeout waiting for %s", url)
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
