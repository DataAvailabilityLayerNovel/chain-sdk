package main // Executable package for running this dApp

import (
	"context" // For managing request timeout and cancellation
	"errors"  // For errors.Is / errors.As sentinel matching
	"fmt"     // For printing output to console
	"github.com/joho/godotenv"
	"log"           // For logging and fatal error handling
	"math"          // For ceil(gas * minGasPrice) fee computation
	"net/http"      // For a custom http.Client (WithHTTPClient demo)
	"os"            // For file operations and environment variables
	"path/filepath" // For walking parent dirs to locate the project .env
	"strconv"       // For string-to-number conversions (ParseUint)
	"strings"       // For string manipulation (TrimSpace)
	"time"          // For timeout duration and blocking operations

	sdk "github.com/cosmos/cosmos-sdk/types" // For sdk.Coins in the faucet demo

	// The dApp's only required import — see the SDK package doc.
	cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() { // Entry point of the program
	// ──────────────────────────────────────────────────────────
	// Configuration (env-overridable, sensible defaults)
	// ──────────────────────────────────────────────────────────
	// EXEC_URL  — cosmos-exec-grpc HTTP endpoint (default: SDK default 127.0.0.1:50051)
	// WASM_FILE — compiled CosmWasm artifact to deploy
	// CHAIN_ID  — set together with APP_PRIVKEY to switch to the SIGNED tx path
	// APP_PRIVKEY — 32-byte hex secp256k1 key used to sign txs
	// Load the project's .env by walking up from the working directory, so the
	// example reuses the same config as the rest of ev-node (it lives at the
	// repo root). godotenv does not override variables already set in the real
	// environment, so an explicit `KEY=... go run .` still wins.
	loadDotEnv()
	execURL := envOr("EXEC_URL", cosmoswasm.DefaultExecAPIURL)
	wasmFile := envOr("WASM_FILE", "contract/artifacts/my_counter.wasm")
	// The executor enforces signatures + a non-zero min gas price, so the chain
	// ID and a funded key are required. Accept the example's own names first,
	// then fall back to the canonical COSMOS_EXEC_* names used in ev-node/.env.
	chainID := envFirst("CHAIN_ID", "COSMOS_EXEC_CHAIN_ID")
	privHex := envFirst("APP_PRIVKEY", "COSMOS_EXEC_TREASURY_PRIVKEY_HEX")

	// NewClientFromConfig is the recommended (production) constructor: it adds
	// a request timeout plus automatic retries on transient failures such as
	// "connection refused", so a not-yet-ready node doesn't abort the run.
	client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
		ExecURL:       execURL,
		Timeout:       20 * time.Second,
		RetryAttempts: 5,
		RetryDelay:    2 * time.Second,
		ChainID:       chainID,
	})
	if err != nil {
		log.Fatalf("create client: %v", err)
	}

	// Single context governing the whole tx lifecycle. DefaultTxTimeout is the
	// SDK's suggested budget; we widen it because we run three txs end to end.
	ctx, cancel := context.WithTimeout(context.Background(), 3*cosmoswasm.DefaultTxTimeout)
	defer cancel()

	// ──────────────────────────────────────────────────────────
	// App identity: derive this dApp's Celestia namespace
	// ──────────────────────────────────────────────────────────
	// NamespaceFromString deterministically derives a v0 namespace from a
	// human-readable label. We round-trip it through NamespaceFromHex and
	// Equal to prove the encoding is stable.
	ns := cosmoswasm.NamespaceFromString("my-counter-dapp")
	nsRoundTrip, err := cosmoswasm.NamespaceFromHex(ns.Hex())
	if err != nil {
		log.Fatalf("namespace round-trip: %v", err)
	}
	fmt.Printf("App namespace: %s (stable=%t)\n", ns.String(), ns.Equal(nsRoundTrip))

	// ──────────────────────────────────────────────────────────
	// Sender / signer setup
	// ──────────────────────────────────────────────────────────
	// Two supported modes:
	//   • Unsigned (default): uses DefaultSender(), works when the executor
	//     does not enforce signatures — matches the canonical SDK example.
	//   • Signed: when CHAIN_ID + APP_PRIVKEY are set, build a Signer and use
	//     the BuildSigned* tx builders (required if signatures are enforced).
	var signer *cosmoswasm.Signer
	sender := cosmoswasm.DefaultSender()
	if chainID != "" && privHex != "" {
		signer, err = cosmoswasm.NewSignerFromHex(privHex, chainID)
		if err != nil {
			log.Fatalf("create signer: %v", err)
		}
		// Per-tx gas limit. The chain's ante feePolicy requires a fee of
		// ceil(gasLimit * minGasPrice) in the gas denom when
		// COSMOS_EXEC_MIN_GAS_PRICE > 0 — a 0-fee signed tx is rejected with
		// ErrInsufficientFee regardless of treasury balance. These defaults
		// mirror the chain's COSMOS_EXEC_* env (see ev-node/.env); override
		// via GAS_LIMIT / MIN_GAS_PRICE / GAS_DENOM.
		const defaultGasLimit = 80_000_000
		gasLimit := uint64(defaultGasLimit)
		if v := strings.TrimSpace(os.Getenv("GAS_LIMIT")); v != "" {
			if g, perr := strconv.ParseUint(v, 10, 64); perr == nil && g > 0 {
				gasLimit = g
			}
		}
		gasDenom := envFirstOr("ustake", "GAS_DENOM", "COSMOS_EXEC_GAS_DENOM")
		minGasPrice := 0.000001
		if v := envFirst("MIN_GAS_PRICE", "COSMOS_EXEC_MIN_GAS_PRICE"); v != "" {
			if p, perr := strconv.ParseFloat(v, 64); perr == nil && p > 0 {
				minGasPrice = p
			}
		}
		feeAmt := int64(math.Ceil(float64(gasLimit) * minGasPrice))
		fee := sdk.NewCoins(sdk.NewInt64Coin(gasDenom, feeAmt))

		signer = signer.
			WithGasLimit(gasLimit). // constrain compute per tx
			WithFee(fee)            // satisfy ante feePolicy (non-zero min gas price)
		sender = signer.Address()
		fmt.Printf("Signed mode: chain_id=%s signer=%s\n", chainID, sender)
		fmt.Printf("  gas_limit=%d fee=%s (min_gas_price=%g)\n", gasLimit, fee, minGasPrice)
	} else {
		fmt.Printf("Unsigned mode: sender=%s\n", sender)
	}

	// FetchAccount queries the chain's auth state for the sender. The account
	// may not exist yet (fresh chain) — that is not an error here.
	if acct, aerr := client.FetchAccount(ctx, sender); aerr != nil {
		fmt.Printf("  (account lookup skipped: %v)\n", friendly(aerr))
	} else {
		fmt.Printf("  account exists=%t number=%d sequence=%d\n",
			acct.Exists, acct.AccountNumber, acct.Sequence)
	}

	// ──────────────────────────────────────────────────────────
	// Step 1: Store WASM code
	// ──────────────────────────────────────────────────────────
	fmt.Println("\nStep 1 — Store WASM code")

	wasmBytes, err := os.ReadFile(wasmFile)
	if err != nil {
		log.Fatalf("read wasm file %q: %v", wasmFile, err)
	}
	fmt.Printf("  wasm size: %d bytes\n", len(wasmBytes))

	var storeTx []byte
	if signer != nil {
		storeTx, err = cosmoswasm.BuildSignedStoreTx(ctx, client, signer, wasmBytes)
	} else {
		storeTx, err = cosmoswasm.BuildStoreTx(wasmBytes, sender)
	}
	if err != nil {
		log.Fatalf("build store tx: %v", err)
	}

	// Exercise the explicit encoders + the base64 submit path. SubmitTxBytes
	// would do the encoding for us; doing it by hand here demonstrates the
	// Tier-2 helpers and keeps the wire payload inspectable.
	fmt.Printf("  tx hex prefix: %s…\n", head(cosmoswasm.EncodeTxHex(storeTx), 24))
	storeRes, err := client.SubmitTxBase64(ctx, cosmoswasm.EncodeTxBase64(storeTx))
	if err != nil {
		log.Fatalf("submit store tx: %v", friendly(err))
	}
	fmt.Printf("  tx_hash: %s\n", storeRes.Hash)

	storeResult := mustSucceed(client, ctx, storeRes.Hash, "store")
	codeIDStr := findEventValue(storeResult.Events, "code_id")
	codeID, err := strconv.ParseUint(codeIDStr, 10, 64)
	if err != nil {
		log.Fatalf("parse code_id %q: %v", codeIDStr, err)
	}
	fmt.Printf("  code_id: %d (height=%d)\n", codeID, storeResult.Height)

	// ──────────────────────────────────────────────────────────
	// Step 2: Instantiate contract
	// ──────────────────────────────────────────────────────────
	fmt.Println("\nStep 2 — Instantiate contract")

	initReq := cosmoswasm.InstantiateTxRequest{
		Sender: sender,
		CodeID: codeID,
		Label:  "my-counter-instance",
		Msg:    map[string]any{"count": 0}, // must match Rust InstantiateMsg
	}
	var initTx []byte
	if signer != nil {
		initTx, err = cosmoswasm.BuildSignedInstantiateTx(ctx, client, signer, initReq)
	} else {
		initTx, err = cosmoswasm.BuildInstantiateTx(initReq)
	}
	if err != nil {
		log.Fatalf("build instantiate tx: %v", err)
	}

	initRes, err := client.SubmitTxBytes(ctx, initTx)
	if err != nil {
		log.Fatalf("submit instantiate tx: %v", friendly(err))
	}
	initResult := mustSucceed(client, ctx, initRes.Hash, "instantiate")

	// Different wasmd versions emit the address under either key.
	contractAddr := firstNonEmpty(
		findEventValue(initResult.Events, "_contract_address"),
		findEventValue(initResult.Events, "contract_address"),
	)
	if contractAddr == "" {
		log.Fatalf("contract address not found in instantiate events")
	}
	fmt.Printf("  contract: %s (height=%d)\n", contractAddr, initResult.Height)

	// ──────────────────────────────────────────────────────────
	// Step 3: Execute — increment counter
	// ──────────────────────────────────────────────────────────
	fmt.Println("\nStep 3 — Execute: increment")

	execReq := cosmoswasm.ExecuteTxRequest{
		Sender:   sender,
		Contract: contractAddr,
		Msg:      map[string]any{"increment": struct{}{}}, // must match Rust ExecuteMsg
	}
	var execTx []byte
	if signer != nil {
		execTx, err = cosmoswasm.BuildSignedExecuteTx(ctx, client, signer, execReq)
	} else {
		execTx, err = cosmoswasm.BuildExecuteTx(execReq)
	}
	if err != nil {
		log.Fatalf("build execute tx: %v", err)
	}

	execRes, err := client.SubmitTxBytes(ctx, execTx)
	if err != nil {
		log.Fatalf("submit execute tx: %v", friendly(err))
	}
	execResult := mustSucceed(client, ctx, execRes.Hash, "execute")
	fmt.Printf("  success at height=%d\n", execResult.Height)

	// ──────────────────────────────────────────────────────────
	// Step 4: Query — get count (raw + typed)
	// ──────────────────────────────────────────────────────────
	fmt.Println("\nStep 4 — Query: get_count")

	// QuerySmartRaw returns the undecoded payload; QuerySmart decodes it into
	// a map. We call both to demonstrate the two query entry points.
	raw, err := client.QuerySmartRaw(ctx, contractAddr, map[string]any{"get_count": struct{}{}})
	if err != nil {
		log.Fatalf("query raw: %v", friendly(err))
	}
	fmt.Printf("  raw data: %v%s\n", raw.Data, raw.DataRaw)

	result, err := client.QuerySmart(ctx, contractAddr, map[string]any{"get_count": struct{}{}})
	if err != nil {
		log.Fatalf("query: %v", friendly(err))
	}
	fmt.Printf("  count = %v\n", result["count"])

	// ──────────────────────────────────────────────────────────
	// Step 5: Extra SDK coverage
	// ──────────────────────────────────────────────────────────
	coverageDemo(ctx, execURL, contractAddr, signer, chainID, privHex)

	// ──────────────────────────────────────────────────────────
	// Step 6: Faucet (optional — gated by ENABLE_FAUCET)
	// ──────────────────────────────────────────────────────────
	faucetStep(ctx, execURL, signer, chainID, privHex)

	fmt.Println("\nAll steps passed.")
}

