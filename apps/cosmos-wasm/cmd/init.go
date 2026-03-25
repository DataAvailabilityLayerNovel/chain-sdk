package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	rollcmd "github.com/evstack/ev-node/pkg/cmd"
	rollconf "github.com/evstack/ev-node/pkg/config"
	rollgenesis "github.com/evstack/ev-node/pkg/genesis"
)

func InitCmd() *cobra.Command {
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize evolve config for Cosmos/WASM full node",
		Long: `Initialize configuration files for a Evolve node that uses
Cosmos/WASM as execution backend via gRPC execution bridge.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			homePath, err := cmd.Flags().GetString(rollconf.FlagRootDir)
			if err != nil {
				return fmt.Errorf("error reading home flag: %w", err)
			}

			aggregator, err := cmd.Flags().GetBool(rollconf.FlagAggregator)
			if err != nil {
				return fmt.Errorf("error reading aggregator flag: %w", err)
			}

			cfg, _ := rollconf.Load(cmd)
			cfg.Node.Aggregator = aggregator
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("error validating config: %w", err)
			}

			passphraseFile, err := cmd.Flags().GetString(rollconf.FlagSignerPassphraseFile)
			if err != nil {
				return fmt.Errorf("failed to get '%s' flag: %w", rollconf.FlagSignerPassphraseFile, err)
			}

			var passphrase string
			if passphraseFile != "" {
				passphraseBytes, err := os.ReadFile(passphraseFile)
				if err != nil {
					return fmt.Errorf("failed to read passphrase from file '%s': %w", passphraseFile, err)
				}
				passphrase = strings.TrimSpace(string(passphraseBytes))
				if passphrase == "" {
					return fmt.Errorf("passphrase file '%s' is empty", passphraseFile)
				}
			}

			proposerAddress, err := rollcmd.CreateSigner(&cfg, homePath, passphrase)
			if err != nil {
				return err
			}

			if err := cfg.SaveAsYaml(); err != nil {
				return fmt.Errorf("error writing evnode.yml file: %w", err)
			}

			if err := rollcmd.LoadOrGenNodeKey(homePath); err != nil {
				return err
			}

			chainID, err := cmd.Flags().GetString(rollgenesis.ChainIDFlag)
			if err != nil {
				return err
			}

			err = rollgenesis.CreateGenesis(homePath, chainID, 1, proposerAddress)
			genesisPath := rollgenesis.GenesisPath(homePath)
			if errors.Is(err, rollgenesis.ErrGenesisExists) {
				if genesis, err := rollgenesis.LoadGenesis(genesisPath); err == nil {
					if err := genesis.Validate(); err != nil {
						return fmt.Errorf("existing genesis file is invalid: %w", err)
					}
				} else {
					return fmt.Errorf("error loading existing genesis file: %w", err)
				}

				cmd.Printf("Genesis file already exists at %s, skipping creation.\n", genesisPath)
			} else if err != nil {
				return fmt.Errorf("error initializing genesis file: %w", err)
			}

			cmd.Printf("Successfully initialized config file at %s\n", cfg.ConfigPath())
			return nil
		},
	}

	rollconf.AddFlags(initCmd)
	initCmd.Flags().String(rollgenesis.ChainIDFlag, "cosmos-wasm-test-chain", "chain ID")

	return initCmd
}
