package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"
)

const defaultWasmURL = "https://github.com/CosmWasm/cw-plus/releases/download/v1.1.0/cw20_base.wasm"

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

	proc, err := cosmoswasm.StartDALChain(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer proc.Stop()

	log.Printf("Chain ready: sequencer=%s exec=%s", proc.Endpoints.SequencerRPC, proc.Endpoints.SequencerExecAPI)

	wasmBytes, err := loadWASM(ctx)
	if err != nil {
		log.Fatal(err)
	}

	client := cosmoswasm.NewClient(proc.Endpoints.SequencerExecAPI)
	sender := cosmoswasm.DefaultSender()

	storeTx, err := cosmoswasm.BuildStoreTx(wasmBytes, sender)
	if err != nil {
		log.Fatal(err)
	}

	storeSubmit, err := client.SubmitTxBytes(ctx, storeTx)
	if err != nil {
		log.Fatal(err)
	}

	storeResult, err := client.WaitTxResult(ctx, storeSubmit.Hash, time.Second)
	if err != nil {
		log.Fatal(err)
	}
	if storeResult.Code != 0 {
		log.Fatalf("store tx failed: code=%d log=%s", storeResult.Code, storeResult.Log)
	}

	codeIDText := findEventValue(storeResult.Events, "code_id")
	if codeIDText == "" {
		log.Fatalf("cannot parse code_id from tx events")
	}

	codeID, err := strconv.ParseUint(codeIDText, 10, 64)
	if err != nil {
		log.Fatalf("invalid code_id %q: %v", codeIDText, err)
	}

	initMsg := defaultInitMsg(sender)
	if customMsg := strings.TrimSpace(os.Getenv("INIT_MSG")); customMsg != "" {
		initMsg = customMsg
	}

	label := firstNonEmpty(os.Getenv("LABEL"), "sdk-dapp-"+strconv.FormatInt(time.Now().Unix(), 10))
	initTx, err := cosmoswasm.BuildInstantiateTx(cosmoswasm.InstantiateTxRequest{
		Sender: sender,
		CodeID: codeID,
		Label:  label,
		Msg:    initMsg,
	})
	if err != nil {
		log.Fatal(err)
	}

	initSubmit, err := client.SubmitTxBytes(ctx, initTx)
	if err != nil {
		log.Fatal(err)
	}

	initResult, err := client.WaitTxResult(ctx, initSubmit.Hash, time.Second)
	if err != nil {
		log.Fatal(err)
	}
	if initResult.Code != 0 {
		log.Fatalf("instantiate tx failed: code=%d log=%s", initResult.Code, initResult.Log)
	}

	contractAddr := firstNonEmpty(
		findEventValue(initResult.Events, "_contract_address"),
		findEventValue(initResult.Events, "contract_address"),
	)
	if contractAddr == "" {
		log.Fatalf("cannot parse contract address from instantiate events")
	}

	log.Printf("Deploy success")
	log.Printf("store_tx_hash=%s", storeSubmit.Hash)
	log.Printf("instantiate_tx_hash=%s", initSubmit.Hash)
	log.Printf("code_id=%d", codeID)
	log.Printf("contract_addr=%s", contractAddr)
	log.Printf("Press Ctrl+C to stop chain")

	<-ctx.Done()
}

func loadWASM(ctx context.Context) ([]byte, error) {
	if wasmFile := strings.TrimSpace(os.Getenv("WASM_FILE")); wasmFile != "" {
		return os.ReadFile(wasmFile)
	}

	wasmURL := firstNonEmpty(os.Getenv("WASM_URL"), defaultWasmURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wasmURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build wasm download request: %w", err)
	}

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("download wasm: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("download wasm failed: status=%d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read wasm body: %w", err)
	}

	if len(body) == 0 {
		return nil, errors.New("downloaded wasm is empty")
	}

	return body, nil
}

func defaultInitMsg(sender string) string {
	payload := map[string]any{
		"name":     "Token",
		"symbol":   "TOK",
		"decimals": 6,
		"initial_balances": []map[string]string{
			{"address": sender, "amount": "1000000"},
		},
		"mint": map[string]string{
			"minter": sender,
			"cap":    "1000000000",
		},
		"marketing": nil,
	}

	bz, _ := json.Marshal(payload)
	return string(bz)
}

func findEventValue(events []cosmoswasm.TxEvent, key string) string {
	for _, event := range events {
		for _, attribute := range event.Attributes {
			if attribute.Key == key && strings.TrimSpace(attribute.Value) != "" {
				return strings.TrimSpace(attribute.Value)
			}
		}
	}
	return ""
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