// coverageDemo exercises the remaining public SDK surface so a test run can
// touch as many entry points as possible. Anything that would be rejected by
// the counter contract (blob/batch/bank messages) is only *built* and encoded,
// never submitted — building is what we want to cover.
func coverageDemo(ctx context.Context, execURL, contractAddr string, signer *cosmoswasm.Signer, chainID, privHex string) {
	fmt.Println("\nStep 5 — Extra SDK coverage")

	// 5a. DefaultSDKConfig + Validate: the zero-value config must reject an
	// empty ExecURL, then accept it once set.
	cfg := cosmoswasm.DefaultSDKConfig()
	if err := cfg.Validate(); err == nil {
		log.Fatalf("DefaultSDKConfig.Validate should reject empty ExecURL")
	}
	cfg.ExecURL = execURL
	if err := cfg.Validate(); err != nil {
		log.Fatalf("validate config: %v", err)
	}
	fmt.Printf("  config validated (timeout=%s)\n", cfg.Timeout)

	// 5b. NewClient (quick constructor) + WithHTTPClient (custom transport),
	// then a real read to prove the alternate client works end to end.
	altClient := cosmoswasm.NewClient(execURL).
		WithHTTPClient(&http.Client{Timeout: 30 * time.Second})
	if res, err := altClient.QuerySmart(ctx, contractAddr,
		map[string]any{"get_count": struct{}{}}); err != nil {
		fmt.Printf("  alt client query skipped: %v\n", friendly(err))
	} else {
		fmt.Printf("  alt client count = %v\n", res["count"])
	}

	// 5c. NewNamespaceV0 + Bytes(): the low-level namespace constructor.
	customNS, err := cosmoswasm.NewNamespaceV0([]byte("counter"))
	if err != nil {
		log.Fatalf("NewNamespaceV0: %v", err)
	}
	fmt.Printf("  custom namespace bytes len=%d hex=%s\n",
		len(customNS.Bytes()), customNS.Hex())

	// 5d. Blob-first builders. The counter contract does not handle these
	// messages, so we build + encode only (no submit) to cover the builders
	// and their request types.
	blobTx, err := cosmoswasm.BuildBlobCommitTx(cosmoswasm.BlobCommitTxRequest{
		Contract:   contractAddr,
		Commitment: strings.Repeat("ab", 32), // 32-byte hex placeholder
		Tag:        "snapshot",
		Extra:      map[string]any{"height": 1},
	})
	if err != nil {
		log.Fatalf("BuildBlobCommitTx: %v", err)
	}
	batchTx, err := cosmoswasm.BuildBatchRootTx(cosmoswasm.BatchRootTxRequest{
		Contract: contractAddr,
		Root:     strings.Repeat("cd", 32),
		Count:    8,
		Tag:      "events",
	})
	if err != nil {
		log.Fatalf("BuildBatchRootTx: %v", err)
	}
	fmt.Printf("  blob-commit tx: %d bytes, batch-root tx: %d bytes (built, not submitted)\n",
		len(blobTx), len(batchTx))

	// 5f. Dev-facing reads (bọc thêm): ước lượng chi phí, đọc block, đếm mempool,
	// simulate gas. Đều an toàn — không phát sinh giao dịch.
	obs := cosmoswasm.NewClient(execURL)
	if est, eerr := obs.EstimateCost(ctx, cosmoswasm.EstimateRequest{Bytes: 1024, Gas: 200_000}); eerr == nil {
		fmt.Printf("  estimate(1KB, 200k gas): DA=%s%s gas=%s%s\n",
			est.EstDAAmount, est.EstDADenom, est.EstGasAmount, est.EstGasDenom)
	}
	if blk, found, berr := obs.GetLatestBlock(ctx); berr == nil && found {
		fmt.Printf("  latest block: height=%d num_txs=%d\n", blk.Height, blk.NumTxs)
		if byH, herr := obs.GetBlockByHeight(ctx, blk.Height); herr == nil {
			fmt.Printf("  block#%d app_hash=%s…\n", byH.Height, head(byH.AppHash, 12))
		}
	}
	if n, perr := obs.GetPendingTxCount(ctx); perr == nil {
		fmt.Printf("  pending tx in mempool: %d\n", n)
	}
	// SimulateTx cần tx bytes: dựng một execute (increment) trên contract thật để
	// đo gas. Ở chế độ enforce chữ ký, tx chưa ký có thể bị từ chối → bỏ qua.
	if simTx, serr := cosmoswasm.BuildExecuteTx(cosmoswasm.ExecuteTxRequest{
		Contract: contractAddr,
		Msg:      map[string]any{"increment": struct{}{}},
	}); serr == nil {
		if sim, simErr := obs.SimulateTx(ctx, simTx); simErr == nil {
			fmt.Printf("  simulate increment: gas_used=%d gas_limit=%d fee=%s%s\n",
				sim.GasUsed, sim.GasLimit, sim.FeeAmount, sim.FeeDenom)
		} else {
			fmt.Printf("  simulate skipped: %v\n", friendly(simErr))
		}
	}

	// 5e. Faucet helpers. TreasuryAddressFromHex is offline; BuildSignedBankSend
	// signs offline too. Only submitted when FAUCET_TO is set and we have a key.
	if privHex == "" || chainID == "" {
		fmt.Println("  faucet demo skipped (set CHAIN_ID + APP_PRIVKEY to enable)")
		return
	}
	treasury, err := cosmoswasm.TreasuryAddressFromHex(privHex)
	if err != nil {
		log.Fatalf("TreasuryAddressFromHex: %v", err)
	}
	to := envOr("FAUCET_TO", treasury) // default: self-send (always valid)
	amount := sdk.NewCoins(sdk.NewInt64Coin("stake", 1))

	var accNum, seq uint64
	if signer != nil {
		if acct, aerr := cosmoswasm.NewClient(execURL).FetchAccount(ctx, treasury); aerr == nil {
			accNum, seq = acct.AccountNumber, acct.Sequence
		}
	}
	// Cover the 0-fee variant (built only — would be rejected by the ante
	// feePolicy if submitted on a min-gas-price chain).
	if _, _, berr := cosmoswasm.BuildSignedBankSend(privHex, chainID, to, amount, accNum, seq, 200_000); berr != nil {
		log.Fatalf("BuildSignedBankSend: %v", berr)
	}
	// The submittable tx carries a fee: ceil(gas * minGasPrice) in the gas
	// denom, same rule as the contract txs above.
	const bankGas = 200_000
	gasDenom := envFirstOr("ustake", "GAS_DENOM", "COSMOS_EXEC_GAS_DENOM")
	bankFeeAmt := int64(math.Ceil(float64(bankGas) * 0.000001))
	if bankFeeAmt < 1 {
		bankFeeAmt = 1
	}
	bankFee := sdk.NewCoins(sdk.NewInt64Coin(gasDenom, bankFeeAmt))
	from, sendTx, err := cosmoswasm.BuildSignedBankSendWithFee(
		privHex, chainID, to, amount, bankFee, accNum, seq, bankGas)
	if err != nil {
		log.Fatalf("BuildSignedBankSendWithFee: %v", err)
	}
	fmt.Printf("  faucet send built: from=%s to=%s fee=%s (%d bytes)\n", from, to, bankFee, len(sendTx))
}

