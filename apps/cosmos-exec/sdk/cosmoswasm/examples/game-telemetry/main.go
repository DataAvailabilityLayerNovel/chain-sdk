// Package main demonstrates the SDK's "blob-first" pattern end-to-end with a
// realistic on-chain game: a match-arena where bulk telemetry (player positions,
// events, full-match replays) is pushed DIRECTLY to Celestia DA, and only the
// commitments are recorded on-chain in the `telemetry-registry` CosmWasm
// contract, attached to a match.
//
// Convenience: this example auto-loads the project .env (so it talks to the same
// Celestia bridge as the chain) and, when --contract is omitted, auto-deploys
// the contract wasm from ./contract/artifacts. So the whole demo is two steps:
//
//	# terminal 1 — start the chain (reads .env)
//	go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go
//
//	# terminal 2 — build+drop the wasm (see ./README.md), then:
//	go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry
//
// What it shows: compress → SubmitBlob → record in a match → retrieve → verify;
// chunk a large replay → SubmitBatch → one Merkle root + proof; the on-chain
// game flow register→create→start→record→finish; and reading the telemetry back
// from Celestia via the match's on-chain pointers.
//
// Without a chain (no --exec-url reachable / no wasm) it still runs the
// off-chain half. Override anything via flags or env (DA_BRIDGE_RPC,
// DA_AUTH_TOKEN).
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

// signGasLimit is the per-tx gas limit used for every signed tx in this demo.
// Large enough to cover store_code; fee is derived from it below.
const signGasLimit = 100_000_000

type telemetryFrame struct {
	Match   string         `json:"match"`
	Tick    int            `json:"tick"`
	TS      int64          `json:"ts"`
	Players []playerSample `json:"players"`
}

type playerSample struct {
	ID  string     `json:"id"`
	Pos [3]float64 `json:"pos"`
	HP  int        `json:"hp"`
}

