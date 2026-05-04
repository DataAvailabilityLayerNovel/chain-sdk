package cosmoswasm

import (
	"context"
	"io"
	"time"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/devchain"
)

// Default endpoint URLs used by DALChainConfig when no overrides are provided.
const (
	DefaultSequencerRPCURL  = devchain.DefaultSequencerRPCURL
	DefaultFullNodeRPCURL   = devchain.DefaultFullNodeRPCURL
	DefaultSequencerExecURL = devchain.DefaultSequencerExecURL
	DefaultFullNodeExecURL  = devchain.DefaultFullNodeExecURL
)

// DALChainConfig holds configuration for launching a local DAL chain.
// This is a dev/test utility — not part of the core SDK API.
type DALChainConfig struct {
	ProjectRoot    string
	ChainName      string
	Namespace      string
	DABridgeRPC    string
	DAAuthToken    string
	CleanOnStart   bool
	CleanOnExit    bool
	LogLevel       string
	BlockTime      time.Duration
	SubmitInterval time.Duration
	Stdout         io.Writer
	Stderr         io.Writer

	SequencerRPC     string
	FullNodeRPC      string
	SequencerExecURL string
	FullNodeExecURL  string
}

// DALChainEndpoints contains the resolved endpoint URLs for the running chain.
type DALChainEndpoints struct {
	SequencerRPC     string
	FullNodeRPC      string
	SequencerExecAPI string
	FullNodeExecAPI  string
}

// DALChainProcess represents a running local chain process.
type DALChainProcess struct {
	Config    DALChainConfig
	Endpoints DALChainEndpoints
	proc      *devchain.Process
}

// Stop kills the running chain process.
func (p *DALChainProcess) Stop() error {
	if p == nil || p.proc == nil {
		return nil
	}
	return p.proc.Stop()
}

// Validate checks that the config has all required fields.
func (c DALChainConfig) Validate() error {
	return dalConfigToInternal(c).Validate()
}

// DefaultDALChainConfig returns a DALChainConfig with sensible defaults.
func DefaultDALChainConfig(projectRoot string) DALChainConfig {
	dc := devchain.DefaultConfig(projectRoot)
	return dalConfigFromInternal(dc)
}

// StartDALChain launches a local DAL chain and waits for it to become healthy.
func StartDALChain(ctx context.Context, cfg DALChainConfig) (*DALChainProcess, error) {
	ic := dalConfigToInternal(cfg)

	proc, err := devchain.Start(ctx, ic)
	if err != nil {
		return nil, err
	}

	return &DALChainProcess{
		Config: cfg,
		Endpoints: DALChainEndpoints{
			SequencerRPC:     proc.Endpoints.SequencerRPC,
			FullNodeRPC:      proc.Endpoints.FullNodeRPC,
			SequencerExecAPI: proc.Endpoints.SequencerExecAPI,
			FullNodeExecAPI:  proc.Endpoints.FullNodeExecAPI,
		},
		proc: proc,
	}, nil
}

func dalConfigToInternal(c DALChainConfig) devchain.Config {
	return devchain.Config{
		ProjectRoot:      c.ProjectRoot,
		ChainName:        c.ChainName,
		Namespace:        c.Namespace,
		DABridgeRPC:      c.DABridgeRPC,
		DAAuthToken:      c.DAAuthToken,
		CleanOnStart:     c.CleanOnStart,
		CleanOnExit:      c.CleanOnExit,
		LogLevel:         c.LogLevel,
		BlockTime:        c.BlockTime,
		SubmitInterval:   c.SubmitInterval,
		Stdout:           c.Stdout,
		Stderr:           c.Stderr,
		SequencerRPC:     c.SequencerRPC,
		FullNodeRPC:      c.FullNodeRPC,
		SequencerExecURL: c.SequencerExecURL,
		FullNodeExecURL:  c.FullNodeExecURL,
	}
}

func dalConfigFromInternal(dc devchain.Config) DALChainConfig {
	return DALChainConfig{
		ProjectRoot:      dc.ProjectRoot,
		ChainName:        dc.ChainName,
		Namespace:        dc.Namespace,
		DABridgeRPC:      dc.DABridgeRPC,
		DAAuthToken:      dc.DAAuthToken,
		CleanOnStart:     dc.CleanOnStart,
		CleanOnExit:      dc.CleanOnExit,
		LogLevel:         dc.LogLevel,
		BlockTime:        dc.BlockTime,
		SubmitInterval:   dc.SubmitInterval,
		Stdout:           dc.Stdout,
		Stderr:           dc.Stderr,
		SequencerRPC:     dc.SequencerRPC,
		FullNodeRPC:      dc.FullNodeRPC,
		SequencerExecURL: dc.SequencerExecURL,
		FullNodeExecURL:  dc.FullNodeExecURL,
	}
}
