package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
	projectRoot, err := resolveProjectRoot()
	if err != nil {
		log.Fatal(err)
	}

	daBridgeRPC := strings.TrimSpace(os.Getenv("DA_BRIDGE_RPC"))
	if daBridgeRPC == "" {
		log.Fatal("DA_BRIDGE_RPC is required")
	}

	chainName := firstNonEmpty(os.Getenv("CHAIN_NAME"), "my-dapp-chain")
	namespace := firstNonEmpty(os.Getenv("DA_NAMESPACE"), "my-dapp-namespace")
	authToken := strings.TrimSpace(os.Getenv("DA_AUTH_TOKEN"))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := cosmoswasm.DefaultDALChainConfig(projectRoot)
	cfg.ChainName = chainName
	cfg.Namespace = namespace
	cfg.DABridgeRPC = daBridgeRPC
	cfg.DAAuthToken = authToken
	cfg.CleanOnStart = true
	cfg.CleanOnExit = false
	cfg.LogLevel = "info"
	cfg.BlockTime = 2 * time.Second
	cfg.SubmitInterval = 8 * time.Second

	log.Printf("Starting dApp chain on DAL")
	log.Printf("project_root=%s", projectRoot)
	log.Printf("chain_name=%s namespace=%s", chainName, namespace)

	proc, err := cosmoswasm.StartDALChain(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer proc.Stop()

	log.Printf("Chain is ready")
	log.Printf("sequencer_rpc=%s", proc.Endpoints.SequencerRPC)
	log.Printf("fullnode_rpc=%s", proc.Endpoints.FullNodeRPC)
	log.Printf("sequencer_exec_api=%s", proc.Endpoints.SequencerExecAPI)
	log.Printf("fullnode_exec_api=%s", proc.Endpoints.FullNodeExecAPI)
	log.Printf("Press Ctrl+C to stop")

	<-ctx.Done()
	log.Printf("Stopping chain...")
}

func resolveProjectRoot() (string, error) {
	if value := strings.TrimSpace(os.Getenv("EVNODE_PROJECT_ROOT")); value != "" {
		return value, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current working directory: %w", err)
	}

	current := wd
	for {
		if _, err := os.Stat(filepath.Join(current, "scripts", "run-cosmos-wasm-nodes.go")); err == nil {
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return "", errors.New("cannot detect ev-node project root (set EVNODE_PROJECT_ROOT)")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