func main() {
	// Load the project .env first so DA_BRIDGE_RPC / DA_AUTH_TOKEN match the
	// chain started by run-cosmos-wasm-nodes.go (shell env still wins).
	projectRoot := findProjectRoot()
	if projectRoot != "" {
		_ = loadDotEnv(filepath.Join(projectRoot, ".env"))
	}

	var (
		daRPC     = flag.String("da-rpc", firstNonEmpty(os.Getenv("DA_BRIDGE_RPC"), "http://127.0.0.1:26658"), "Celestia bridge JSON-RPC URL")
		daToken   = flag.String("da-token", os.Getenv("DA_AUTH_TOKEN"), "Celestia DA auth token")
		daGasP    = flag.Float64("da-gas-price", envFloat("DA_GAS_PRICE", 0.005), "DA gas price (utia/gas); 0 = let the node estimate")
		namespace = flag.String("namespace", firstNonEmpty(os.Getenv("GAME_NAMESPACE"), "my-game"), "App DA namespace for game data")
		execURL   = flag.String("exec-url", "http://127.0.0.1:50051", "cosmos-exec-grpc URL")
		contract  = flag.String("contract", "", "telemetry-registry contract address (skip to auto-deploy --wasm)")
		wasmPath  = flag.String("wasm", defaultWasmPath(projectRoot), "wasm to auto-deploy when --contract is empty")
		frames    = flag.Int("frames", 5, "number of telemetry frames to record")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	bc, err := cosmoswasm.NewBlobClient(ctx, cosmoswasm.BlobClientConfig{
		BridgeRPC: *daRPC,
		AuthToken: *daToken,
		Namespace: *namespace,
		GasPrice:  *daGasP,
	})
	if err != nil {
		log.Fatalf("connect blob client: %v", err)
	}
	defer bc.Close()
	log.Printf("blob client ready: namespace=%s (%s)", *namespace, bc.Namespace())

	// Resolve the contract: explicit --contract, else auto-deploy the wasm, else
	// run off-chain only.
	var client *cosmoswasm.Client
	var signer *cosmoswasm.Signer
	var matchID uint64
	contractAddr := strings.TrimSpace(*contract)
	if contractAddr == "" && *wasmPath != "" && fileExists(*wasmPath) {
		client = mustClient(*execURL)
		signer = mustSigner(ctx, client)
		contractAddr = deployContract(ctx, client, signer, *wasmPath)
		log.Printf("[deploy] telemetry-registry deployed at %s", contractAddr)
	}
	if contractAddr != "" {
		if client == nil {
			client = mustClient(*execURL)
		}
		if signer == nil {
			signer = mustSigner(ctx, client)
		}
		matchID = setupMatch(ctx, client, signer, contractAddr)
		log.Printf("[game] match #%d started (host=%s)", matchID, signer.Address())
	} else {
		log.Printf("[game] no contract (no --contract and no wasm at %s) — running off-chain half only", *wasmPath)
	}

	// ── 1. Record N telemetry frames: compress → submit → record on-chain ────
	var lastSub *cosmoswasm.BlobSubmitResponse
	for i := 0; i < *frames; i++ {
		raw, _ := json.Marshal(makeFrame("match-42", i))
		payload, did := cosmoswasm.CompressIfBeneficial(raw)
		sub, err := bc.SubmitBlob(ctx, payload)
		if err != nil {
			log.Fatalf("submit frame %d: %v", i, err)
		}
		log.Printf("[frame %d] %d→%d bytes (compressed=%v) → Celestia commitment=%s height=%d",
			i, len(raw), len(payload), did, short(sub.Commitment), sub.Height)
		if contractAddr != "" {
			recordTelemetry(ctx, client, signer, contractAddr, matchID, sub)
		}
		lastSub = sub
	}

	// Read one frame back from Celestia and verify integrity.
	if lastSub != nil {
		got, err := bc.RetrieveBlob(ctx, lastSub.Height, lastSub.Commitment)
		if err != nil {
			log.Fatalf("retrieve last frame: %v", err)
		}
		ok, _ := bc.VerifyBlob(ctx, lastSub.Height, lastSub.Commitment)
		restored, _ := cosmoswasm.MaybeDecompress(got)
		log.Printf("[verify] last frame: integrity=%v, restored=%d bytes", ok, len(restored))
		log.Printf("[cost] on-chain footprint per frame ~%d bytes vs %d bytes data",
			len(lastSub.Commitment)/2+8, lastSub.Size)
	}

	// ── 2. Full replay: chunk → batch → one Merkle root → record on-chain ────
	replay := makeReplay(300 * 1024) // 300 KiB synthetic match replay
	chunks, meta := cosmoswasm.ChunkBlob(replay, 64*1024)
	res, err := bc.SubmitBatch(ctx, chunks)
	if err != nil {
		log.Fatalf("submit replay batch: %v", err)
	}
	log.Printf("[replay] %d bytes → %d chunks at height=%d, root=%s", len(replay), res.Count, res.Height, short(res.Root))

	// Prove chunk #1 belongs to the batch root.
	proof, _ := cosmoswasm.BuildMerkleProof(res.Commitments, 1)
	if err := cosmoswasm.VerifyMerkleProof(proof); err != nil {
		log.Fatalf("merkle proof: %v", err)
	}
	log.Printf("[proof] chunk #1 ∈ replay root: %d-step proof, root match=%v", len(proof.Path), proof.Root == res.Root)

	// Reassemble the replay from Celestia (verifies OriginalHash).
	parts := make([][]byte, len(res.Commitments))
	for i, com := range res.Commitments {
		p, err := bc.RetrieveBlob(ctx, res.Height, com)
		if err != nil {
			log.Fatalf("retrieve chunk %d: %v", i, err)
		}
		parts[i] = p
	}
	reassembled, err := cosmoswasm.ReassembleChunks(parts, meta)
	if err != nil {
		log.Fatalf("reassemble replay: %v", err)
	}
	log.Printf("[replay] reassembled %d bytes, matches original=%v", len(reassembled), bytes.Equal(reassembled, replay))

	if contractAddr != "" {
		recordReplay(ctx, client, signer, contractAddr, matchID, res, bc.Namespace())
	}

	// ── 3. Finish the match + read everything back from the contract ─────────
	if contractAddr != "" {
		finishMatch(ctx, client, signer, contractAddr, matchID)
		readBack(ctx, client, bc, contractAddr, matchID)
	}

	log.Printf("done.")
}

// ── Deploy ───────────────────────────────────────────────────────────────────

// deployContract stores the wasm and instantiates it, returning the contract
// address. Fails fast on any error.
func deployContract(ctx context.Context, client *cosmoswasm.Client, signer *cosmoswasm.Signer, wasmPath string) string {
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		log.Fatalf("read wasm %s: %v", wasmPath, err)
	}

	storeTx, err := cosmoswasm.BuildSignedStoreTx(ctx, client, signer, wasm)
	if err != nil {
		log.Fatalf("build store tx: %v", err)
	}
	storeRes := submitWait(ctx, client, storeTx, "store_code")
	codeIDText := findEventValue(storeRes.Events, "code_id")
	codeID, err := strconv.ParseUint(codeIDText, 10, 64)
	if err != nil {
		log.Fatalf("parse code_id (%q): %v", codeIDText, err)
	}
	log.Printf("[deploy] stored code_id=%d (%d bytes)", codeID, len(wasm))

	initTx, err := cosmoswasm.BuildSignedInstantiateTx(ctx, client, signer, cosmoswasm.InstantiateTxRequest{
		CodeID: codeID,
		Label:  "telemetry-registry",
		Msg:    map[string]any{}, // default max_players_per_match
	})
	if err != nil {
		log.Fatalf("build instantiate tx: %v", err)
	}
	initRes := submitWait(ctx, client, initTx, "instantiate")
	addr := firstNonEmpty(
		findEventValue(initRes.Events, "_contract_address"),
		findEventValue(initRes.Events, "contract_address"),
	)
	if addr == "" {
		log.Fatalf("cannot parse contract address from instantiate events")
	}
	return addr
}

