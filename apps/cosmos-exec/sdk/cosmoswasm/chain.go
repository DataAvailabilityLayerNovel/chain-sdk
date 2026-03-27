package cosmoswasm

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
	"time"
)

const (
	defaultSequencerRPCURL  = "http://127.0.0.1:38331"
	defaultFullNodeRPCURL   = "http://127.0.0.1:48331"
	defaultSequencerExecURL = "http://127.0.0.1:50051"
	defaultFullNodeExecURL  = "http://127.0.0.1:50052"
)

type DALChainConfig struct {
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
}

type DALChainEndpoints struct {
	SequencerRPC     string
	FullNodeRPC      string
	SequencerExecAPI string
	FullNodeExecAPI  string
}

type DALChainProcess struct {
	Cmd       *exec.Cmd
	Config    DALChainConfig
	Endpoints DALChainEndpoints
}

func DefaultDALChainConfig(projectRoot string) DALChainConfig {
	return DALChainConfig{
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

func (c DALChainConfig) Validate() error {
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

func (p *DALChainProcess) Stop() error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}
	return p.Cmd.Process.Kill()
}

func StartDALChain(ctx context.Context, cfg DALChainConfig) (*DALChainProcess, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "go", buildRunnerArgs(cfg)...)
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

	endpoints := DALChainEndpoints{
		SequencerRPC:     defaultSequencerRPCURL,
		FullNodeRPC:      defaultFullNodeRPCURL,
		SequencerExecAPI: defaultSequencerExecURL,
		FullNodeExecAPI:  defaultFullNodeExecURL,
	}

	if err := waitForLive(ctx, endpoints.SequencerRPC+"/health/live", 120*time.Second); err != nil {
		return nil, fmt.Errorf("sequencer not ready: %w", err)
	}
	if err := waitForLive(ctx, endpoints.FullNodeRPC+"/health/live", 120*time.Second); err != nil {
		return nil, fmt.Errorf("full node not ready: %w", err)
	}

	return &DALChainProcess{Cmd: cmd, Config: cfg, Endpoints: endpoints}, nil
}

func buildRunnerArgs(cfg DALChainConfig) []string {
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
