// Package main is the entrypoint of the `evcosmos` binary.
//
// VI: Đây là binary `evcosmos` — TẦNG ĐỒNG THUẬN (ev-node runtime) của rollup
// Cosmos/WASM. Nó lo: sản xuất block (khi aggregator), ký header, submit/đọc
// blob lên Celestia (DA), P2P, và đồng bộ block. Nó KHÔNG tự chạy tx —
// phần thực thi (CosmWasm/cosmos-sdk state) nằm ở binary riêng `cosmos-exec-grpc`
// (thư mục ../cosmos-exec), evcosmos gọi qua cờ --grpc-executor-url.
//
// Cả "sequencer" lẫn "full node" đều là binary này, chỉ khác cờ
// --evnode.node.aggregator (true/false). Xem chi tiết kiến trúc + thư mục data:
// ../cosmos-exec/sdk/cosmoswasm/docs/node-operations.md.
//
// Thư mục apps/cosmos-wasm/:
//
//	main.go                 — file này: dựng CLI gốc `evcosmos` + gắn subcommand
//	cmd/init.go             — `evcosmos init`: tạo config/genesis/signer cho home
//	cmd/run.go              — `evcosmos start`: mở store, dựng sequencer, StartNode
//	cmd/executor_client.go  — client nối tới cosmos-exec-grpc (execution bridge)
//	go.mod                  — module riêng (multi-module repo)
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-wasm/cmd"
	evcmd "github.com/DataAvailabilityLayerNovel/chain-sdk/pkg/cmd"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/pkg/config"
)

// main lắp ráp cây lệnh cobra cho `evcosmos` rồi thực thi. Không chứa logic
// nghiệp vụ — chỉ wiring CLI; mỗi subcommand tự xử lý ở package cmd / pkg/cmd.
func main() {
	rootCmd := &cobra.Command{
		Use:   "evcosmos",
		Short: "Evolve node with Cosmos/WASM execution bridge",
		Long: `Run a Evolve full node with a Cosmos execution bridge.
The execution service must implement the Evolve execution gRPC interface
and can be backed by Cosmos SDK + CosmWasm runtime.`,
	}

	// Gắn cờ toàn cục --evnode.* (node/p2p/da/rpc/signer...) cho mọi subcommand;
	// tên + default định nghĩa ở pkg/config.
	config.AddGlobalFlags(rootCmd, "evcosmos")

	rootCmd.AddCommand(
		cmd.InitCmd(),             // init: dựng config/genesis/signer trong --home
		cmd.RunCmd,                // start: chạy node (block production / sync + DA + P2P)
		evcmd.VersionCmd,          // version: in version/commit của binary
		evcmd.NetInfoCmd,          // net-info: in địa chỉ P2P / peers (dùng cho --p2p.peers)
		evcmd.StoreUnsafeCleanCmd, // unsafe-clean: xoá store đồng thuận (nguy hiểm)
		evcmd.KeysCmd(),           // keys: quản lý signer key (add/show/...)
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
