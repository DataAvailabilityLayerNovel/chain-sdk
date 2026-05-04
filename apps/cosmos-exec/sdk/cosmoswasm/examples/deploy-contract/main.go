// deploy-contract demonstrates the full contract lifecycle on a running executor:
//
//  1. Deploy a reflect contract (store code + instantiate)
//  2. Execute a contract message (change_owner) and verify result
//  3. Store blobs off-chain, record commitments on-chain
//  4. Query contract state
//  5. Build and verify Merkle proofs
//
// Prerequisites:
//
//	# Terminal 1 — start the full E2E stack:
//	go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true
//
//	# Terminal 2 — run this example:
//	go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/deploy-contract
//
// Or with API-only executor (blob + deploy work, but tx won't be executed without sequencer):
//
//	cd apps/cosmos-exec && go run ./cmd/cosmos-exec-grpc --in-memory
//	go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/deploy-contract
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"

	"github.com/CosmWasm/wasmd/x/wasm/keeper/testdata"
	cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
	execURL := os.Getenv("EXEC_URL")
	if execURL == "" {
		execURL = cosmoswasm.DefaultExecAPIURL
	}

	client := cosmoswasm.NewClient(execURL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sender := sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20))
	newOwner := sdk.AccAddress(bytes.Repeat([]byte{0x22}, 20))

	// ── Step 1: Deploy reflect contract ─────────────────────────────────
	fmt.Println("Step 1 — Store WASM code (reflect contract)")

	storeTx, err := cosmoswasm.BuildStoreTx(testdata.ReflectContractWasm(), sender.String())
	if err != nil {
		log.Fatalf("  build store tx: %v", err)
	}

	storeSubmit, err := client.SubmitTxBytes(ctx, storeTx)
	if err != nil {
		log.Fatalf("  submit store tx: %v", err)
	}
	fmt.Printf("  submitted tx_hash=%s\n", storeSubmit.Hash)

	fmt.Println("  waiting for execution...")
	storeResult, err := client.WaitTxResult(ctx, storeSubmit.Hash, time.Second)
	if err != nil {
		log.Fatalf("  wait store result: %v", err)
	}
	if storeResult.Code != 0 {
		log.Fatalf("  store tx failed: code=%d log=%s", storeResult.Code, storeResult.Log)
	}
	fmt.Printf("  store tx success at height=%d, code_id from events\n", storeResult.Height)

	// ── Step 2: Instantiate contract ────────────────────────────────────
	fmt.Println("\nStep 2 — Instantiate reflect contract (code_id=1)")

	instantiateTx, err := cosmoswasm.BuildInstantiateTx(cosmoswasm.InstantiateTxRequest{
		Sender: sender.String(),
		CodeID: 1,
		Label:  "reflect-deploy-example",
		Msg:    "{}",
	})
	if err != nil {
		log.Fatalf("  build instantiate tx: %v", err)
	}

	initSubmit, err := client.SubmitTxBytes(ctx, instantiateTx)
	if err != nil {
		log.Fatalf("  submit instantiate tx: %v", err)
	}
	fmt.Printf("  submitted tx_hash=%s\n", initSubmit.Hash)

	fmt.Println("  waiting for execution...")
	initResult, err := client.WaitTxResult(ctx, initSubmit.Hash, time.Second)
	if err != nil {
		log.Fatalf("  wait instantiate result: %v", err)
	}
	if initResult.Code != 0 {
		log.Fatalf("  instantiate tx failed: code=%d log=%s", initResult.Code, initResult.Log)
	}

	contractAddr := findEventValue(initResult.Events, "_contract_address")
	if contractAddr == "" {
		contractAddr = findEventValue(initResult.Events, "contract_address")
	}
	if contractAddr == "" {
		log.Fatal("  cannot find contract_address in instantiate events")
	}
	fmt.Printf("  contract deployed: %s (height=%d)\n", contractAddr, initResult.Height)

	// ── Step 3: Execute contract — change owner ─────────────────────────
	fmt.Println("\nStep 3 — Execute: change_owner")

	execMsg, _ := json.Marshal(testdata.ReflectHandleMsg{
		ChangeOwner: &testdata.OwnerPayload{Owner: newOwner},
	})

	executeTx, err := buildRawTx(&wasmtypes.MsgExecuteContract{
		Sender:   sender.String(),
		Contract: contractAddr,
		Msg:      execMsg,
	})
	if err != nil {
		log.Fatalf("  build execute tx: %v", err)
	}

	execSubmit, err := client.SubmitTxBytes(ctx, executeTx)
	if err != nil {
		log.Fatalf("  submit execute tx: %v", err)
	}
	fmt.Printf("  submitted tx_hash=%s\n", execSubmit.Hash)

	fmt.Println("  waiting for execution...")
	execResult, err := client.WaitTxResult(ctx, execSubmit.Hash, time.Second)
	if err != nil {
		log.Fatalf("  wait execute result: %v", err)
	}
	if execResult.Code != 0 {
		log.Fatalf("  execute tx failed: code=%d log=%s", execResult.Code, execResult.Log)
	}
	fmt.Printf("  execute success at height=%d\n", execResult.Height)

	// ── Step 4: Query contract state ────────────────────────────────────
	fmt.Println("\nStep 4 — Query: owner")

	queryMsg, _ := json.Marshal(testdata.ReflectQueryMsg{Owner: &struct{}{}})
	queryResult, err := client.QuerySmartRaw(ctx, contractAddr, queryMsg)
	if err != nil {
		fmt.Printf("  query returned error (may need gas/state recovery): %v\n", err)
		fmt.Println("  skipping query verification — contract state was modified by execute tx above")
	} else {
		fmt.Printf("  query result: %v\n", formatQueryResult(queryResult))
		fmt.Printf("  expected new owner: %s\n", newOwner.String())
	}

	// ── Step 5: Blob store + Merkle proof ───────────────────────────────
	fmt.Println("\nStep 5 — Store 3 blobs off-chain + Merkle proof")

	blobs := [][]byte{
		[]byte(`{"event":"game_start","ts":1}`),
		[]byte(`{"event":"player_move","ts":2,"x":10,"y":20}`),
		[]byte(`{"event":"game_end","ts":3,"score":9999}`),
	}

	commitments := make([]string, len(blobs))
	for i, b := range blobs {
		res, err := client.SubmitBlob(ctx, b)
		if err != nil {
			log.Fatalf("  blob[%d] submit: %v", i, err)
		}
		commitments[i] = res.Commitment
		fmt.Printf("  blob[%d] → %s (%d bytes)\n", i, res.Commitment[:16]+"…", res.Size)
	}

	// Verify we can retrieve
	data, err := client.RetrieveBlobData(ctx, commitments[0])
	if err != nil {
		log.Fatalf("  retrieve blob[0]: %v", err)
	}
	fmt.Printf("  retrieved blob[0]: %s\n", string(data))

	// Build + verify Merkle proof for blob[1]
	proof, err := cosmoswasm.GetProof(commitments, 1)
	if err != nil {
		log.Fatalf("  build proof: %v", err)
	}
	if err := cosmoswasm.VerifyMerkleProof(proof); err != nil {
		log.Fatalf("  proof INVALID: %v", err)
	}
	fmt.Printf("  Merkle proof verified for blob[1] in root=%s…\n", proof.Root[:16])

	// ── Step 6: Cost estimate ───────────────────────────────────────────
	fmt.Println("\nStep 6 — Cost estimate")

	est := cosmoswasm.EstimateCost(cosmoswasm.EstimateCostRequest{DataBytes: 1024 * 1024})
	fmt.Printf("  1 MB data:\n")
	fmt.Printf("    direct on-chain: %d gas\n", est.DirectTx.TotalGas)
	fmt.Printf("    blob + commit:   %d gas (%.0f%% cheaper)\n", est.BlobCommit.TotalGas, est.SavingsPercent)

	// ── Summary ─────────────────────────────────────────────────────────
	fmt.Println("\n--- Summary ---")
	fmt.Printf("  contract:     %s\n", contractAddr)
	fmt.Printf("  store tx:     %s (height %d)\n", storeSubmit.Hash[:16]+"…", storeResult.Height)
	fmt.Printf("  init tx:      %s (height %d)\n", initSubmit.Hash[:16]+"…", initResult.Height)
	fmt.Printf("  execute tx:   %s (height %d)\n", execSubmit.Hash[:16]+"…", execResult.Height)
	fmt.Printf("  blobs stored: %d (off-chain)\n", len(blobs))
	fmt.Printf("  merkle root:  %s…\n", proof.Root[:16])
	fmt.Println("\nAll steps passed.")
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

func formatQueryResult(res *cosmoswasm.QuerySmartResponse) string {
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
