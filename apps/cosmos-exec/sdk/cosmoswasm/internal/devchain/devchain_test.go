package devchain

import (
	"strings"
	"testing"
	"time"
)

func TestBuildRunnerArgs(t *testing.T) {
	cfg := DefaultConfig("/tmp/ev-node")
	cfg.ChainName = "my-chain"
	cfg.CleanOnStart = false
	cfg.CleanOnExit = true
	cfg.LogLevel = "debug"
	cfg.BlockTime = 3 * time.Second
	cfg.SubmitInterval = 12 * time.Second

	args := BuildRunnerArgs(cfg)
	joined := strings.Join(args, " ")

	expectedParts := []string{
		"run",
		"./scripts/run-cosmos-wasm-nodes.go",
		"--chain-id my-chain",
		"--clean-on-start=false",
		"--clean-on-exit=true",
		"--log-level debug",
		"--block-time 3s",
		"--submit-interval 12s",
	}

	for _, part := range expectedParts {
		if !strings.Contains(joined, part) {
			t.Fatalf("missing part %q in args: %s", part, joined)
		}
	}
}
