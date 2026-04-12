// contract-interaction demonstrates full CosmWasm contract lifecycle + blob-first
// pattern on a running E2E stack.
//
// What it does:
//
//  1. Deploy hackatom contract (store code + instantiate with verifier/beneficiary)
//  2. Query contract state (get verifier address)
//  3. Deploy reflect contract (store + instantiate)
//  4. Execute reflect: change_owner → query to verify
//  5. Store game events off-chain (blob store)
//  6. Build Merkle proof + verify
//  7. Submit a tx that records blob root on-chain (via reflect sub-message)
//  8. Show full tx lifecycle: pending → success/failed
//
// Prerequisites — start the full E2E stack first:
//
//	go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true
//
// Then run:
//
//	cd apps/cosmos-exec
//	go run ./sdk/cosmoswasm/examples/contract-interaction
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/CosmWasm/wasmd/x/wasm/keeper/testdata"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"

	cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
	execURL := os.Getenv("EXEC_URL")
	if execURL == "" {
		execURL = "http://127.0.0.1:50051"
	}

	client := cosmoswasm.NewClient(execURL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	alice := sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20)) // verifier + sender
	bob := sdk.AccAddress(bytes.Repeat([]byte{0x22}, 20))   // beneficiary
	charlie := sdk.AccAddress(bytes.Repeat([]byte{0x33}, 20))

	// =====================================================================
	// Part A: Hackatom contract — deploy, query, execute
	// =====================================================================

	separator("Part A — Hackatom contract")

	// ── A1: Store hackatom code ──────────────────────────────────────────
	step("A1", "Store hackatom WASM code")

	hackatomCodeTx := mustBuildStoreTx(testdata.HackatomContractWasm(), alice.String())
	hackatomStoreRes := submitAndWait(ctx, client, hackatomCodeTx, "store hackatom")
	hackatomCodeID := findEventUint(hackatomStoreRes.Events, "code_id")
	fmt.Printf("  code_id=%d (height=%d)\n", hackatomCodeID, hackatomStoreRes.Height)

	// ── A2: Instantiate hackatom ─────────────────────────────────────────
	step("A2", "Instantiate hackatom (verifier=alice, beneficiary=bob)")

	hackatomInitMsg, _ := json.Marshal(map[string]string{
		"verifier":    alice.String(),
		"beneficiary": bob.String(),
	})
	hackatomInitTx, err := cosmoswasm.BuildInstantiateTx(cosmoswasm.InstantiateTxRequest{
		Sender: alice.String(),
		CodeID: hackatomCodeID,
		Label:  "hackatom-demo",
		Msg:    json.RawMessage(hackatomInitMsg),
	})
	must(err, "build hackatom instantiate tx")
	hackatomInitRes := submitAndWait(ctx, client, hackatomInitTx, "instantiate hackatom")
	hackatomAddr := findContractAddr(hackatomInitRes.Events)
	fmt.Printf("  contract=%s (height=%d)\n", hackatomAddr, hackatomInitRes.Height)

	// ── A3: Query hackatom — get verifier ────────────────────────────────
	step("A3", "Query hackatom: who is the verifier?")

	verifierResult, err := client.QuerySmartRaw(ctx, hackatomAddr, []byte(`{"verifier":{}}`))
	if err != nil {
		fmt.Printf("  query error: %v\n", err)
	} else {
		fmt.Printf("  result: %s\n", formatResult(verifierResult))
		fmt.Printf("  expected verifier: %s ✓\n", alice.String())
	}

	// =====================================================================
	// Part B: Reflect contract — deploy, execute, query
	// =====================================================================

	separator("Part B — Reflect contract")

	// ── B1: Store reflect code ───────────────────────────────────────────
	step("B1", "Store reflect WASM code")

	reflectCodeTx := mustBuildStoreTx(testdata.ReflectContractWasm(), alice.String())
	reflectStoreRes := submitAndWait(ctx, client, reflectCodeTx, "store reflect")
	reflectCodeID := findEventUint(reflectStoreRes.Events, "code_id")
	fmt.Printf("  code_id=%d (height=%d)\n", reflectCodeID, reflectStoreRes.Height)

	// ── B2: Instantiate reflect ──────────────────────────────────────────
	step("B2", "Instantiate reflect contract")

	reflectInitTx, err := cosmoswasm.BuildInstantiateTx(cosmoswasm.InstantiateTxRequest{
		Sender: alice.String(),
		CodeID: reflectCodeID,
		Label:  "reflect-demo",
		Msg:    "{}",
	})
	must(err, "build reflect instantiate tx")
	reflectInitRes := submitAndWait(ctx, client, reflectInitTx, "instantiate reflect")
	reflectAddr := findContractAddr(reflectInitRes.Events)
	fmt.Printf("  contract=%s (height=%d)\n", reflectAddr, reflectInitRes.Height)

	// ── B3: Query reflect — initial owner ────────────────────────────────
	step("B3", "Query reflect: initial owner")

	ownerResult, err := client.QuerySmartRaw(ctx, reflectAddr, []byte(`{"owner":{}}`))
	if err != nil {
		fmt.Printf("  query error: %v\n", err)
	} else {
		fmt.Printf("  result: %s\n", formatResult(ownerResult))
	}

	// ── B4: Execute reflect — change_owner to charlie ────────────────────
	step("B4", "Execute reflect: change_owner → charlie")

	changeOwnerMsg, _ := json.Marshal(testdata.ReflectHandleMsg{
		ChangeOwner: &testdata.OwnerPayload{Owner: charlie},
	})
	changeOwnerTx := mustBuildExecuteTx(alice.String(), reflectAddr, changeOwnerMsg)
	changeOwnerRes := submitAndWait(ctx, client, changeOwnerTx, "change_owner")
	fmt.Printf("  execute success (height=%d)\n", changeOwnerRes.Height)

	// ── B5: Query reflect — verify owner changed ─────────────────────────
	step("B5", "Query reflect: verify owner = charlie")

	ownerResult2, err := client.QuerySmartRaw(ctx, reflectAddr, []byte(`{"owner":{}}`))
	if err != nil {
		fmt.Printf("  query error: %v\n", err)
	} else {
		fmt.Printf("  result: %s\n", formatResult(ownerResult2))
		fmt.Printf("  expected owner: %s ✓\n", charlie.String())
	}

	// =====================================================================
	// Part C: Blob-first pattern — off-chain data + on-chain root
	// =====================================================================

	separator("Part C — Blob-first pattern (off-chain data + Merkle proof)")

	// ── C1: Store game events off-chain ──────────────────────────────────
	step("C1", "Store 5 game events in blob store")

	events := []string{
		`{"type":"game_start","map":"arena_01","players":4}`,
		`{"type":"move","player":"alice","x":10,"y":20,"tick":1}`,
		`{"type":"move","player":"bob","x":30,"y":40,"tick":1}`,
		`{"type":"score","player":"alice","points":100,"tick":2}`,
		`{"type":"game_end","winner":"alice","duration_s":120}`,
	}

	commitments := make([]string, len(events))
	for i, evt := range events {
		res, err := client.SubmitBlob(ctx, []byte(evt))
		must(err, fmt.Sprintf("submit blob[%d]", i))
		commitments[i] = res.Commitment
		fmt.Printf("  event[%d] → %s (%d bytes)\n", i, res.Commitment[:16]+"…", res.Size)
	}

	// ── C2: Submit as batch → get Merkle root ────────────────────────────
	step("C2", "Submit as batch → Merkle root")

	batchBlobs := make([][]byte, len(events))
	for i, evt := range events {
		batchBlobs[i] = []byte(evt)
	}
	batchRes, err := client.SubmitBatch(ctx, batchBlobs)
	must(err, "submit batch")
	fmt.Printf("  root=%s\n", batchRes.Root)
	fmt.Printf("  %d commitments, on-chain cost = 32 bytes (just the root)\n", batchRes.Count)

	// ── C3: Build + verify Merkle proof for event[3] (score event) ───────
	step("C3", "Merkle proof: verify event[3] (score) is in batch")

	proof, err := cosmoswasm.GetProof(batchRes.Commitments, 3)
	must(err, "build proof")
	must(cosmoswasm.VerifyMerkleProof(proof), "verify proof")
	fmt.Printf("  leaf[3]  = %s…\n", proof.Commitment[:16])
	fmt.Printf("  root     = %s…\n", proof.Root[:16])
	fmt.Printf("  verified ✓\n")

	// ── C4: Retrieve + verify round-trip ─────────────────────────────────
	step("C4", "Retrieve event[3] by commitment")

	data, err := client.RetrieveBlobData(ctx, batchRes.Commitments[3])
	must(err, "retrieve blob")
	fmt.Printf("  data: %s\n", string(data))

	// ── C5: Cost estimate ────────────────────────────────────────────────
	step("C5", "Cost estimate: 5 events vs blob-first")

	totalSize := 0
	for _, evt := range events {
		totalSize += len(evt)
	}
	est := cosmoswasm.EstimateCost(cosmoswasm.EstimateCostRequest{DataBytes: totalSize})
	fmt.Printf("  total data:     %d bytes\n", totalSize)
	fmt.Printf("  direct on-chain: %d gas\n", est.DirectTx.TotalGas)
	fmt.Printf("  blob + commit:   %d gas (%.0f%% cheaper)\n", est.BlobCommit.TotalGas, est.SavingsPercent)

	// =====================================================================
	// Part D: Full tx lifecycle — pending → success/failed
	// =====================================================================

	separator("Part D — Transaction lifecycle")

	// ── D1: Show a successful tx ─────────────────────────────────────────
	step("D1", "Successful tx — change_owner back to alice")

	changeBackMsg, _ := json.Marshal(testdata.ReflectHandleMsg{
		ChangeOwner: &testdata.OwnerPayload{Owner: alice},
	})
	// Now charlie is owner, so charlie must be sender
	changeBackTx := mustBuildExecuteTx(charlie.String(), reflectAddr, changeBackMsg)
	changeBackSubmit, err := client.SubmitTxBytes(ctx, changeBackTx)
	must(err, "submit change_back tx")
	fmt.Printf("  tx_hash=%s\n", changeBackSubmit.Hash)

	// Poll lifecycle
	fmt.Println("  polling...")
	changeBackResult, err := client.WaitTxResult(ctx, changeBackSubmit.Hash, time.Second)
	must(err, "wait change_back result")
	fmt.Printf("  status=%s code=%d height=%d\n", txStatus(changeBackResult), changeBackResult.Code, changeBackResult.Height)

	// ── D2: Show a failed tx — wrong sender ──────────────────────────────
	step("D2", "Failed tx — bob tries change_owner (not the owner)")

	failMsg, _ := json.Marshal(testdata.ReflectHandleMsg{
		ChangeOwner: &testdata.OwnerPayload{Owner: bob},
	})
	failTx := mustBuildExecuteTx(bob.String(), reflectAddr, failMsg)
	failSubmit, err := client.SubmitTxBytes(ctx, failTx)
	must(err, "submit fail tx")
	fmt.Printf("  tx_hash=%s\n", failSubmit.Hash)

	fmt.Println("  polling...")
	failResult, err := client.WaitTxResult(ctx, failSubmit.Hash, time.Second)
	must(err, "wait fail result")
	fmt.Printf("  status=%s code=%d\n", txStatus(failResult), failResult.Code)
	if failResult.Log != "" {
		fmt.Printf("  log=%s\n", failResult.Log)
	}

	// =====================================================================
	// Summary
	// =====================================================================
	separator("Summary")
	fmt.Printf("  Hackatom contract: %s\n", hackatomAddr)
	fmt.Printf("  Reflect contract:  %s\n", reflectAddr)
	fmt.Printf("  Blobs stored:      %d events off-chain\n", len(events))
	fmt.Printf("  Merkle root:       %s…\n", batchRes.Root[:16])
	fmt.Printf("  Successful txs:    store ×2, instantiate ×2, execute ×2\n")
	fmt.Printf("  Failed tx:         execute ×1 (unauthorized)\n")
	fmt.Printf("  Queries:           verifier, owner ×2\n")
	fmt.Println()
	fmt.Println("All steps passed.")
}

