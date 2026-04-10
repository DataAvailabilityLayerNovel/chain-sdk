// game-telemetry demonstrates the blob-first pattern for data-heavy dApps.
//
// It shows a game server emitting frequent event frames (game state changes,
// player moves, scores) cheaply:
//
//   - Large event frames → executor blob store (off WASM state, ~free)
//   - One Merkle root per flush → WASM contract (on-chain, minimal gas)
//   - Per-frame Merkle proofs → clients can verify any event without replaying all
//
// Run (requires cosmos-wasm nodes running at defaults):
//
//	go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry/main.go
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

	cosmoswasm "github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"
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

// stubContractAddr is a placeholder — replace with your deployed contract address.
const stubContractAddr = "cosmos1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs"

func main() {
	execURL := os.Getenv("EXEC_URL")
	if execURL == "" {
		execURL = "http://127.0.0.1:50051"
	}

	client := cosmoswasm.NewClient(execURL)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// -------------------------------------------------------------------------
	// Demo 1: BatchBuilder — accumulate game events, auto-flush every 5 s or 3 MB
	// -------------------------------------------------------------------------
	bb := cosmoswasm.NewBatchBuilder(client, cosmoswasm.BatchBuilderConfig{
		Contract: stubContractAddr,
		Tag:      "game-events",
		MaxBytes: cosmoswasm.DefaultBatchMaxBytes,
	})

	flushCh := bb.StartAutoFlush(ctx, 5*time.Second)

	// Print flush results in the background.
	go func() {
		for result := range flushCh {
			if result.Err != nil {
				log.Printf("[batch] flush error: %v", result.Err)
				continue
			}
			if result.Receipt != nil {
				log.Printf("[batch] flushed root=%s blobs=%d txHash=%s",
					result.Receipt.Root[:12]+"…", len(result.Receipt.Refs), result.Receipt.TxHash)
			}
		}
	}()

	// Emit 20 game events into the batch builder.
	log.Println("Emitting 20 game events into BatchBuilder…")
	for i := range 20 {
		event := makeEvent(uint64(i), 8)
		payload, err := json.Marshal(event)
		if err != nil {
			log.Fatalf("marshal event: %v", err)
		}

		// Add returns a receipt only when it triggered a size-based flush.
		receipt, err := bb.Add(ctx, payload, func(ctx context.Context, blobs [][]byte) (*cosmoswasm.CommitReceipt, error) {
			return client.CommitRoot(ctx, cosmoswasm.CommitRootRequest{
				Blobs:    blobs,
				Contract: stubContractAddr,
				Tag:      "game-events",
			})
		})
		if err != nil {
			// In a real game server you would handle/retry; here we log and continue.
			log.Printf("[batch] add error (frame %d): %v — is the executor running?", i, err)
			continue
		}
		if receipt != nil {
			log.Printf("[batch] size-flush at frame %d: root=%s", i, receipt.Root[:12]+"…")
		}
	}

	// -------------------------------------------------------------------------
	// Demo 2: CommitCritical — important event submitted immediately
	// -------------------------------------------------------------------------
	criticalEvent := makeEvent(9999, 4)
	criticalEvent.Players[0].Score = 1_000_000 // record-breaking score
	payload, _ := json.Marshal(criticalEvent)

	log.Println("Submitting critical event (purchase / record score) immediately…")
	receipt, err := client.CommitCritical(ctx, cosmoswasm.CommitRootRequest{
		Blobs:    [][]byte{payload},
		Contract: stubContractAddr,
		Tag:      "critical-score",
	})
	if err != nil {
		log.Printf("[critical] error (is executor running?): %v", err)
	} else {
		log.Printf("[critical] committed root=%s txHash=%s", receipt.Root[:12]+"…", receipt.TxHash)

		// -------------------------------------------------------------------------
		// Demo 3: Merkle proof — prove a specific event is in a batch
		// -------------------------------------------------------------------------
		if len(receipt.Refs) > 0 {
			commitments := make([]string, len(receipt.Refs))
			for i, r := range receipt.Refs {
				commitments[i] = r.Commitment
			}

			proof, err := cosmoswasm.GetProof(commitments, 0)
			if err != nil {
				log.Printf("[proof] build error: %v", err)
			} else {
				if err := cosmoswasm.VerifyMerkleProof(proof); err != nil {
					log.Printf("[proof] INVALID: %v", err)
				} else {
					log.Printf("[proof] ✓ verified leaf[0]=%s in root=%s", proof.Commitment[:12]+"…", proof.Root[:12]+"…")
				}

				// Demonstrate retrieval by commitment.
				data, err := client.RetrieveBlobData(ctx, proof.Commitment)
				if err != nil {
					log.Printf("[retrieve] error: %v", err)
				} else {
					log.Printf("[retrieve] ✓ got %d bytes for commitment %s", len(data), proof.Commitment[:12]+"…")
				}
			}
		}
	}

	// -------------------------------------------------------------------------
	// Demo 4: Single large snapshot (CommitRoot with one big blob)
	// -------------------------------------------------------------------------
	log.Println("Committing large game-state snapshot (512 KB)…")
	snapshot := makeSnapshot(512 * 1024)
	receipt2, err := client.CommitRoot(ctx, cosmoswasm.CommitRootRequest{
		Blobs:    [][]byte{snapshot},
		Contract: stubContractAddr,
		Tag:      "snapshot",
	})
	if err != nil {
		log.Printf("[snapshot] error (is executor running?): %v", err)
	} else {
		log.Printf("[snapshot] committed root=%s size=%d bytes txHash=%s",
			receipt2.Root[:12]+"…", len(snapshot), receipt2.TxHash)
	}

	// -------------------------------------------------------------------------
	// Demo 5: Compression — show savings on structured data
	// -------------------------------------------------------------------------
	sampleEvent := makeEvent(0, 16)
	samplePayload, _ := json.Marshal(sampleEvent)
	compressed, didCompress := cosmoswasm.CompressIfBeneficial(samplePayload)
	if didCompress {
		fmt.Printf("\nCompression demo:\n")
		fmt.Printf("  Original:   %d bytes\n", len(samplePayload))
		fmt.Printf("  Compressed: %d bytes (%.0f%% reduction)\n",
			len(compressed), (1-float64(len(compressed))/float64(len(samplePayload)))*100)
		fmt.Println("  (BatchBuilder compresses by default — no extra code needed)")
	}

	// -------------------------------------------------------------------------
	// Demo 6: EstimateCost — compare direct-tx vs blob+commit
	// -------------------------------------------------------------------------
	fmt.Println("\nCost comparison (EstimateCost):")
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
	fmt.Printf("\nChunking demo:\n")
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
	fmt.Println("Summary — what went on-chain vs off-chain:")
	fmt.Println("  Off-chain (executor blob store): all event payloads + snapshots")
	fmt.Println("  On-chain (WASM contract):        one 32-byte Merkle root per flush")
	fmt.Println("  Proof:                           clients verify any event offline")
	fmt.Println("  Compression:                     enabled by default in BatchBuilder")
	fmt.Println("  Chunking:                        auto-split blobs > 512 KB")
	fmt.Println("  EstimateCost:                    compare gas before choosing strategy")

	// Let auto-flush finish one final cycle before exit.
	select {
	case <-time.After(6 * time.Second):
	case <-ctx.Done():
	}
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
