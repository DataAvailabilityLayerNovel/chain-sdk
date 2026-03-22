package cmd
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
	cosmosWasmDBName = "cosmos-wasm"
	FlagGrpcExecutorURL = "grpc-executor-url"
)

























































































































}	return seq, nil	}		return nil, fmt.Errorf("failed to create single sequencer: %w", err)	if err != nil {	)		executor,		genesisDoc,		1000,		[]byte(genesisDoc.ChainID),		nodeConfig,		daClient,		datastore,		logger,	seq, err := single.NewSequencer(	}		return basedSeq, nil			Msg("based sequencer initialized")			Uint64("da_epoch", genesisDoc.DAEpochForcedInclusion).			Str("forced_inclusion_namespace", nodeConfig.DA.GetForcedInclusionNamespace()).		logger.Info().		}			return nil, fmt.Errorf("failed to create based sequencer: %w", err)		if err != nil {		basedSeq, err := based.NewBasedSequencer(daClient, nodeConfig, datastore, genesisDoc, logger, executor)		}			return nil, fmt.Errorf("based sequencer mode requires aggregator mode to be enabled")		if !nodeConfig.Node.Aggregator {	if nodeConfig.Node.BasedSequencer {	daClient := block.NewDAClient(blobClient, nodeConfig, logger)	}		return nil, fmt.Errorf("failed to create blob client: %w", err)	if err != nil {	blobClient, err := blobrpc.NewWSClient(ctx, nodeConfig.DA.Address, nodeConfig.DA.AuthToken, "")) (coresequencer.Sequencer, error) {	executor execution.Executor,	genesisDoc genesis.Genesis,	nodeConfig config.Config,	datastore datastore.Batching,	logger zerolog.Logger,	ctx context.Context,func createSequencer(}	return executiongrpc.NewClient(executorURL), nil	}		return nil, fmt.Errorf("%s flag is required", FlagGrpcExecutorURL)	if executorURL == "" {	}		return nil, fmt.Errorf("failed to get '%s' flag: %w", FlagGrpcExecutorURL, err)	if err != nil {	executorURL, err := cmd.Flags().GetString(FlagGrpcExecutorURL)func createExecutionClient(cmd *cobra.Command) (execution.Executor, error) {}	cmd.Flags().String(FlagGrpcExecutorURL, "http://localhost:50051", "URL of Cosmos/WASM execution gRPC service")func addExecutionFlags(cmd *cobra.Command) {}	addExecutionFlags(RunCmd)	config.AddFlags(RunCmd)func init() {}	},		return rollcmd.StartNode(logger, cmd, executor, sequencer, nodeKey, datastore, nodeConfig, genesisDoc, node.NodeOptions{})		}			return err		if err != nil {		nodeKey, err := key.LoadNodeKey(filepath.Dir(nodeConfig.ConfigPath()))		}			return err		if err != nil {		sequencer, err := createSequencer(cmd.Context(), logger, datastore, nodeConfig, genesisDoc, executor)		}			logger.Warn().Msg("da_start_height is not set in genesis.json, ask your chain developer")		if genesisDoc.DAStartHeight == 0 && !nodeConfig.Node.Aggregator {		}			return err		if err != nil {		genesisDoc, err := rollgenesis.LoadGenesis(rollgenesis.GenesisPath(nodeConfig.RootDir))		}			return err		if err != nil {		datastore, err := store.NewDefaultKVStore(nodeConfig.RootDir, nodeConfig.DBPath, cosmosWasmDBName)		logger.Info().Str("headerNamespace", headerNamespace.HexString()).Str("dataNamespace", dataNamespace.HexString()).Msg("namespaces")		dataNamespace := da.NamespaceFromString(nodeConfig.DA.GetDataNamespace())		headerNamespace := da.NamespaceFromString(nodeConfig.DA.GetNamespace())		logger := rollcmd.SetupLogger(nodeConfig.Log)		}			return err		if err != nil {		nodeConfig, err := rollcmd.ParseConfig(cmd)		}			return err		if err != nil {		executor, err := createExecutionClient(cmd)	RunE: func(cmd *cobra.Command, args []string) error {through the Evolve execution gRPC interface.`,	Long: `Start a Evolve node that connects to a Cosmos/WASM execution service	Short:   "Run Evolve node with Cosmos/WASM execution bridge",	Aliases: []string{"node", "run"},	Use:     "start",var RunCmd = &cobra.Command{