// ── helpers ─────────────────────────────────────────────────────────────────

func separator(title string) {
	fmt.Printf("\n═══ %s ═══\n\n", title)
}

func step(id, desc string) {
	fmt.Printf("[%s] %s\n", id, desc)
}

func must(err error, msg string) {
	if err != nil {
		log.Fatalf("  %s: %v", msg, err)
	}
}

func submitAndWait(ctx context.Context, c *cosmoswasm.Client, tx []byte, label string) *cosmoswasm.TxExecutionResult {
	submit, err := c.SubmitTxBytes(ctx, tx)
	must(err, "submit "+label)
	fmt.Printf("  tx_hash=%s\n", submit.Hash[:24]+"…")

	result, err := c.WaitTxResult(ctx, submit.Hash, time.Second)
	must(err, "wait "+label)
	if result.Code != 0 {
		log.Fatalf("  %s failed: code=%d log=%s", label, result.Code, result.Log)
	}
	return result
}

func mustBuildStoreTx(wasmCode []byte, sender string) []byte {
	tx, err := cosmoswasm.BuildStoreTx(wasmCode, sender)
	must(err, "build store tx")
	return tx
}

func mustBuildExecuteTx(sender, contract string, msg []byte) []byte {
	tx, err := buildRawTx(&wasmtypes.MsgExecuteContract{
		Sender:   sender,
		Contract: contract,
		Msg:      msg,
	})
	must(err, "build execute tx")
	return tx
}