// faucetStep submits a signed bank-send. Gated by ENABLE_FAUCET so it can be
// turned on for chains where the signer holds funds, off everywhere else.
// FAUCET_TO defaults to the signer's own address (self-send is always valid);
// FAUCET_AMOUNT / FAUCET_DENOM override the transferred coin.
func faucetStep(ctx context.Context, execURL string, signer *cosmoswasm.Signer, chainID, privHex string) {
	fmt.Println("\nStep 6 — Faucet")

	if !faucetEnabled() {
		fmt.Println("  skipped (set ENABLE_FAUCET=true)")
		return
	}
	if signer == nil || chainID == "" || privHex == "" {
		log.Fatalf("faucet requires signed mode: set CHAIN_ID + APP_PRIVKEY")
	}

	treasury, err := cosmoswasm.TreasuryAddressFromHex(privHex)
	if err != nil {
		log.Fatalf("TreasuryAddressFromHex: %v", err)
	}
	to := envOr("FAUCET_TO", treasury)

	amt := int64(1)
	if v := strings.TrimSpace(os.Getenv("FAUCET_AMOUNT")); v != "" {
		parsed, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || parsed < 1 {
			log.Fatalf("invalid FAUCET_AMOUNT %q", v)
		}
		amt = parsed
	}
	denom := envOr("FAUCET_DENOM", "stake")
	amount := sdk.NewCoins(sdk.NewInt64Coin(denom, amt))

	client := cosmoswasm.NewClient(execURL)
	var accNum, seq uint64
	if acct, aerr := client.FetchAccount(ctx, treasury); aerr == nil {
		accNum, seq = acct.AccountNumber, acct.Sequence
	}

	// Same fee rule as the contract txs: ceil(gas * minGasPrice) in the gas denom.
	const bankGas = 200_000
	gasDenom := envFirstOr("ustake", "GAS_DENOM", "COSMOS_EXEC_GAS_DENOM")
	bankFeeAmt := int64(math.Ceil(float64(bankGas) * 0.000001))
	if bankFeeAmt < 1 {
		bankFeeAmt = 1
	}
	bankFee := sdk.NewCoins(sdk.NewInt64Coin(gasDenom, bankFeeAmt))

	from, sendTx, err := cosmoswasm.BuildSignedBankSendWithFee(
		privHex, chainID, to, amount, bankFee, accNum, seq, bankGas)
	if err != nil {
		log.Fatalf("BuildSignedBankSendWithFee: %v", err)
	}
	fmt.Printf("  from=%s to=%s amount=%s fee=%s\n", from, to, amount, bankFee)

	res, err := client.SubmitTxBytes(ctx, sendTx)
	if err != nil {
		log.Fatalf("submit faucet tx: %v", friendly(err))
	}
	fmt.Printf("  tx_hash: %s\n", res.Hash)
	result := mustSucceed(client, ctx, res.Hash, "faucet")
	fmt.Printf("  success at height=%d\n", result.Height)
}

func faucetEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_FAUCET"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// mustSucceed waits for a tx to finalize, then re-fetches it via GetTxResult
// to demonstrate the non-blocking accessor, and aborts on a non-zero code.
func mustSucceed(client *cosmoswasm.Client, ctx context.Context, hash, label string) *cosmoswasm.TxExecutionResult {
	res, err := client.WaitTxResult(ctx, hash, cosmoswasm.DefaultPollInterval)
	if err != nil {
		log.Fatalf("wait %s result: %v", label, friendly(err))
	}
	if res.Code != 0 {
		log.Fatalf("%s failed: code=%d log=%s", label, res.Code, res.Log)
	}
	// Confirm GetTxResult now reports the tx as found (it backs WaitTxResult).
	if got, gerr := client.GetTxResult(ctx, hash); gerr == nil && !got.Found {
		log.Fatalf("%s tx %s vanished after finalization", label, hash)
	}
	return res
}

// mustFinalize blocks until a tx reaches DA-level finality (its data is
// permanently available and ordered on the DA layer), and aborts otherwise.
func mustFinalize(client *cosmoswasm.Client, ctx context.Context, hash, label string) *cosmoswasm.TxExecutionResult {
	res, err := client.WaitTxFinality(ctx, hash, cosmoswasm.FinalityDA, cosmoswasm.DefaultPollInterval)
	if err != nil {
		log.Fatalf("wait %s finality: %v", label, friendly(err))
	}
	if res.Code != 0 {
		log.Fatalf("%s failed: code=%d log=%s", label, res.Code, res.Log)
	}
	return res
}

