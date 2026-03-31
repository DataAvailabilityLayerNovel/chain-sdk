package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "dal-sdk",
		Short: "DAL Cosmos WASM SDK CLI",
		Long:  "CLI for DAL dApp chain management and WASM contract operations",
	}

	// Info command
	infoCmd := &cobra.Command{
		Use:   "info",
		Short: "Show SDK information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("DAL Cosmos WASM SDK CLI")
			fmt.Println("Usage:")
			fmt.Println("  dal-sdk chain start --name <name> --namespace <ns> --da-rpc <url>")
			fmt.Println("  dal-sdk tx submit --tx-base64 <b64> --rpc <url> --wait")
			fmt.Println("  dal-sdk contract deploy --wasm <file> --init-msg '{...}' --rpc <url>")
			fmt.Println("  dal-sdk contract balance --contract <addr> --address <addr> --rpc <url>")
		},
	}
	rootCmd.AddCommand(infoCmd)

	// TX commands
	txCmd := &cobra.Command{
		Use:   "tx",
		Short: "Submit and inspect transactions",
	}
	rootCmd.AddCommand(txCmd)

	txSubmitCmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit signed tx (base64/hex/raw file)",
		RunE:  runTxSubmit,
	}
	txSubmitCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	txSubmitCmd.Flags().String("tx-base64", "", "Transaction bytes in base64")
	txSubmitCmd.Flags().String("tx-hex", "", "Transaction bytes in hex")
	txSubmitCmd.Flags().String("tx-file", "", "Path to raw tx bytes file")
	txSubmitCmd.Flags().Bool("wait", true, "Wait until tx result is available")
	txSubmitCmd.Flags().Uint64("poll-ms", 1000, "Polling interval (ms) when --wait=true")
	txCmd.AddCommand(txSubmitCmd)

	txResultCmd := &cobra.Command{
		Use:   "result --hash <txhash>",
		Short: "Get tx result by hash",
		RunE:  runTxResult,
	}
	txResultCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	txResultCmd.Flags().String("hash", "", "Transaction hash")
	_ = txResultCmd.MarkFlagRequired("hash")
	txCmd.AddCommand(txResultCmd)

	// Chain commands
	chainCmd := &cobra.Command{
		Use:   "chain",
		Short: "Manage chains",
	}
	rootCmd.AddCommand(chainCmd)

	// Chain start
	chainStartCmd := &cobra.Command{
		Use:   "start",
		Short: "Start a DAL Cosmos WASM chain",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChainStart(cmd)
		},
	}
	chainStartCmd.Flags().String("name", "mychain", "Chain name (chain-id)")
	chainStartCmd.Flags().String("namespace", "mynamespace", "DA namespace")
	chainStartCmd.Flags().String("da-rpc", "", "DA bridge RPC endpoint")
	chainStartCmd.Flags().String("project-root", "", "Path to ev-node repository root")
	chainStartCmd.Flags().String("log-level", "info", "Log level (debug|info|warn|error)")
	chainStartCmd.Flags().Uint64("block-time", 1000, "Block time in milliseconds")
	chainStartCmd.Flags().Uint64("submit-interval", 5000, "DA submit interval in milliseconds")
	chainStartCmd.Flags().Bool("clean", true, "Clean state on startup")
	chainStartCmd.Flags().Bool("clean-exit", true, "Clean state on exit")
	chainCmd.AddCommand(chainStartCmd)

	// Contract commands
	contractCmd := &cobra.Command{
		Use:   "contract",
		Short: "Manage contracts",
	}
	rootCmd.AddCommand(contractCmd)

	// Bank commands
	bankCmd := &cobra.Command{
		Use:   "bank",
		Short: "Native bank operations",
	}
	rootCmd.AddCommand(bankCmd)

	bankSendCmd := &cobra.Command{
		Use:   "send --to <addr> --amount <coins>",
		Short: "Send native tokens (MsgSend)",
		RunE:  runBankSend,
	}
	bankSendCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	bankSendCmd.Flags().String("sender", "", "Sender address (default SDK sender)")
	bankSendCmd.Flags().String("to", "", "Recipient address")
	bankSendCmd.Flags().String("amount", "", "Coins, e.g. 1000stake")
	bankSendCmd.Flags().Bool("wait", true, "Wait until tx result is available")
	bankSendCmd.Flags().Uint64("poll-ms", 1000, "Polling interval (ms) when --wait=true")
	_ = bankSendCmd.MarkFlagRequired("to")
	_ = bankSendCmd.MarkFlagRequired("amount")
	bankCmd.AddCommand(bankSendCmd)

	bankBalanceCmd := &cobra.Command{
		Use:   "balance --address <addr>",
		Short: "Check native bank balances via Cosmos REST",
		RunE:  runBankBalance,
	}
	bankBalanceCmd.Flags().String("rest", "http://127.0.0.1:38331", "Cosmos REST endpoint")
	bankBalanceCmd.Flags().String("address", "", "Wallet address")
	_ = bankBalanceCmd.MarkFlagRequired("address")
	bankCmd.AddCommand(bankBalanceCmd)

	contractStoreCmd := &cobra.Command{
		Use:   "store --wasm <file>",
		Short: "Store wasm bytecode",
		RunE:  runContractStore,
	}
	contractStoreCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	contractStoreCmd.Flags().String("wasm", "", "Path to wasm file")
	contractStoreCmd.Flags().String("sender", "", "Sender address (default SDK sender)")
	contractStoreCmd.Flags().Bool("wait", true, "Wait until tx result is available")
	contractStoreCmd.Flags().Uint64("poll-ms", 1000, "Polling interval (ms) when --wait=true")
	_ = contractStoreCmd.MarkFlagRequired("wasm")
	contractCmd.AddCommand(contractStoreCmd)

	contractInstantiateCmd := &cobra.Command{
		Use:   "instantiate --code-id <id> --init-msg '{...}'",
		Short: "Instantiate a wasm contract",
		RunE:  runContractInstantiate,
	}
	contractInstantiateCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	contractInstantiateCmd.Flags().Uint64("code-id", 0, "Stored wasm code ID")
	contractInstantiateCmd.Flags().String("init-msg", "{}", "Instantiate JSON msg")
	contractInstantiateCmd.Flags().String("sender", "", "Sender address (default SDK sender)")
	contractInstantiateCmd.Flags().String("label", "", "Contract label")
	contractInstantiateCmd.Flags().String("admin", "", "Admin address (optional)")
	contractInstantiateCmd.Flags().Bool("wait", true, "Wait until tx result is available")
	contractInstantiateCmd.Flags().Uint64("poll-ms", 1000, "Polling interval (ms) when --wait=true")
	_ = contractInstantiateCmd.MarkFlagRequired("code-id")
	contractCmd.AddCommand(contractInstantiateCmd)

	contractExecuteCmd := &cobra.Command{
		Use:   "execute --contract <addr> --msg '{...}'",
		Short: "Execute a wasm contract",
		RunE:  runContractExecute,
	}
	contractExecuteCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	contractExecuteCmd.Flags().String("contract", "", "Contract address")
	contractExecuteCmd.Flags().String("msg", "{}", "Execute JSON msg")
	contractExecuteCmd.Flags().String("sender", "", "Sender address (default SDK sender)")
	contractExecuteCmd.Flags().Bool("wait", true, "Wait until tx result is available")
	contractExecuteCmd.Flags().Uint64("poll-ms", 1000, "Polling interval (ms) when --wait=true")
	_ = contractExecuteCmd.MarkFlagRequired("contract")
	contractCmd.AddCommand(contractExecuteCmd)

	contractTransferCmd := &cobra.Command{
		Use:   "transfer --contract <addr> --to <addr> --amount <amount>",
		Short: "CW20 transfer convenience command",
		RunE:  runContractTransfer,
	}
	contractTransferCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	contractTransferCmd.Flags().String("contract", "", "CW20 contract address")
	contractTransferCmd.Flags().String("to", "", "Recipient address")
	contractTransferCmd.Flags().String("amount", "", "Transfer amount")
	contractTransferCmd.Flags().String("sender", "", "Sender address (default SDK sender)")
	contractTransferCmd.Flags().Bool("wait", true, "Wait until tx result is available")
	contractTransferCmd.Flags().Uint64("poll-ms", 1000, "Polling interval (ms) when --wait=true")
	_ = contractTransferCmd.MarkFlagRequired("contract")
	_ = contractTransferCmd.MarkFlagRequired("to")
	_ = contractTransferCmd.MarkFlagRequired("amount")
	contractCmd.AddCommand(contractTransferCmd)

	contractQueryCmd := &cobra.Command{
		Use:   "query --contract <addr> --msg '{...}'",
		Short: "Query wasm smart contract",
		RunE:  runContractQuery,
	}
	contractQueryCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	contractQueryCmd.Flags().String("contract", "", "Contract address")
	contractQueryCmd.Flags().String("msg", "{}", "Query JSON msg")
	_ = contractQueryCmd.MarkFlagRequired("contract")
	contractCmd.AddCommand(contractQueryCmd)

	contractBalanceCmd := &cobra.Command{
		Use:   "balance --contract <addr> --address <addr>",
		Short: "Check CW20 balance via smart query",
		RunE:  runContractBalance,
	}
	contractBalanceCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	contractBalanceCmd.Flags().String("contract", "", "CW20 contract address")
	contractBalanceCmd.Flags().String("address", "", "Wallet address")
	_ = contractBalanceCmd.MarkFlagRequired("contract")
	_ = contractBalanceCmd.MarkFlagRequired("address")
	contractCmd.AddCommand(contractBalanceCmd)

	contractDeployCmd := &cobra.Command{
		Use:   "deploy --wasm <file> [--init-msg '{...}']",
		Short: "Store + instantiate in one command",
		RunE:  runContractDeploy,
	}
	contractDeployCmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	contractDeployCmd.Flags().String("wasm", "", "Path to wasm file")
	contractDeployCmd.Flags().String("sender", "", "Sender address (default SDK sender)")
	contractDeployCmd.Flags().String("init-msg", "{}", "Instantiate JSON msg")
	contractDeployCmd.Flags().String("label", "", "Contract label")
	contractDeployCmd.Flags().String("admin", "", "Admin address (optional)")
	contractDeployCmd.Flags().Uint64("poll-ms", 1000, "Polling interval (ms)")
	_ = contractDeployCmd.MarkFlagRequired("wasm")
	contractCmd.AddCommand(contractDeployCmd)

	contractDeployCW20Cmd := &cobra.Command{
		Use:   "deploy-cw20 --wasm <file>",
		Short: "Deploy CW20 token contract with default init message",
		RunE:  runContractDeployCW20,
	}
	contractDeployCW20Cmd.Flags().String("rpc", "http://127.0.0.1:50051", "cosmos-exec-grpc API URL")
	contractDeployCW20Cmd.Flags().String("wasm", "", "Path to cw20 wasm file")
	contractDeployCW20Cmd.Flags().String("sender", "", "Sender address (default SDK sender)")
	contractDeployCW20Cmd.Flags().String("name", "Token", "Token name")
	contractDeployCW20Cmd.Flags().String("symbol", "TOK", "Token symbol")
	contractDeployCW20Cmd.Flags().Uint64("decimals", 6, "Token decimals")
	contractDeployCW20Cmd.Flags().String("supply", "1000000", "Initial supply")
	contractDeployCW20Cmd.Flags().String("recipient", "", "Initial balance recipient (default sender)")
	contractDeployCW20Cmd.Flags().String("label", "", "Contract label")
	contractDeployCW20Cmd.Flags().String("admin", "", "Admin address (optional)")
	contractDeployCW20Cmd.Flags().Uint64("poll-ms", 1000, "Polling interval (ms)")
	_ = contractDeployCW20Cmd.MarkFlagRequired("wasm")
	contractCmd.AddCommand(contractDeployCW20Cmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runChainStart(cmd *cobra.Command) error {
	name, _ := cmd.Flags().GetString("name")
	namespace, _ := cmd.Flags().GetString("namespace")
	daRpc, _ := cmd.Flags().GetString("da-rpc")
	projectRoot, _ := cmd.Flags().GetString("project-root")
	logLevel, _ := cmd.Flags().GetString("log-level")
	blockTimeMs, _ := cmd.Flags().GetUint64("block-time")
	submitIntervalMs, _ := cmd.Flags().GetUint64("submit-interval")
	cleanOnStart, _ := cmd.Flags().GetBool("clean")
	cleanOnExit, _ := cmd.Flags().GetBool("clean-exit")

	// Use env var if flag not provided
	if daRpc == "" {
		daRpc = os.Getenv("DA_BRIDGE_RPC")
	}
	if namespace == "mynamespace" {
		ns := os.Getenv("DA_NAMESPACE")
		if ns != "" {
			namespace = ns
		}
	}
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = os.Getenv("EVNODE_PROJECT_ROOT")
	}
	if strings.TrimSpace(projectRoot) == "" {
		autoRoot, err := detectProjectRoot()
		if err == nil {
			projectRoot = autoRoot
		}
	}

	// Validate DA RPC
	if daRpc == "" {
		return fmt.Errorf("DA bridge RPC required: use --da-rpc flag or set DA_BRIDGE_RPC env var")
	}

	// Create config
	cfg := cosmoswasm.DefaultDALChainConfig(projectRoot)
	cfg.ChainName = name
	cfg.Namespace = namespace
	cfg.DABridgeRPC = daRpc
	cfg.DAAuthToken = os.Getenv("DA_AUTH_TOKEN")
	cfg.LogLevel = logLevel
	cfg.BlockTime = time.Duration(blockTimeMs) * time.Millisecond
	cfg.SubmitInterval = time.Duration(submitIntervalMs) * time.Millisecond
	cfg.CleanOnStart = cleanOnStart
	cfg.CleanOnExit = cleanOnExit

	// Validate config
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	fmt.Printf("Starting DAL Cosmos WASM chain...\n")
	fmt.Printf("  Chain ID: %s\n", cfg.ChainName)
	fmt.Printf("  Namespace: %s\n", cfg.Namespace)
	fmt.Printf("  DA RPC: %s\n", cfg.DABridgeRPC)
	fmt.Printf("  Project Root: %s\n", cfg.ProjectRoot)
	fmt.Printf("  Block Time: %v\n", cfg.BlockTime)
	fmt.Printf("  Submit Interval: %v\n\n", cfg.SubmitInterval)

	// Create context and signal handler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n\nShutting down chain...")
		cancel()
	}()

	// Start chain
	proc, err := cosmoswasm.StartDALChain(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to start chain: %w", err)
	}
	defer proc.Stop()

	fmt.Printf("✓ Chain started successfully!\n\n")
	fmt.Printf("Endpoints:\n")
	fmt.Printf("  Sequencer RPC:     %s\n", proc.Endpoints.SequencerRPC)
	fmt.Printf("  Fullnode RPC:      %s\n", proc.Endpoints.FullNodeRPC)
	fmt.Printf("  Sequencer Exec:    %s\n", proc.Endpoints.SequencerExecAPI)
	fmt.Printf("  Fullnode Exec:     %s\n\n", proc.Endpoints.FullNodeExecAPI)
	fmt.Printf("Press Ctrl+C to stop...\n")

	// Wait for context cancellation
	<-ctx.Done()
	fmt.Println("✓ Chain stopped")
	return nil
}

func detectProjectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	cur := wd
	for {
		runner := filepath.Join(cur, "scripts", "run-cosmos-wasm-nodes.go")
		if _, statErr := os.Stat(runner); statErr == nil {
			return cur, nil
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	return "", fmt.Errorf("cannot auto-detect ev-node project root from %s; use --project-root or EVNODE_PROJECT_ROOT", wd)
}

func runTxSubmit(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	txBase64, _ := cmd.Flags().GetString("tx-base64")
	txHex, _ := cmd.Flags().GetString("tx-hex")
	txFile, _ := cmd.Flags().GetString("tx-file")
	wait, _ := cmd.Flags().GetBool("wait")
	pollMs, _ := cmd.Flags().GetUint64("poll-ms")

	provided := 0
	if strings.TrimSpace(txBase64) != "" {
		provided++
	}
	if strings.TrimSpace(txHex) != "" {
		provided++
	}
	if strings.TrimSpace(txFile) != "" {
		provided++
	}
	if provided != 1 {
		return fmt.Errorf("provide exactly one of --tx-base64, --tx-hex, --tx-file")
	}

	client := cosmoswasm.NewClient(rpc)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var submitResp *cosmoswasm.SubmitTxResponse
	var err error

	if strings.TrimSpace(txBase64) != "" {
		submitResp, err = client.SubmitTxBase64(ctx, txBase64)
	} else {
		var txBytes []byte
		if strings.TrimSpace(txHex) != "" {
			txBytes, err = hex.DecodeString(strings.TrimSpace(txHex))
			if err != nil {
				return fmt.Errorf("invalid --tx-hex: %w", err)
			}
		} else {
			txBytes, err = os.ReadFile(txFile)
			if err != nil {
				return fmt.Errorf("read --tx-file: %w", err)
			}
		}
		submitResp, err = client.SubmitTxBytes(ctx, txBytes)
	}
	if err != nil {
		return err
	}

	fmt.Printf("tx_hash=%s\n", submitResp.Hash)
	if !wait {
		return nil
	}

	result, err := client.WaitTxResult(ctx, submitResp.Hash, time.Duration(pollMs)*time.Millisecond)
	if err != nil {
		return err
	}

	return printJSON(result)
}

func runTxResult(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	hash, _ := cmd.Flags().GetString("hash")

	client := cosmoswasm.NewClient(rpc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	result, err := client.GetTxResult(ctx, hash)
	if err != nil {
		return err
	}

	return printJSON(result)
}

func runContractStore(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	wasmFile, _ := cmd.Flags().GetString("wasm")
	sender, _ := cmd.Flags().GetString("sender")
	wait, _ := cmd.Flags().GetBool("wait")
	pollMs, _ := cmd.Flags().GetUint64("poll-ms")

	wasmBytes, err := os.ReadFile(wasmFile)
	if err != nil {
		return fmt.Errorf("read wasm file: %w", err)
	}

	txBytes, err := cosmoswasm.BuildStoreTx(wasmBytes, sender)
	if err != nil {
		return err
	}

	return submitAndPrint(rpc, txBytes, wait, pollMs)
}

func runContractInstantiate(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	codeID, _ := cmd.Flags().GetUint64("code-id")
	initMsg, _ := cmd.Flags().GetString("init-msg")
	sender, _ := cmd.Flags().GetString("sender")
	label, _ := cmd.Flags().GetString("label")
	admin, _ := cmd.Flags().GetString("admin")
	wait, _ := cmd.Flags().GetBool("wait")
	pollMs, _ := cmd.Flags().GetUint64("poll-ms")

	request := cosmoswasm.InstantiateTxRequest{
		Sender: sender,
		CodeID: codeID,
		Msg:    initMsg,
		Label:  label,
		Admin:  admin,
	}

	txBytes, err := cosmoswasm.BuildInstantiateTx(request)
	if err != nil {
		return err
	}

	return submitAndPrint(rpc, txBytes, wait, pollMs)
}

func runContractExecute(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	contract, _ := cmd.Flags().GetString("contract")
	msg, _ := cmd.Flags().GetString("msg")
	sender, _ := cmd.Flags().GetString("sender")
	wait, _ := cmd.Flags().GetBool("wait")
	pollMs, _ := cmd.Flags().GetUint64("poll-ms")

	request := cosmoswasm.ExecuteTxRequest{
		Sender:   sender,
		Contract: contract,
		Msg:      msg,
	}

	txBytes, err := cosmoswasm.BuildExecuteTx(request)
	if err != nil {
		return err
	}

	return submitAndPrint(rpc, txBytes, wait, pollMs)
}

func runContractQuery(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	contract, _ := cmd.Flags().GetString("contract")
	msg, _ := cmd.Flags().GetString("msg")

	client := cosmoswasm.NewClient(rpc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	response, err := client.QuerySmartRaw(ctx, contract, msg)
	if err != nil {
		return err
	}

	return printJSON(response)
}

func runContractBalance(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	contract, _ := cmd.Flags().GetString("contract")
	address, _ := cmd.Flags().GetString("address")

	query := map[string]any{
		"balance": map[string]any{
			"address": address,
		},
	}

	client := cosmoswasm.NewClient(rpc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	response, err := client.QuerySmartRaw(ctx, contract, query)
	if err != nil {
		return err
	}

	return printJSON(response)
}

func runContractDeploy(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	wasmFile, _ := cmd.Flags().GetString("wasm")
	sender, _ := cmd.Flags().GetString("sender")
	initMsg, _ := cmd.Flags().GetString("init-msg")
	label, _ := cmd.Flags().GetString("label")
	admin, _ := cmd.Flags().GetString("admin")
	pollMs, _ := cmd.Flags().GetUint64("poll-ms")

	wasmBytes, err := os.ReadFile(wasmFile)
	if err != nil {
		return fmt.Errorf("read wasm file: %w", err)
	}

	return deployContract(rpc, wasmBytes, sender, initMsg, label, admin, pollMs)
}

func runContractDeployCW20(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	wasmFile, _ := cmd.Flags().GetString("wasm")
	sender, _ := cmd.Flags().GetString("sender")
	name, _ := cmd.Flags().GetString("name")
	symbol, _ := cmd.Flags().GetString("symbol")
	decimals, _ := cmd.Flags().GetUint64("decimals")
	supply, _ := cmd.Flags().GetString("supply")
	recipient, _ := cmd.Flags().GetString("recipient")
	label, _ := cmd.Flags().GetString("label")
	admin, _ := cmd.Flags().GetString("admin")
	pollMs, _ := cmd.Flags().GetUint64("poll-ms")

	wasmBytes, err := os.ReadFile(wasmFile)
	if err != nil {
		return fmt.Errorf("read wasm file: %w", err)
	}

	if strings.TrimSpace(sender) == "" {
		sender = cosmoswasm.DefaultSender()
	}
	if strings.TrimSpace(recipient) == "" {
		recipient = sender
	}

	init := map[string]any{
		"name":     name,
		"symbol":   symbol,
		"decimals": decimals,
		"initial_balances": []map[string]string{
			{
				"address": recipient,
				"amount":  supply,
			},
		},
		"mint": map[string]string{
			"minter": sender,
		},
		"marketing": nil,
	}

	return deployContract(rpc, wasmBytes, sender, init, label, admin, pollMs)
}

func deployContract(rpc string, wasmBytes []byte, sender string, initMsg any, label, admin string, pollMs uint64) error {

	storeTx, err := cosmoswasm.BuildStoreTx(wasmBytes, sender)
	if err != nil {
		return err
	}

	client := cosmoswasm.NewClient(rpc)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	storeSubmit, err := client.SubmitTxBytes(ctx, storeTx)
	if err != nil {
		return err
	}
	fmt.Printf("store_tx_hash=%s\n", storeSubmit.Hash)

	storeResult, err := client.WaitTxResult(ctx, storeSubmit.Hash, time.Duration(pollMs)*time.Millisecond)
	if err != nil {
		return err
	}
	if storeResult.Code != 0 {
		return fmt.Errorf("store tx failed: code=%d log=%s", storeResult.Code, storeResult.Log)
	}

	codeIDText := findEventAttribute(storeResult.Events, "store_code", "code_id")
	if strings.TrimSpace(codeIDText) == "" {
		return fmt.Errorf("cannot detect code_id from store tx result")
	}

	codeID, err := strconv.ParseUint(codeIDText, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid code_id %q: %w", codeIDText, err)
	}
	fmt.Printf("code_id=%d\n", codeID)

	instantiateReq := cosmoswasm.InstantiateTxRequest{
		Sender: sender,
		CodeID: codeID,
		Msg:    initMsg,
		Label:  label,
		Admin:  admin,
	}

	instantiateTx, err := cosmoswasm.BuildInstantiateTx(instantiateReq)
	if err != nil {
		return err
	}

	initSubmit, err := client.SubmitTxBytes(ctx, instantiateTx)
	if err != nil {
		return err
	}
	fmt.Printf("instantiate_tx_hash=%s\n", initSubmit.Hash)

	initResult, err := client.WaitTxResult(ctx, initSubmit.Hash, time.Duration(pollMs)*time.Millisecond)
	if err != nil {
		return err
	}
	if initResult.Code != 0 {
		return fmt.Errorf("instantiate tx failed: code=%d log=%s", initResult.Code, initResult.Log)
	}

	contractAddr := findEventAttribute(initResult.Events, "instantiate", "_contract_address")
	if strings.TrimSpace(contractAddr) == "" {
		contractAddr = findEventAttribute(initResult.Events, "instantiate", "contract_address")
	}
	if strings.TrimSpace(contractAddr) != "" {
		fmt.Printf("contract_address=%s\n", contractAddr)
	}

	return nil
}

func runContractTransfer(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	contract, _ := cmd.Flags().GetString("contract")
	to, _ := cmd.Flags().GetString("to")
	amount, _ := cmd.Flags().GetString("amount")
	sender, _ := cmd.Flags().GetString("sender")
	wait, _ := cmd.Flags().GetBool("wait")
	pollMs, _ := cmd.Flags().GetUint64("poll-ms")

	msg := map[string]any{
		"transfer": map[string]string{
			"recipient": to,
			"amount":    amount,
		},
	}

	request := cosmoswasm.ExecuteTxRequest{
		Sender:   sender,
		Contract: contract,
		Msg:      msg,
	}

	txBytes, err := cosmoswasm.BuildExecuteTx(request)
	if err != nil {
		return err
	}

	return submitAndPrint(rpc, txBytes, wait, pollMs)
}

func runBankSend(cmd *cobra.Command, args []string) error {
	rpc, _ := cmd.Flags().GetString("rpc")
	sender, _ := cmd.Flags().GetString("sender")
	to, _ := cmd.Flags().GetString("to")
	amountText, _ := cmd.Flags().GetString("amount")
	wait, _ := cmd.Flags().GetBool("wait")
	pollMs, _ := cmd.Flags().GetUint64("poll-ms")

	if strings.TrimSpace(sender) == "" {
		sender = cosmoswasm.DefaultSender()
	}

	fromAddr, err := sdk.AccAddressFromBech32(sender)
	if err != nil {
		return fmt.Errorf("invalid sender address: %w", err)
	}
	toAddr, err := sdk.AccAddressFromBech32(to)
	if err != nil {
		return fmt.Errorf("invalid recipient address: %w", err)
	}

	coins, err := sdk.ParseCoinsNormalized(amountText)
	if err != nil {
		return fmt.Errorf("invalid amount: %w", err)
	}
	if !coins.IsValid() || coins.IsZero() {
		return fmt.Errorf("amount must be > 0")
	}

	msg := &banktypes.MsgSend{
		FromAddress: fromAddr.String(),
		ToAddress:   toAddr.String(),
		Amount:      coins,
	}

	txBytes, err := buildProtoTxBytes(msg)
	if err != nil {
		return err
	}

	return submitAndPrint(rpc, txBytes, wait, pollMs)
}

func runBankBalance(cmd *cobra.Command, args []string) error {
	rest, _ := cmd.Flags().GetString("rest")
	address, _ := cmd.Flags().GetString("address")

	rest = strings.TrimRight(strings.TrimSpace(rest), "/")
	if rest == "" {
		rest = "http://127.0.0.1:38331"
	}

	url := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", rest, address)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("bank balance query failed (%d). Ensure REST endpoint is enabled. Response: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}

	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		fmt.Println(string(payload))
		return nil
	}

	return printJSON(decoded)
}

func submitAndPrint(rpc string, txBytes []byte, wait bool, pollMs uint64) error {
	client := cosmoswasm.NewClient(rpc)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := client.SubmitTxBytes(ctx, txBytes)
	if err != nil {
		return err
	}

	fmt.Printf("tx_hash=%s\n", response.Hash)
	if !wait {
		return nil
	}

	result, err := client.WaitTxResult(ctx, response.Hash, time.Duration(pollMs)*time.Millisecond)
	if err != nil {
		return err
	}

	return printJSON(result)
}

func findEventAttribute(events []cosmoswasm.TxEvent, eventType, key string) string {
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		for _, attr := range event.Attributes {
			if attr.Key == key {
				return attr.Value
			}
		}
	}
	return ""
}

func printJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(encoded))
	return nil
}

func buildProtoTxBytes(msgs ...sdk.Msg) ([]byte, error) {
	packedMsgs := make([]*codectypes.Any, 0, len(msgs))
	for _, msg := range msgs {
		anyMsg, err := codectypes.NewAnyWithValue(msg)
		if err != nil {
			return nil, err
		}
		packedMsgs = append(packedMsgs, anyMsg)
	}

	bodyBytes, err := proto.Marshal(&txv1beta1.TxBody{Messages: packedMsgs})
	if err != nil {
		return nil, err
	}

	authInfoBytes, err := proto.Marshal(&txv1beta1.AuthInfo{})
	if err != nil {
		return nil, err
	}

	return proto.Marshal(&txv1beta1.TxRaw{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
		Signatures:    nil,
	})
}
