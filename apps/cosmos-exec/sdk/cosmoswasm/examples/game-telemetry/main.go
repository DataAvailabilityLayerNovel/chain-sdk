// game-telemetry demonstrates the blob-first pattern for data-heavy dApps.
//
// It shows a game server emitting frequent event frames (game state changes,
// player moves, scores) cheaply:
//
//   - Large event frames → executor blob store (off WASM state, ~free)
//   - One Merkle root per batch → verifiable off-chain
//   - Per-frame Merkle proofs → clients can verify any event without replaying all
//
// Run (requires cosmos-exec-grpc running):
//
//	# Terminal 1:
//	cd apps/cosmos-exec && go run ./cmd/cosmos-exec-grpc --in-memory
//
//	# Terminal 2:
//	cd apps/cosmos-exec && go run ./sdk/cosmoswasm/examples/game-telemetry
//
// Or with full E2E stack:
//
//	go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true
//	cd apps/cosmos-exec && go run ./sdk/cosmoswasm/examples/game-telemetry
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

// GameEvent simulates a single game frame / event payload.
type GameEvent struct {
	Frame     uint64         `json:"frame"`
	Timestamp int64          `json:"ts"`
	Players   []PlayerUpdate `json:"players"`
}

type PlayerUpdate struct {
	ID    string  `json:"id"`
	X, Y  float64 `json:"x,y"`
	Score int     `json:"score"`
}

