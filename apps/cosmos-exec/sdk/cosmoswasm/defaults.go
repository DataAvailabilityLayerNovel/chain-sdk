package cosmoswasm

import "time"

const (
	// DefaultPollInterval is the suggested WaitTxResult polling cadence.
	DefaultPollInterval = 1 * time.Second

	// DefaultTxTimeout is the suggested context timeout for a single tx lifecycle
	// (submit + wait for result).
	DefaultTxTimeout = 60 * time.Second
)
