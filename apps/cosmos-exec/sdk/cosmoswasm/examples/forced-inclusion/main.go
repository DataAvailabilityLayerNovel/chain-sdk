// Package main demonstrates "forced inclusion" — the censorship-resistance
// escape hatch of Evolve. Instead of submitting a tx to the sequencer
// (cosmos-exec-grpc /tx/submit), the user posts the raw tx bytes directly to
// the DA layer (Celestia) inside a dedicated "forced inclusion" namespace.
// The sequencer is required (by evnode consensus rules) to include those tx
// in a block before the current DA epoch ends; otherwise full nodes reject
// the chain.
//
// Prerequisites:
//
//  1. evnode + sequencer started WITH forced inclusion enabled. From the
//     project root:
//
//	   go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go \
//	       --clean-on-start=true \
//	       --block-time=2s \
//	       --forced-inclusion-namespace=rollup-fi \
//	       --forced-inclusion-epoch=10
//
//  2. The same DA bridge node credentials in .env that the chain uses
//     (DA_BRIDGE_RPC, DA_AUTH_TOKEN). For a TRUE bypass in production, the
//     user should have an INDEPENDENT bridge node — for this demo we reuse
//     the same one to keep the setup simple.
//
//  3. Build the example:
//
//	   go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/forced-inclusion
//
// What this example does:
//
//   - Builds a CosmWasm MsgStoreCode tx (uploads WASM bytecode — the same kind
//     of tx a normal client submits through cosmos-exec-grpc). Two variants:
//   - default (success): a SIGNED tx with a funded treasury key + gas limit
//     + fee. The executor enforces signatures and a min gas price (see
//     ev-node/.env), so this is what executes with code=0. Needs CHAIN_ID +
//     APP_PRIVKEY (or the COSMOS_EXEC_* equivalents).
//   - --demo-fail: an UNSIGNED tx (no signature/gas/fee). It is still
//     force-included, but the ante handler rejects it with code=11
//     (ErrOutOfGas) — kept to show that inclusion ≠ successful execution.
//   - Posts the raw tx bytes as a Celestia blob in the forced inclusion
//     namespace via Celestia's JSON-RPC `blob.Submit`. The sequencer is
//     bypassed.
//   - Polls cosmos-exec-grpc /tx/result/{hash} until the tx appears, proving
//     the sequencer was forced to include it (and, for the signed tx, that it
//     executed successfully).
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	libshare "github.com/celestiaorg/go-square/v3/share"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/joho/godotenv"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
	jsonrpc "github.com/DataAvailabilityLayerNovel/chain-sdk/pkg/da/jsonrpc"
)

