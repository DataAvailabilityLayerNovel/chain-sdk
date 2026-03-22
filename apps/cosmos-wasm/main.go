package cosmoswasm
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/evstack/ev-node/apps/cosmos-wasm/cmd"
	evcmd "github.com/evstack/ev-node/pkg/cmd"




























}	}		os.Exit(1)		fmt.Fprintln(os.Stderr, err)	if err := rootCmd.Execute(); err != nil {	)		evcmd.KeysCmd(),		evcmd.StoreUnsafeCleanCmd,		evcmd.NetInfoCmd,		evcmd.VersionCmd,		cmd.RunCmd,		cmd.InitCmd(),	rootCmd.AddCommand(	config.AddGlobalFlags(rootCmd, "evcosmos")	}and can be backed by Cosmos SDK + CosmWasm runtime.`,The execution service must implement the Evolve execution gRPC interface		Long: `Run a Evolve full node with a Cosmos execution bridge.		Short: "Evolve node with Cosmos/WASM execution bridge",		Use:   "evcosmos",	rootCmd := &cobra.Command{func main() {)	"github.com/evstack/ev-node/pkg/config"