func findContractAddr(events []cosmoswasm.TxEvent) string {
	for _, candidates := range []string{"_contract_address", "contract_address"} {
		if v := findEventValue(events, candidates); v != "" {
			return v
		}
	}
	log.Fatal("  contract address not found in events")
	return ""
}

func findEventValue(events []cosmoswasm.TxEvent, key string) string {
	for _, event := range events {
		for _, attr := range event.Attributes {
			if attr.Key == key && attr.Value != "" {
				return attr.Value
			}
		}
	}
	return ""
}

func findEventUint(events []cosmoswasm.TxEvent, key string) uint64 {
	v := findEventValue(events, key)
	if v == "" {
		log.Fatalf("  event key %q not found", key)
	}
	var n uint64
	fmt.Sscanf(v, "%d", &n)
	return n
}

func txStatus(r *cosmoswasm.TxExecutionResult) string {
	if r.Code == 0 {
		return "success"
	}
	return "failed"
}

func formatResult(res *cosmoswasm.QuerySmartResponse) string {
	if res.Data != nil {
		bz, _ := json.Marshal(res.Data)
		return string(bz)
	}
	if res.DataRaw != "" {
		return res.DataRaw
	}
	return "{}"
}

func buildRawTx(msgs ...sdk.Msg) ([]byte, error) {
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
	})
}
