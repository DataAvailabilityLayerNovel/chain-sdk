package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/evstack/ev-node/apps/cosmos-wasm/cmd"
	evcmd "github.com/evstack/ev-node/pkg/cmd"
	"github.com/evstack/ev-node/pkg/config"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "evcosmos",
		Short: "Evolve node with Cosmos/WASM execution bridge",
		Long: `Run a Evolve full node with a Cosmos execution bridge.
The execution service must implement the Evolve execution gRPC interface
and can be backed by Cosmos SDK + CosmWasm runtime.`,
	}

	config.AddGlobalFlags(rootCmd, "evcosmos")

	rootCmd.AddCommand(
		cmd.InitCmd(),
		cmd.RunCmd,
		evcmd.VersionCmd,
		evcmd.NetInfoCmd,
		evcmd.StoreUnsafeCleanCmd,
		evcmd.KeysCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
