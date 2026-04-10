// Quickstart — copy-paste this file and run in under 5 minutes.
//
// Prerequisites:
//   1. Start the executor:
//      go run ./apps/cosmos-exec/cmd/cosmos-exec-grpc --in-memory
//
//   2. Run this example:
//      go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/quickstart/main.go
//
// What it does (no WASM contract needed):
//   • Submits 3 blobs to the blob store
//   • Retrieves one by commitment
//   • Builds + verifies a Merkle proof
//   • Prints a cost estimate
//   • Shows compression savings
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	cosmoswasm "github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
	// ── Connect ──────────────────────────────────────────────────────────
	url := os.Getenv("EXEC_URL")
	if url == "" {
		url = "http://127.0.0.1:50051"
	}
	client := cosmoswasm.NewClient(url)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── 1. Store blobs ──────────────────────────────────────────────────
	blobs := [][]byte{
		[]byte(`{"move":"e2e4"}`),
		[]byte(`{"move":"e7e5"}`),
		[]byte(`{"move":"g1f3"}`),
	}

	fmt.Println("Step 1 — submit 3 blobs")
	commitments := make([]string, len(blobs))
	for i, b := range blobs {
		res, err := client.SubmitBlob(ctx, b)
		if err != nil {
			log.Fatalf("  blob[%d] failed: %v", i, err)
		}
		commitments[i] = res.Commitment
		fmt.Printf("  blob[%d] → %s…  (%d bytes)\n", i, res.Commitment[:12], res.Size)
	}

	// ── 2. Retrieve ─────────────────────────────────────────────────────
	fmt.Println("\nStep 2 — retrieve blob[0]")
	data, err := client.RetrieveBlobData(ctx, commitments[0])
	if err != nil {
		log.Fatalf("  retrieve failed: %v", err)
	}
	fmt.Printf("  got: %s\n", string(data))

	// ── 3. Merkle proof ─────────────────────────────────────────────────
	fmt.Println("\nStep 3 — Merkle proof for blob[1]")
	proof, err := cosmoswasm.GetProof(commitments, 1)
	if err != nil {
		log.Fatalf("  proof build failed: %v", err)
	}
	if err := cosmoswasm.VerifyMerkleProof(proof); err != nil {
		log.Fatalf("  proof INVALID: %v", err)
	}
	fmt.Printf("  root=%s…  verified ✓\n", proof.Root[:12])

	// ── 4. Cost estimate ────────────────────────────────────────────────
	fmt.Println("\nStep 4 — cost estimate (1 MB)")
	est := cosmoswasm.EstimateCost(cosmoswasm.EstimateCostRequest{DataBytes: 1024 * 1024})
	fmt.Printf("  direct on-chain: %d gas\n", est.DirectTx.TotalGas)
	fmt.Printf("  blob + commit:   %d gas  (%.0f%% cheaper)\n", est.BlobCommit.TotalGas, est.SavingsPercent)

	// ── 5. Compression ──────────────────────────────────────────────────
	fmt.Println("\nStep 5 — compression")
	sample := []byte(strings.Repeat(`{"tick":1}`, 200))
	compressed, ok := cosmoswasm.CompressIfBeneficial(sample)
	if ok {
		fmt.Printf("  %d B → %d B  (%.0f%% smaller)\n",
			len(sample), len(compressed),
			(1-float64(len(compressed))/float64(len(sample)))*100)
	}

	fmt.Println("\nDone! All 5 steps passed.")
}