// ── On-chain game flow ───────────────────────────────────────────────────────

func setupMatch(ctx context.Context, client *cosmoswasm.Client, signer *cosmoswasm.Signer, contract string) uint64 {
	registerPlayer(ctx, client, signer, contract, "demo-bot")
	res := execGame(ctx, client, signer, contract, map[string]any{"create_match": map[string]any{}})
	idStr := attrFromResult(res, "match_id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		log.Fatalf("parse match_id from event (%q): %v", idStr, err)
	}
	execGame(ctx, client, signer, contract, map[string]any{"start_match": map[string]any{"match_id": id}})
	return id
}

// registerPlayer registers the sender, tolerating the "already registered" case
// so the example can be re-run against an existing contract.
func registerPlayer(ctx context.Context, client *cosmoswasm.Client, signer *cosmoswasm.Signer, contract, handle string) {
	tx, err := cosmoswasm.BuildSignedExecuteTx(ctx, client, signer, cosmoswasm.ExecuteTxRequest{
		Contract: contract,
		Msg:      map[string]any{"register_player": map[string]any{"handle": handle}},
	})
	if err != nil {
		log.Fatalf("build register_player: %v", err)
	}
	sub, err := client.SubmitTxBytes(ctx, tx)
	if err != nil {
		log.Fatalf("submit register_player: %v", err)
	}
	res, err := client.WaitTxResult(ctx, sub.Hash, time.Second)
	if err != nil {
		log.Fatalf("wait register_player: %v", err)
	}
	if res.Code != 0 {
		if strings.Contains(res.Log, "already registered") {
			log.Printf("[game] player already registered — continuing")
			return
		}
		log.Fatalf("register_player failed (code=%d): %s", res.Code, res.Log)
	}
}

func recordTelemetry(ctx context.Context, client *cosmoswasm.Client, signer *cosmoswasm.Signer, contract string, matchID uint64, sub *cosmoswasm.BlobSubmitResponse) {
	execGame(ctx, client, signer, contract, map[string]any{
		"record_telemetry": map[string]any{
			"match_id":   matchID,
			"commitment": sub.Commitment,
			"height":     sub.Height,
			"namespace":  sub.Namespace,
		},
	})
}

func recordReplay(ctx context.Context, client *cosmoswasm.Client, signer *cosmoswasm.Signer, contract string, matchID uint64, res *cosmoswasm.BlobBatchResponse, nsHex string) {
	execGame(ctx, client, signer, contract, map[string]any{
		"record_replay": map[string]any{
			"match_id":  matchID,
			"root":      res.Root,
			"height":    res.Height,
			"count":     res.Count,
			"namespace": nsHex,
		},
	})
}

