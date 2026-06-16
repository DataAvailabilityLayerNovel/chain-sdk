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
//   - Builds a CosmWasm bank-send tx (the same one a normal client would
//     submit through cosmos-exec-grpc).
//   - Posts the raw tx bytes as a Celestia blob in the forced inclusion
//     namespace via Celestia's JSON-RPC `blob.Submit`. The sequencer is
//     bypassed.
//   - Polls cosmos-exec-grpc /tx/result/{hash} until the tx appears, proving
//     the sequencer was forced to include it.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	libshare "github.com/celestiaorg/go-square/v3/share"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
	jsonrpc "github.com/DataAvailabilityLayerNovel/chain-sdk/pkg/da/jsonrpc"
)

func main() {
	var (
		execURL      = flag.String("exec-url", "http://127.0.0.1:50051", "cosmos-exec-grpc URL")
		daBridgeRPC  = flag.String("da-rpc", firstNonEmpty(os.Getenv("DA_BRIDGE_RPC"), "http://127.0.0.1:26658"), "Celestia bridge JSON-RPC URL")
		daAuthToken  = flag.String("da-token", os.Getenv("DA_AUTH_TOKEN"), "Celestia bridge auth token (Bearer)")
		nsString     = flag.String("namespace", "rollup-fi", "forced inclusion namespace string")
		txHex        = flag.String("tx-hex", "", "raw tx bytes in hex; if empty, a demo no-op tx is built")
		pollInterval = flag.Duration("poll", 2*time.Second, "interval to poll /tx/result")
		pollTimeout  = flag.Duration("timeout", 5*time.Minute, "give up after this duration")
	)
	flag.Parse()

	if strings.TrimSpace(*daBridgeRPC) == "" {
		log.Fatal("DA bridge RPC URL is required (--da-rpc or DA_BRIDGE_RPC)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *pollTimeout)
	defer cancel()

	// 1. Resolve the raw tx bytes.
	var txBytes []byte
	if strings.TrimSpace(*txHex) != "" {
		b, err := hex.DecodeString(strings.TrimPrefix(*txHex, "0x"))
		if err != nil {
			log.Fatalf("decode --tx-hex: %v", err)
		}
		txBytes = b
	} else {
		// For the demo, use a tiny payload that's clearly identifiable on DA.
		// This will not actually execute as a Cosmos tx — it just demonstrates
		// the submit path. Real users should pass a real tx via --tx-hex.
		txBytes = []byte(fmt.Sprintf("forced-inclusion-demo-%d", time.Now().UnixNano()))
		log.Printf("[demo] no --tx-hex provided; submitting marker bytes (len=%d)", len(txBytes))
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

	height, err := cli.Blob.Submit(ctx, []*jsonrpc.Blob{blob}, nil)
	if err != nil {
		log.Fatalf("submit blob: %v", err)
	}
	log.Printf("blob submitted to Celestia: DA height=%d namespace=%s", height, *nsString)

	// 4. Wait for the sequencer to pick it up. Forced inclusion is enforced
	// at epoch boundaries — sequencer must include this tx before the epoch
	// containing `height` ends. We poll cosmos-exec-grpc /tx/result.
	client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
		ExecURL: *execURL,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		log.Fatalf("init exec client: %v", err)
	}

	log.Printf("polling %s/tx/result/%s every %s ...", *execURL, txHashHex, *pollInterval)
	res, err := client.WaitTxResult(ctx, txHashHex, *pollInterval)
	if err != nil {
		log.Fatalf("wait tx: %v (the sequencer may not have processed it yet — check evcosmos logs for 'forced_inclusion')", err)
	}

	log.Printf("SUCCESS: tx included at chain height=%d code=%d log=%q", res.Height, res.Code, res.Log)
	if res.Code != 0 {
		log.Printf("note: code != 0 means the tx was included but failed to execute. The censorship-resistance guarantee is about INCLUSION, not success — sequencer cannot refuse to include, but the chain still validates the tx normally.")
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