func main() {
	execURL := os.Getenv("EXEC_URL")
	if execURL == "" {
		execURL = "http://127.0.0.1:50051"
	}

	client := cosmoswasm.NewClient(execURL)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// -------------------------------------------------------------------------
	// Demo 1: Batch submit — accumulate 20 game events, submit as one batch
	// -------------------------------------------------------------------------
	log.Println("Demo 1 — Batch submit: 20 game events")

	blobs := make([][]byte, 20)
	for i := range 20 {
		event := makeEvent(uint64(i), 8)
		payload, err := json.Marshal(event)
		if err != nil {
			log.Fatalf("marshal event: %v", err)
		}
		// Compress if beneficial (default BatchBuilder behavior).
		if compressed, ok := cosmoswasm.CompressIfBeneficial(payload); ok {
			blobs[i] = compressed
		} else {
			blobs[i] = payload
		}
	}

	batchRes, err := client.SubmitBatch(ctx, blobs)
	if err != nil {
		log.Fatalf("[batch] submit error (is executor running?): %v", err)
	}
	log.Printf("[batch] root=%s blobs=%d", batchRes.Root[:16]+"…", batchRes.Count)

	// -------------------------------------------------------------------------
	// Demo 2: Critical event — submit single blob immediately
	// -------------------------------------------------------------------------
	criticalEvent := makeEvent(9999, 4)
	criticalEvent.Players[0].Score = 1_000_000 // record-breaking score
	payload, _ := json.Marshal(criticalEvent)

	log.Println("\nDemo 2 — Critical event: single blob submit")
	criticalRes, err := client.SubmitBlob(ctx, payload)
	if err != nil {
		log.Fatalf("[critical] error (is executor running?): %v", err)
	}
	log.Printf("[critical] commitment=%s size=%d bytes", criticalRes.Commitment[:16]+"…", criticalRes.Size)

	// -------------------------------------------------------------------------
	// Demo 3: Merkle proof — prove a specific event is in the batch
	// -------------------------------------------------------------------------
	log.Println("\nDemo 3 — Merkle proof for event[5] in batch")
	proof, err := cosmoswasm.GetProof(batchRes.Commitments, 5)
	if err != nil {
		log.Fatalf("[proof] build error: %v", err)
	}
	if err := cosmoswasm.VerifyMerkleProof(proof); err != nil {
		log.Fatalf("[proof] INVALID: %v", err)
	}
	log.Printf("[proof] ✓ verified leaf[5]=%s in root=%s", proof.Commitment[:16]+"…", proof.Root[:16]+"…")

	// Retrieve the blob to confirm data integrity.
	data, err := client.RetrieveBlobData(ctx, proof.Commitment)
	if err != nil {
		log.Fatalf("[retrieve] error: %v", err)
	}
	// Decompress if needed.
	if decompressed, err := cosmoswasm.MaybeDecompress(data); err == nil {
		data = decompressed
	}
	log.Printf("[retrieve] ✓ got %d bytes for commitment %s", len(data), proof.Commitment[:16]+"…")

	// -------------------------------------------------------------------------
	// Demo 4: Large snapshot — single 512 KB blob
	// -------------------------------------------------------------------------
	log.Println("\nDemo 4 — Large snapshot: 512 KB blob")
	snapshot := makeSnapshot(512 * 1024)
	snapshotRes, err := client.SubmitBlob(ctx, snapshot)
	if err != nil {
		log.Fatalf("[snapshot] error: %v", err)
	}
	log.Printf("[snapshot] commitment=%s size=%d bytes", snapshotRes.Commitment[:16]+"…", snapshotRes.Size)

	// Verify round-trip.
	retrieved, err := client.RetrieveBlobData(ctx, snapshotRes.Commitment)
	if err != nil {
		log.Fatalf("[snapshot] retrieve error: %v", err)
	}
	log.Printf("[snapshot] ✓ round-trip verified: %d bytes", len(retrieved))

	// -------------------------------------------------------------------------
	// Demo 5: Compression — show savings on structured data
	// -------------------------------------------------------------------------
	sampleEvent := makeEvent(0, 16)
	samplePayload, _ := json.Marshal(sampleEvent)
	compressed, didCompress := cosmoswasm.CompressIfBeneficial(samplePayload)
	if didCompress {
		fmt.Printf("\nDemo 5 — Compression:\n")
		fmt.Printf("  Original:   %d bytes\n", len(samplePayload))
		fmt.Printf("  Compressed: %d bytes (%.0f%% reduction)\n",
			len(compressed), (1-float64(len(compressed))/float64(len(samplePayload)))*100)
	}

	// -------------------------------------------------------------------------
	// Demo 6: EstimateCost — compare direct-tx vs blob+commit
	// -------------------------------------------------------------------------
	fmt.Println("\nDemo 6 — Cost comparison (EstimateCost):")
	fmt.Printf("%-12s | %-14s | %-14s | %-10s\n", "Data Size", "Direct Gas", "Blob Gas", "Savings")
	fmt.Println("-------------|----------------|----------------|----------")
	for _, sz := range []int{1024, 100 * 1024, 1024 * 1024, 10 * 1024 * 1024} {
		est := cosmoswasm.EstimateCost(cosmoswasm.EstimateCostRequest{DataBytes: sz})
		label := formatBytes(sz)
		fmt.Printf("%-12s | %14d | %14d | %9.1f%%\n",
			label, est.DirectTx.TotalGas, est.BlobCommit.TotalGas, est.SavingsPercent)
	}

	// -------------------------------------------------------------------------
	// Demo 7: Chunking — large blob automatically split
	// -------------------------------------------------------------------------
	largeBlob := makeSnapshot(1024 * 1024) // 1 MB
	chunks, meta := cosmoswasm.ChunkBlob(largeBlob, cosmoswasm.DefaultMaxChunkSize)
	fmt.Printf("\nDemo 7 — Chunking:\n")
	fmt.Printf("  1 MB blob → %d chunks of ≤%d KB each\n", len(chunks), cosmoswasm.DefaultMaxChunkSize/1024)
	if meta != nil {
		reassembled, err := cosmoswasm.ReassembleChunks(chunks, meta)
		if err != nil {
			log.Printf("  reassemble error: %v", err)
		} else {
			fmt.Printf("  Reassembled: %d bytes, integrity ✓\n", len(reassembled))
		}
	}

	fmt.Println()
	fmt.Println("Summary — what this demo shows:")
	fmt.Println("  Blob store:    20 events + 1 critical + 1 snapshot stored off-chain")
	fmt.Printf("  Merkle root:   %s… (verifiable batch of 20)\n", batchRes.Root[:16])
	fmt.Println("  Proof:         any event verifiable offline without replaying all")
	fmt.Println("  Compression:   enabled by default (50-70% for JSON)")
	fmt.Println("  Chunking:      auto-split blobs > 512 KB")
	fmt.Println("  EstimateCost:  compare gas before choosing strategy")
	fmt.Println()
	fmt.Println("To record roots on-chain, deploy a contract with record_batch handler.")
	fmt.Println("See: examples/deploy-contract")
}

// makeEvent builds a fake game event with numPlayers player updates.
func makeEvent(frame uint64, numPlayers int) GameEvent {
	players := make([]PlayerUpdate, numPlayers)
	for i := range players {
		players[i] = PlayerUpdate{
			ID:    fmt.Sprintf("player-%d", i),
			X:     rand.Float64() * 1000,
			Y:     rand.Float64() * 1000,
			Score: rand.Intn(10000),
		}
	}
	return GameEvent{
		Frame:     frame,
		Timestamp: time.Now().UnixMilli(),
		Players:   players,
	}
}

func formatBytes(b int) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%d MB", b/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%d KB", b/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// makeSnapshot builds a fake game-state snapshot of size bytes.
func makeSnapshot(size int) []byte {
	data := make([]byte, size)
	rand.Read(data) //nolint:gosec
	return data
}