func finishMatch(ctx context.Context, client *cosmoswasm.Client, signer *cosmoswasm.Signer, contract string, matchID uint64) {
	sender := signer.Address()
	execGame(ctx, client, signer, contract, map[string]any{
		"finish_match": map[string]any{
			"match_id": matchID,
			"winner":   sender,
			"scores":   []any{map[string]any{"player": sender, "score": 100}},
		},
	})
	log.Printf("[game] match #%d finished (winner=%s, score=100)", matchID, sender)
}

// readBack queries the contract for the match's blob pointers, then pulls the
// actual telemetry from Celestia using one of those pointers — closing the loop.
func readBack(ctx context.Context, client *cosmoswasm.Client, bc *cosmoswasm.BlobClient, contract string, matchID uint64) {
	tele, err := client.QuerySmart(ctx, contract, map[string]any{
		"match_telemetry": map[string]any{"match_id": matchID},
	})
	if err != nil {
		log.Fatalf("query match_telemetry: %v", err)
	}
	framesAny, _ := tele["frames"].([]any)
	log.Printf("[read] match #%d has %d on-chain telemetry pointers; replay_root=%v",
		matchID, len(framesAny), tele["replay_root"])

	if len(framesAny) > 0 {
		if f0, ok := framesAny[0].(map[string]any); ok {
			com, _ := f0["commitment"].(string)
			h, _ := f0["height"].(float64)
			if data, err := bc.RetrieveBlob(ctx, uint64(h), com); err == nil {
				restored, _ := cosmoswasm.MaybeDecompress(data)
				log.Printf("[read] pulled frame from on-chain pointer (height=%d): %d bytes", uint64(h), len(restored))
			}
		}
	}

	if lb, err := client.QuerySmart(ctx, contract, map[string]any{"leaderboard": map[string]any{}}); err == nil {
		log.Printf("[read] leaderboard: %v", lb["players"])
	}
	if stats, err := client.QuerySmart(ctx, contract, map[string]any{"stats": map[string]any{}}); err == nil {
		log.Printf("[read] stats: %v", stats)
	}
}

// execGame builds an unsigned execute tx, submits it, waits, fails on non-zero code.
func execGame(ctx context.Context, client *cosmoswasm.Client, signer *cosmoswasm.Signer, contract string, msg map[string]any) *cosmoswasm.TxExecutionResult {
	tx, err := cosmoswasm.BuildSignedExecuteTx(ctx, client, signer, cosmoswasm.ExecuteTxRequest{Contract: contract, Msg: msg})
	if err != nil {
		log.Fatalf("build execute %v: %v", keyOf(msg), err)
	}
	return submitWait(ctx, client, tx, keyOf(msg))
}

func submitWait(ctx context.Context, client *cosmoswasm.Client, tx []byte, label string) *cosmoswasm.TxExecutionResult {
	sub, err := client.SubmitTxBytes(ctx, tx)
	if err != nil {
		log.Fatalf("submit %s: %v", label, err)
	}
	res, err := client.WaitTxResult(ctx, sub.Hash, time.Second)
	if err != nil {
		log.Fatalf("wait %s: %v", label, err)
	}
	if res.Code != 0 {
		log.Fatalf("tx %s failed (code=%d): %s", label, res.Code, res.Log)
	}
	return res
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func mustClient(execURL string) *cosmoswasm.Client {
	c, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{ExecURL: execURL, Timeout: 20 * time.Second})
	if err != nil {
		log.Fatalf("init exec client: %v", err)
	}
	return c
}

