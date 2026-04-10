package cosmoswasm

import "time"

// ─── All SDK default constants in one place ──────────────────────────────────
//
// This file documents every tuneable default used across the cosmoswasm SDK.
// Override any value via the corresponding Config struct field.
//
// ┌─────────────────────────┬───────────────────┬────────────────────────────────────┐
// │ Constant                │ Value             │ Where / What                       │
// ├─────────────────────────┼───────────────────┼────────────────────────────────────┤
// │ DefaultExecAPIURL       │ 127.0.0.1:50051   │ Client HTTP target                 │
// │ DefaultBatchMaxBytes    │ 3 MB              │ BatchBuilder flush threshold        │
// │ DefaultBatchFlushInterval│ 5 s              │ BatchBuilder auto-flush interval    │
// │ DefaultMaxChunkSize     │ 512 KiB           │ Per-blob chunk limit               │
// │ DefaultMaxBlobSize      │ 4 MB              │ Executor blob store per-blob cap   │
// │ DefaultCompress         │ true              │ Gzip in BatchBuilder               │
// │ DefaultPollInterval     │ 1 s               │ WaitTxResult polling               │
// │ DefaultTxTimeout        │ 60 s              │ Suggested per-tx context timeout    │
// │ CelestiaFixedGas        │ 65 000            │ EstimateCost base gas              │
// │ CelestiaGasPerByte      │ 8                 │ EstimateCost per-byte gas          │
// │ CelestiaShareSize       │ 480 B             │ EstimateCost share alignment       │
// │ CosmosBaseTxGas         │ 200 000           │ EstimateCost per-tx overhead       │
// └─────────────────────────┴───────────────────┴────────────────────────────────────┘

const (
	// DefaultPollInterval is the suggested WaitTxResult polling cadence.
	DefaultPollInterval = 1 * time.Second

	// DefaultTxTimeout is the suggested context timeout for a single tx lifecycle
	// (submit + wait for result).
	DefaultTxTimeout = 60 * time.Second
)

// DefaultBatchBuilderConfig returns a BatchBuilderConfig with all defaults
// populated and documented.  Pass it to NewBatchBuilder and override only the
// fields you need:
//
//	cfg := cosmoswasm.DefaultBatchBuilderConfig()
//	cfg.Contract = myAddr
//	cfg.Tag      = "game-events"
//	bb := cosmoswasm.NewBatchBuilder(client, cfg)
func DefaultBatchBuilderConfig() BatchBuilderConfig {
	compress := true
	return BatchBuilderConfig{
		MaxBytes:     DefaultBatchMaxBytes,     // 3 MB — flush when accumulated size hits this
		MaxChunkSize: DefaultMaxChunkSize,      // 512 KiB — auto-split blobs larger than this
		Compress:     &compress,                // gzip BestSpeed — skip for incompressible data
		Tag:          "",                       // set this to your app label
		Contract:     "",                       // REQUIRED: your WASM contract bech32 address
		Sender:       "",                       // optional: uses DefaultSender() when empty
	}
}

// DefaultEstimateCostRequest returns an EstimateCostRequest with sensible
// defaults pre-filled.  Override DataBytes before calling EstimateCost():
//
//	req := cosmoswasm.DefaultEstimateCostRequest()
//	req.DataBytes = 500_000
//	est := cosmoswasm.EstimateCost(req)
func DefaultEstimateCostRequest() EstimateCostRequest {
	return EstimateCostRequest{
		DataBytes:   0,     // REQUIRED: set this
		GasPriceTIA: 0.002, // Celestia low gas price (uTIA/gas)
		MaxBlobSize: DefaultMaxBlobSize,
	}
}
