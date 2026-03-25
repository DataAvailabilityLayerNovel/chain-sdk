package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ipfs/go-datastore"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/evstack/ev-node/block"
	"github.com/evstack/ev-node/core/execution"
	coresequencer "github.com/evstack/ev-node/core/sequencer"
	executiongrpc "github.com/evstack/ev-node/execution/grpc"
	"github.com/evstack/ev-node/node"
	rollcmd "github.com/evstack/ev-node/pkg/cmd"
	"github.com/evstack/ev-node/pkg/config"
	blobrpc "github.com/evstack/ev-node/pkg/da/jsonrpc"
	da "github.com/evstack/ev-node/pkg/da/types"
	"github.com/evstack/ev-node/pkg/genesis"
	rollgenesis "github.com/evstack/ev-node/pkg/genesis"
	"github.com/evstack/ev-node/pkg/p2p/key"
	"github.com/evstack/ev-node/pkg/sequencers/based"
	"github.com/evstack/ev-node/pkg/sequencers/single"
	"github.com/evstack/ev-node/pkg/store"
)

const (
	cosmosWasmDBName    = "cosmos-wasm"
	FlagGrpcExecutorURL = "grpc-executor-url"
)

var RunCmd = &cobra.Command{
	Use:     "start",
	Aliases: []string{"node", "run"},
	Short:   "Run Evolve node with Cosmos/WASM execution bridge",
	Long: `Start a Evolve node that connects to a Cosmos/WASM execution service
through the Evolve execution gRPC interface.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		executor, err := createExecutionClient(cmd)
		if err != nil {
			return err
		}

		nodeConfig, err := rollcmd.ParseConfig(cmd)
		if err != nil {
			return err
		}

		logger := rollcmd.SetupLogger(nodeConfig.Log)

		headerNamespace := da.NamespaceFromString(nodeConfig.DA.GetNamespace())
		dataNamespace := da.NamespaceFromString(nodeConfig.DA.GetDataNamespace())
		logger.Info().Str("headerNamespace", headerNamespace.HexString()).Str("dataNamespace", dataNamespace.HexString()).Msg("namespaces")

		datastore, err := store.NewDefaultKVStore(nodeConfig.RootDir, nodeConfig.DBPath, cosmosWasmDBName)
		if err != nil {
			return err
		}

		genesisDoc, err := rollgenesis.LoadGenesis(rollgenesis.GenesisPath(nodeConfig.RootDir))
		if err != nil {
			return err
		}

		if genesisDoc.DAStartHeight == 0 && !nodeConfig.Node.Aggregator {
			logger.Warn().Msg("da_start_height is not set in genesis.json, ask your chain developer")
		}

		sequencer, err := createSequencer(cmd.Context(), logger, datastore, nodeConfig, genesisDoc, executor)
		if err != nil {
			return err
		}

		nodeKey, err := key.LoadNodeKey(filepath.Dir(nodeConfig.ConfigPath()))
		if err != nil {
			return err
		}

		return rollcmd.StartNode(logger, cmd, executor, sequencer, nodeKey, datastore, nodeConfig, genesisDoc, node.NodeOptions{})
	},
}

func init() {
	config.AddFlags(RunCmd)
	addExecutionFlags(RunCmd)
}

func addExecutionFlags(cmd *cobra.Command) {
	cmd.Flags().String(FlagGrpcExecutorURL, "http://localhost:50051", "URL of Cosmos/WASM execution gRPC service")
}

func createExecutionClient(cmd *cobra.Command) (execution.Executor, error) {
	executorURL, err := cmd.Flags().GetString(FlagGrpcExecutorURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get '%s' flag: %w", FlagGrpcExecutorURL, err)
	}
	if executorURL == "" {
		return nil, fmt.Errorf("%s flag is required", FlagGrpcExecutorURL)
	}

	return executiongrpc.NewClient(executorURL), nil
}

func createSequencer(
	ctx context.Context,
	logger zerolog.Logger,
	datastore datastore.Batching,
	nodeConfig config.Config,
	genesisDoc genesis.Genesis,
	executor execution.Executor,
) (coresequencer.Sequencer, error) {
	blobClient, err := blobrpc.NewWSClient(ctx, nodeConfig.DA.Address, nodeConfig.DA.AuthToken, "")
	if err != nil {
		logger.Warn().Err(err).Str("da_address", nodeConfig.DA.Address).Msg("failed to create websocket DA client, falling back to HTTP client")
		blobClient, err = blobrpc.NewClient(ctx, nodeConfig.DA.Address, nodeConfig.DA.AuthToken, "")
		if err != nil {
			return nil, fmt.Errorf("failed to create DA client over websocket and http: %w", err)
		}
	}

	daClient := block.NewDAClient(blobClient, nodeConfig, logger)

	if nodeConfig.Node.BasedSequencer {
		if !nodeConfig.Node.Aggregator {
			return nil, fmt.Errorf("based sequencer mode requires aggregator mode to be enabled")
		}

		basedSeq, err := based.NewBasedSequencer(daClient, nodeConfig, datastore, genesisDoc, logger, executor)
		if err != nil {
			return nil, fmt.Errorf("failed to create based sequencer: %w", err)
		}

		logger.Info().
			Str("forced_inclusion_namespace", nodeConfig.DA.GetForcedInclusionNamespace()).
			Uint64("da_epoch", genesisDoc.DAEpochForcedInclusion).
			Msg("based sequencer initialized")

		return basedSeq, nil
	}

	seq, err := single.NewSequencer(
		logger,
		datastore,
		daClient,
		nodeConfig,
		[]byte(genesisDoc.ChainID),
		1000,
		genesisDoc,
		executor,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create single sequencer: %w", err)
	}

	return seq, nil
}