// mustSigner builds a Signer from the private key in the environment (the
// project .env's ROLLUP_PRIVATE_KEY_HEX). The chain ID is taken from the live
// node /status (falling back to COSMOS_EXEC_CHAIN_ID), and the fee is derived
// from COSMOS_EXEC_MIN_GAS_PRICE/COSMOS_EXEC_GAS_DENOM so signed txs pass the
// ante fee check when the chain enforces a min gas price.
func mustSigner(ctx context.Context, client *cosmoswasm.Client) *cosmoswasm.Signer {
	privHex := firstNonEmpty(
		os.Getenv("COSMOS_EXEC_TREASURY_PRIVKEY_HEX"),
	)
	if privHex == "" {
		log.Fatalf("no signing key: set COSMOS_EXEC_TREASURY_PRIVKEY_HEX in your env or project .env")
	}

	chainID := strings.TrimSpace(os.Getenv("COSMOS_EXEC_CHAIN_ID"))
	if st, err := client.Status(ctx); err == nil && strings.TrimSpace(st.ChainID) != "" {
		chainID = strings.TrimSpace(st.ChainID) // live chain id wins over env.
	}

	signer, err := cosmoswasm.NewSignerFromHex(privHex, chainID)
	if err != nil {
		log.Fatalf("build signer: %v", err)
	}
	signer.WithGasLimit(signGasLimit)
	if fee := computeFee(signGasLimit); fee != nil {
		signer.WithFee(fee)
	}
	log.Printf("[signer] address=%s chain=%s", signer.Address(), chainID)
	return signer
}

// computeFee returns ceil(gas * minGasPrice) in the gas denom, or nil when the
// chain isn't enforcing a min gas price (no denom/price in env) so the tx is
// submitted with a 0 fee.
func computeFee(gas uint64) sdk.Coins {
	denom := strings.TrimSpace(os.Getenv("COSMOS_EXEC_GAS_DENOM"))
	priceStr := strings.TrimSpace(os.Getenv("COSMOS_EXEC_MIN_GAS_PRICE"))
	if denom == "" || priceStr == "" {
		return nil
	}
	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil || price <= 0 {
		return nil
	}
	amt := int64(math.Ceil(float64(gas) * price))
	if amt <= 0 {
		amt = 1
	}
	return sdk.NewCoins(sdk.NewInt64Coin(denom, amt))
}

func attrFromResult(res *cosmoswasm.TxExecutionResult, key string) string {
	return findEventValue(res.Events, key)
}

func findEventValue(events []cosmoswasm.TxEvent, key string) string {
	for _, ev := range events {
		for _, a := range ev.Attributes {
			if a.Key == key && strings.TrimSpace(a.Value) != "" {
				return strings.TrimSpace(a.Value)
			}
		}
	}
	return ""
}

func keyOf(msg map[string]any) string {
	for k := range msg {
		return k
	}
	return "?"
}

func short(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:8] + "…" + s[len(s)-4:]
}

func defaultWasmPath(projectRoot string) string {
	if projectRoot == "" {
		return ""
	}
	return filepath.Join(projectRoot,
		"apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry/contract/artifacts/telemetry_registry.wasm")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// findProjectRoot walks up from cwd until it finds scripts/run-cosmos-wasm-nodes.go.
func findProjectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	cur := wd
	for {
		if fileExists(filepath.Join(cur, "scripts", "run-cosmos-wasm-nodes.go")) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// loadDotEnv loads KEY=VALUE pairs from a .env file without overwriting vars
// already set in the shell. Minimal stdlib parser (no external deps).
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if strings.HasPrefix(val, "\"") || strings.HasPrefix(val, "'") {
			val = strings.Trim(val, "\"'")
		} else if i := strings.Index(val, " #"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		if key != "" {
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, val)
			}
		}
	}
	return sc.Err()
}

func makeFrame(match string, tick int) telemetryFrame {
	return telemetryFrame{
		Match: match,
		Tick:  tick,
		TS:    time.Now().UnixMilli(),
		Players: []playerSample{
			{ID: "p1", Pos: [3]float64{float64(tick) * 1.5, 0, 12.3}, HP: 100 - tick},
			{ID: "p2", Pos: [3]float64{-float64(tick), 4.2, 9.0}, HP: 87},
			{ID: "p3", Pos: [3]float64{float64(tick) * 0.5, 1.1, -3.7}, HP: 64},
		},
	}
}

func makeReplay(n int) []byte {
	buf := make([]byte, 0, n)
	for len(buf) < n {
		buf = append(buf, []byte("tick:move(p1,1.5,0,12.3);move(p2,-3,4,9);shoot(p1,p3);")...)
	}
	return buf[:n]
}

// envFloat reads a float64 from env, returning def when unset or unparseable.
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
