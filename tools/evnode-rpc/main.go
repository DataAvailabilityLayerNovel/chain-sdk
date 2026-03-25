package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	rpcclient "github.com/evstack/ev-node/pkg/rpc/client"
)

type output map[string]any

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	rpcURL := getRPCURL()
	command := os.Args[1]

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client := rpcclient.NewClient(rpcURL)

	var err error
	switch command {
	case "status":
		err = cmdStatus(ctx, client)
	case "latest-block":
		err = cmdBlock(ctx, client, 0)
	case "block":
		err = cmdBlockWithFlags(ctx, client, os.Args[2:])
	case "tx":
		err = cmdTxWithFlags(ctx, client, os.Args[2:])
	case "txs":
		err = cmdTxsWithFlags(ctx, client, os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func getRPCURL() string {
	if value := strings.TrimSpace(os.Getenv("EVNODE_RPC_URL")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("WASM_RPC_URL")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("NODE")); value != "" {
		return value
	}
	return "http://127.0.0.1:38331"
}

func cmdStatus(ctx context.Context, client *rpcclient.Client) error {
	state, err := client.GetState(ctx)
	if err != nil {
		return err
	}

	lastBlockTime := ""
	if state.GetLastBlockTime() != nil {
		lastBlockTime = state.GetLastBlockTime().AsTime().UTC().Format(time.RFC3339)
	}

	result := output{
		"chain_id":          state.GetChainId(),
		"initial_height":    state.GetInitialHeight(),
		"last_block_height": state.GetLastBlockHeight(),
		"last_block_time":   lastBlockTime,
		"da_height":         state.GetDaHeight(),
		"app_hash":          hex.EncodeToString(state.GetAppHash()),
		"last_header_hash":  hex.EncodeToString(state.GetLastHeaderHash()),
	}
	return printJSON(result)
}

func cmdBlockWithFlags(ctx context.Context, client *rpcclient.Client, args []string) error {
	fs := flag.NewFlagSet("block", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	height := fs.Uint64("height", math.MaxUint64, "block height (required, use 0 for latest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *height == math.MaxUint64 {
		return fmt.Errorf("--height is required")
	}
	return cmdBlock(ctx, client, *height)
}

func cmdBlock(ctx context.Context, client *rpcclient.Client, height uint64) error {
	resp, err := client.GetBlockByHeight(ctx, height)
	if err != nil {
		return err
	}
	if resp.GetBlock() == nil || resp.GetBlock().GetHeader() == nil || resp.GetBlock().GetHeader().GetHeader() == nil {
		return fmt.Errorf("block data not available")
	}

	header := resp.GetBlock().GetHeader().GetHeader()
	txs := resp.GetBlock().GetData().GetTxs()
	txHashes := make([]string, 0, len(txs))
	for _, tx := range txs {
		txHashes = append(txHashes, txHashHex(tx))
	}

	result := output{
		"height":           header.GetHeight(),
		"time_unix_nano":   header.GetTime(),
		"time":             time.Unix(0, int64(header.GetTime())).UTC().Format(time.RFC3339),
		"last_header_hash": hex.EncodeToString(header.GetLastHeaderHash()),
		"data_hash":        hex.EncodeToString(header.GetDataHash()),
		"app_hash":         hex.EncodeToString(header.GetAppHash()),
		"num_txs":          len(txs),
		"tx_hashes":        txHashes,
		"header_da_height": resp.GetHeaderDaHeight(),
		"data_da_height":   resp.GetDataDaHeight(),
	}
	return printJSON(result)
}

func cmdTxsWithFlags(ctx context.Context, client *rpcclient.Client, args []string) error {
	fs := flag.NewFlagSet("txs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	height := fs.Uint64("height", math.MaxUint64, "block height")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *height == math.MaxUint64 {
		return fmt.Errorf("--height is required")
	}

	resp, err := client.GetBlockByHeight(ctx, *height)
	if err != nil {
		return err
	}

	txs := resp.GetBlock().GetData().GetTxs()
	items := make([]output, 0, len(txs))
	for index, tx := range txs {
		items = append(items, output{
			"index":   index,
			"hash":    txHashHex(tx),
			"size":    len(tx),
			"raw_hex": hex.EncodeToString(tx),
		})
	}

	return printJSON(output{
		"height": *height,
		"count":  len(txs),
		"txs":    items,
	})
}

func cmdTxWithFlags(ctx context.Context, client *rpcclient.Client, args []string) error {
	fs := flag.NewFlagSet("tx", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	hash := fs.String("hash", "", "tx hash (hex, no 0x prefix)")
	depth := fs.Uint64("scan-depth", 300, "max blocks to scan backwards from latest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*hash) == "" {
		return fmt.Errorf("--hash is required")
	}

	target := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(*hash), "0x"))
	state, err := client.GetState(ctx)
	if err != nil {
		return err
	}
	latest := state.GetLastBlockHeight()
	if latest == 0 {
		return fmt.Errorf("chain has no blocks")
	}

	var lowerBound uint64 = 1
	if *depth < latest {
		lowerBound = latest - *depth + 1
	}

	for h := latest; h >= lowerBound; h-- {
		resp, err := client.GetBlockByHeight(ctx, h)
		if err != nil {
			return err
		}
		txs := resp.GetBlock().GetData().GetTxs()
		for index, tx := range txs {
			hashHex := txHashHex(tx)
			if hashHex == target {
				return printJSON(output{
					"found":        true,
					"height":       h,
					"index":        index,
					"hash":         hashHex,
					"size":         len(tx),
					"raw_hex":      hex.EncodeToString(tx),
					"header_da":    resp.GetHeaderDaHeight(),
					"data_da":      resp.GetDataDaHeight(),
					"scanned_from": latest,
					"scanned_to":   lowerBound,
				})
			}
		}
		if h == 1 {
			break
		}
	}

	return printJSON(output{
		"found":        false,
		"hash":         target,
		"scanned_from": latest,
		"scanned_to":   lowerBound,
	})
}

func txHashHex(tx []byte) string {
	hash := sha256.Sum256(tx)
	return hex.EncodeToString(hash[:])
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage() {
	fmt.Println(`Usage:
  go run ./tools/evnode-rpc status
  go run ./tools/evnode-rpc latest-block
  go run ./tools/evnode-rpc block --height <n>
  go run ./tools/evnode-rpc tx --hash <hex> [--scan-depth 300]
  go run ./tools/evnode-rpc txs --height <n>

Env priority:
  EVNODE_RPC_URL > WASM_RPC_URL > NODE > default(http://127.0.0.1:38331)`)
}