func main() {
	// Load the nearest .env (walking up to the repo root) BEFORE declaring
	// flags, since the flag defaults below read DA_BRIDGE_RPC / DA_AUTH_TOKEN
	// from the environment. godotenv does not override variables already set in
	// the real environment.
	loadDotEnv()

	var (
		execURL      = flag.String("exec-url", "http://127.0.0.1:50051", "cosmos-exec-grpc URL")
		daBridgeRPC  = flag.String("da-rpc", os.Getenv("DA_BRIDGE_RPC"), "Celestia bridge JSON-RPC URL")
		daAuthToken  = flag.String("da-token", os.Getenv("DA_AUTH_TOKEN"), "Celestia bridge auth token (Bearer)")
		nsString     = flag.String("namespace", "rollup-fi", "forced inclusion namespace string")
		gasPrice     = flag.Float64("gas-price", envFloat("DA_GAS_PRICE", 0.005), "Celestia gas price (utia/gas); passed explicitly so the node skips auto gas-price estimation")
		txHex        = flag.String("tx-hex", "", "raw tx bytes in hex; if empty, a StoreCode tx is built from --wasm-file")
		wasmFile     = flag.String("wasm-file", "", "path to WASM bytecode for the demo StoreCode tx; defaults to the bundled my-counter artifact")
		demoFail     = flag.Bool("demo-fail", false, "build the UNSIGNED demo tx that is force-included but fails to execute (code=11), instead of the signed success tx")
		pollInterval = flag.Duration("poll", 2*time.Second, "interval to poll /tx/result")
		pollTimeout  = flag.Duration("timeout", 5*time.Minute, "give up after this duration")
	)
	flag.Parse()

	// The executor enforces signatures + a non-zero min gas price (see
	// ev-node/.env: COSMOS_EXEC_ENFORCE_SIGNATURES / COSMOS_EXEC_MIN_GAS_PRICE),
	// so the SUCCESS path needs a chain ID and a funded key — same as the
	// my-counter example's signed mode. Accept the example's own names first,
	// then fall back to the canonical COSMOS_EXEC_* names used in ev-node/.env.
	chainID := envFirst("CHAIN_ID", "COSMOS_EXEC_CHAIN_ID")
	privHex := envFirst("APP_PRIVKEY", "COSMOS_EXEC_TREASURY_PRIVKEY_HEX")

	if strings.TrimSpace(*daBridgeRPC) == "" {
		log.Fatal("DA bridge RPC URL is required (--da-rpc or DA_BRIDGE_RPC)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *pollTimeout)
	defer cancel()

	// The exec client is needed up front: the SIGNED success path queries the
	// chain for the treasury's account_number + sequence (via the signer), and
	// we reuse the same client at the end to poll /tx/result.
	client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
		ExecURL: *execURL,
		Timeout: 10 * time.Second,
		ChainID: chainID,
	})
	if err != nil {
		log.Fatalf("init exec client: %v", err)
	}

	// 1. Resolve the raw tx bytes.
	var txBytes []byte
	switch {
	case strings.TrimSpace(*txHex) != "":
		b, derr := hex.DecodeString(strings.TrimPrefix(*txHex, "0x"))
		if derr != nil {
			log.Fatalf("decode --tx-hex: %v", derr)
		}
		txBytes = b

	case *demoFail:
		// ── FAILING demo tx (kept on purpose) ──────────────────────────────
		// An UNSIGNED MsgStoreCode from the placeholder DefaultSender. It is a
		// well-formed Cosmos tx, so it decodes fine — but the executor enforces
		// signatures + a min gas price (COSMOS_EXEC_ENFORCE_SIGNATURES /
		// COSMOS_EXEC_MIN_GAS_PRICE), and this tx carries no signature, gas
		// limit (0), or fee. The ante handler runs it against a 0-gas meter and
		// aborts with code=11 (ErrOutOfGas, "out of gas") before the message is
		// ever executed.
		//
		// We keep it to demonstrate the core forced-inclusion guarantee: the
		// sequencer is forced to INCLUDE the tx (it lands in a block at a real
		// height), but inclusion does NOT imply successful execution — the chain
		// still validates the tx normally and rejects an unpayable one.
		wasmBytes := mustReadWasm(*wasmFile)
		b, berr := cosmoswasm.BuildStoreTx(wasmBytes, cosmoswasm.DefaultSender())
		if berr != nil {
			log.Fatalf("build unsigned StoreCode tx: %v", berr)
		}
		txBytes = b
		log.Printf("[demo-fail] submitting UNSIGNED StoreCode tx (expect force-include then code=11 out of gas); wasm=%d bytes tx=%d bytes", len(wasmBytes), len(txBytes))

	default:
		// ── SUCCESS demo tx ────────────────────────────────────────────────
		// A SIGNED MsgStoreCode that satisfies the ante handler, mirroring the
		// my-counter example's signed mode: a funded treasury key signs the tx,
		// with a gas limit and a fee = ceil(gasLimit * minGasPrice) in the gas
		// denom. Force-included AND executed with code=0.
		if chainID == "" || privHex == "" {
			log.Fatal("success tx needs a signed mode: set CHAIN_ID + APP_PRIVKEY " +
				"(or COSMOS_EXEC_CHAIN_ID + COSMOS_EXEC_TREASURY_PRIVKEY_HEX in .env), " +
				"or pass --demo-fail to run the failing-tx demo, or --tx-hex for a prebuilt tx")
		}
		signer, serr := cosmoswasm.NewSignerFromHex(privHex, chainID)
		if serr != nil {
			log.Fatalf("create signer: %v", serr)
		}
		// Same fee rule as my-counter: the ante feePolicy rejects a 0-fee tx
		// when COSMOS_EXEC_MIN_GAS_PRICE > 0, regardless of treasury balance.
		const gasLimit = 80_000_000
		gasDenom := envFirstOr("ustake", "GAS_DENOM", "COSMOS_EXEC_GAS_DENOM")
		minGasPrice := 0.000001
		if v := envFirst("MIN_GAS_PRICE", "COSMOS_EXEC_MIN_GAS_PRICE"); v != "" {
			if p, perr := strconv.ParseFloat(v, 64); perr == nil && p > 0 {
				minGasPrice = p
			}
		}
		feeAmt := int64(math.Ceil(float64(gasLimit) * minGasPrice))
		fee := sdk.NewCoins(sdk.NewInt64Coin(gasDenom, feeAmt))
		signer = signer.WithGasLimit(gasLimit).WithFee(fee)

		wasmBytes := mustReadWasm(*wasmFile)
		// BuildSignedStoreTx fetches account_number + sequence from the chain,
		// so the signature is valid when the sequencer force-includes the tx.
		b, berr := cosmoswasm.BuildSignedStoreTx(ctx, client, signer, wasmBytes)
		if berr != nil {
			log.Fatalf("build signed StoreCode tx: %v", berr)
		}
		txBytes = b
		log.Printf("[demo] submitting SIGNED StoreCode tx (signer=%s gas=%d fee=%s wasm=%d bytes tx=%d bytes)",
			signer.Address(), gasLimit, fee, len(wasmBytes), len(txBytes))
	}

	txHash := sha256.Sum256(txBytes)
	txHashHex := strings.ToUpper(hex.EncodeToString(txHash[:]))
	log.Printf("tx hash (SHA-256 upper): %s", txHashHex)
	// 2. Construct the Celestia namespace. evnode/cosmos-exec uses a V0
	// namespace whose subID is SHA-256(name)[:10] — matching the SDK helper
	// cosmoswasm.NamespaceFromString. We replicate that here so the bytes on
	// DA match what the chain's forced-inclusion-retriever scans for.
	ns, err := celestiaNamespaceFromString(*nsString)
	if err != nil {
		log.Fatalf("namespace: %v", err)
	}

	// 3. Connect to Celestia bridge and submit the blob.
	cli, err := jsonrpc.NewClient(ctx, *daBridgeRPC, *daAuthToken, "")
	if err != nil {
		log.Fatalf("connect Celestia bridge: %v", err)
	}
	defer cli.Close()

	blob, err := jsonrpc.NewBlobV0(ns, txBytes)
	if err != nil {
		log.Fatalf("build blob: %v", err)
	}

	// Pass explicit submit options so the bridge skips its auto gas-price
	// estimation, which nil-pointer panics on some celestia-node releases when
	// opts == nil ("panic in rpc method 'blob.Submit': nil pointer dereference").
	opts := &jsonrpc.SubmitOptions{GasPrice: *gasPrice, IsGasPriceSet: true}
	height, err := cli.Blob.Submit(ctx, []*jsonrpc.Blob{blob}, opts)
	if err != nil {
		log.Fatalf("submit blob: %v", err)
	}
	log.Printf("blob submitted to Celestia: DA height=%d namespace=%s", height, *nsString)

	// 4. Wait for the sequencer to pick it up. Forced inclusion is enforced
	// at epoch boundaries — sequencer must include this tx before the epoch
	// containing `height` ends. We poll cosmos-exec-grpc /tx/result (reusing the
	// client created above).
	log.Printf("polling %s/tx/result/%s every %s ...", *execURL, txHashHex, *pollInterval)
	res, err := client.WaitTxResult(ctx, txHashHex, *pollInterval)
	if err != nil {
		log.Fatalf("wait tx: %v (the sequencer may not have processed it yet — check evcosmos logs for 'forced_inclusion')", err)
	}

	log.Printf("INCLUDED: tx force-included at chain height=%d code=%d log=%q", res.Height, res.Code, res.Log)
	if res.Code == 0 {
		log.Printf("SUCCESS: tx was force-included AND executed (code=0).")
	} else {
		log.Printf("note: code=%d means the tx was included but failed to execute (the --demo-fail path returns code=11 ErrOutOfGas). The censorship-resistance guarantee is about INCLUSION, not success — the sequencer cannot refuse to include, but the chain still validates the tx normally.", res.Code)
	}
}