// findEventValue returns the first non-empty value for key across all events.
func findEventValue(events []cosmoswasm.TxEvent, key string) string {
	for _, event := range events {
		for _, attr := range event.Attributes {
			if strings.TrimSpace(attr.Key) == key && strings.TrimSpace(attr.Value) != "" {
				return strings.TrimSpace(attr.Value)
			}
		}
	}
	return ""
}

// friendly augments transport errors with the SDK's structured diagnostics
// when present, and a hint for the most common "node not running" case.
func friendly(err error) string {
	if err == nil {
		return ""
	}
	var sdkErr *cosmoswasm.SDKError
	if errors.As(err, &sdkErr) {
		return sdkErr.Error()
	}
	if errors.Is(err, cosmoswasm.ErrNotReachable) ||
		strings.Contains(err.Error(), "connection refused") {
		return err.Error() + "\n  hint: is cosmos-exec-grpc running on " +
			cosmoswasm.DefaultExecAPIURL + "? set EXEC_URL to override."
	}
	return err.Error()
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// envFirst returns the first non-empty value among keys, so the example can
// accept its own variable names while falling back to the canonical
// COSMOS_EXEC_* names defined in ev-node/.env.
func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// envFirstOr is envFirst with a default when none of the keys are set.
func envFirstOr(fallback string, keys ...string) string {
	if v := envFirst(keys...); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv loads the nearest .env file found by walking up from the current
// working directory to the filesystem root. This lets the example reuse the
// repo-root ev-node/.env regardless of where `go run` is invoked from. Loading
// is best-effort: a missing .env is not an error. godotenv.Load does not
// override variables already present in the real environment.
func loadDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if _, statErr := os.Stat(candidate); statErr == nil {
			_ = godotenv.Load(candidate)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return // reached filesystem root
		}
		dir = parent
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
