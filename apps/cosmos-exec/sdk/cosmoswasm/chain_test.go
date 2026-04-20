package cosmoswasm

import (
	"testing"
	"time"
)

func TestDefaultDALChainConfig(t *testing.T) {
	cfg := DefaultDALChainConfig("/tmp/ev-node")
	if cfg.ChainName != "cosmos-wasm-local" {
		t.Fatalf("unexpected default chain name: %s", cfg.ChainName)
	}
	if cfg.Namespace != "rollup" {
		t.Fatalf("unexpected default namespace: %s", cfg.Namespace)
	}
}

func TestDALChainConfigDefaults(t *testing.T) {
	cfg := DefaultDALChainConfig("/tmp/ev-node")

	if cfg.BlockTime != 2*time.Second {
		t.Fatalf("unexpected block time: %s", cfg.BlockTime)
	}
	if cfg.SubmitInterval != 8*time.Second {
		t.Fatalf("unexpected submit interval: %s", cfg.SubmitInterval)
	}
	if cfg.CleanOnStart != true {
		t.Fatal("expected CleanOnStart to be true")
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("unexpected log level: %s", cfg.LogLevel)
	}
}