// celestiaNamespaceFromString mirrors cosmoswasm.NamespaceFromString so the
// namespace bytes posted to Celestia exactly match what the chain scans.
//
// NamespaceFromString = V0 namespace with subID = SHA-256(name)[:10].
func celestiaNamespaceFromString(name string) (libshare.Namespace, error) {
	sum := sha256.Sum256([]byte(name))
	return libshare.NewV0Namespace(sum[:10])
}

// mustReadWasm reads the WASM bytecode for the demo StoreCode tx. It uses the
// explicit path when provided, otherwise the bundled my-counter artifact, and
// aborts with a clear message on failure.
func mustReadWasm(wasmFile string) []byte {
	path := strings.TrimSpace(wasmFile)
	if path == "" {
		path = defaultWasmPath()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read wasm %q: %v (pass --wasm-file to point at a .wasm artifact, or --tx-hex for a prebuilt tx)", path, err)
	}
	return b
}

// defaultWasmPath resolves the bundled my-counter WASM artifact relative to
// THIS source file (via runtime.Caller), so `go run .` works regardless of the
// current working directory. The example lives at
// .../examples/forced-inclusion/main.go and the artifact at
// .../examples/my-counter/contract/artifacts/my_counter.wasm.
func defaultWasmPath() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "" // ReadFile will then fail with a clear error.
	}
	dir := filepath.Dir(thisFile)
	return filepath.Join(dir, "..", "my-counter", "contract", "artifacts", "my_counter.wasm")
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

// envFloat reads a float64 from the environment, falling back to def when the
// variable is unset or unparsable.
func envFloat(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
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
