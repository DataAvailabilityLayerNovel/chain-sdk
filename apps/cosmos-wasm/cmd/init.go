package cmd
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















































































}	return initCmd	initCmd.Flags().String(rollgenesis.ChainIDFlag, "cosmos-wasm-test-chain", "chain ID")	rollconf.AddFlags(initCmd)	}		},			return nil			cmd.Printf("Successfully initialized config file at %s\n", cfg.ConfigPath())			}				return fmt.Errorf("error initializing genesis file: %w", err)			} else if err != nil {				cmd.Printf("Genesis file already exists at %s, skipping creation.\n", genesisPath)				}					return fmt.Errorf("error loading existing genesis file: %w", err)				} else {					}						return fmt.Errorf("existing genesis file is invalid: %w", err)					if err := genesis.Validate(); err != nil {				if genesis, err := rollgenesis.LoadGenesis(genesisPath); err == nil {			if errors.Is(err, rollgenesis.ErrGenesisExists) {			genesisPath := rollgenesis.GenesisPath(homePath)			err = rollgenesis.CreateGenesis(homePath, chainID, 1, proposerAddress)			}				return err			if err != nil {			chainID, err := cmd.Flags().GetString(rollgenesis.ChainIDFlag)			}				return err			if err := rollcmd.LoadOrGenNodeKey(homePath); err != nil {			}				return fmt.Errorf("error writing evnode.yml file: %w", err)			if err := cfg.SaveAsYaml(); err != nil {			}				return err			if err != nil {			proposerAddress, err := rollcmd.CreateSigner(&cfg, homePath, passphrase)			}				}					return fmt.Errorf("passphrase file '%s' is empty", passphraseFile)				if passphrase == "" {				passphrase = strings.TrimSpace(string(passphraseBytes))				}					return fmt.Errorf("failed to read passphrase from file '%s': %w", passphraseFile, err)				if err != nil {				passphraseBytes, err := os.ReadFile(passphraseFile)			if passphraseFile != "" {			var passphrase string			}				return fmt.Errorf("failed to get '%s' flag: %w", rollconf.FlagSignerPassphraseFile, err)			if err != nil {			passphraseFile, err := cmd.Flags().GetString(rollconf.FlagSignerPassphraseFile)			}				return fmt.Errorf("error validating config: %w", err)			if err := cfg.Validate(); err != nil {			cfg.Node.Aggregator = aggregator			cfg, _ := rollconf.Load(cmd)			}				return fmt.Errorf("error reading aggregator flag: %w", err)			if err != nil {			aggregator, err := cmd.Flags().GetBool(rollconf.FlagAggregator)			}				return fmt.Errorf("error reading home flag: %w", err)			if err != nil {			homePath, err := cmd.Flags().GetString(rollconf.FlagRootDir)		RunE: func(cmd *cobra.Command, args []string) error {		Args: cobra.NoArgs,