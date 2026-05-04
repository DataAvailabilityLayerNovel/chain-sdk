package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/evm/cmd"
	rollcmd "github.com/DataAvailabilityLayerNovel/chain-sdk/pkg/cmd"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/pkg/config"
)

func main() {
	// Initiate the root command
	rootCmd := &cobra.Command{
		Use:   "evm",
		Short: "Evolve with EVM; single sequencer",
	}

	config.AddGlobalFlags(rootCmd, "evm")

	// Add configuration flags to NetInfoCmd so it can read RPC address
	config.AddFlags(rollcmd.NetInfoCmd)

	rootCmd.AddCommand(
		cmd.InitCmd(),
		cmd.RunCmd,
		cmd.NewRollbackCmd(),
		rollcmd.VersionCmd,
		rollcmd.NetInfoCmd,
		rollcmd.StoreUnsafeCleanCmd,
		rollcmd.KeysCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		// Print to stderr and exit with error
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
