package cosmoswasm

import "math"

// DefaultMaxBlobSize is the per-blob limit used by the executor blob store (4 MB).
const DefaultMaxBlobSize = 4 * 1024 * 1024

// Celestia DA gas constants — mirrors docs/guides/celestia-gas-calculator.md
// and Celestia's DefaultEstimateGas logic.
const (
	// CelestiaFixedGas is the base gas cost per PayForBlobs submission.
	CelestiaFixedGas uint64 = 65_000
	// CelestiaGasPerByte is the gas charged for each byte of blob data.
	CelestiaGasPerByte uint64 = 8
	// CelestiaShareSize is the share alignment size (bytes).
	CelestiaShareSize uint64 = 480
)

// Cosmos SDK on-chain gas constants — conservative estimates for CosmWasm
// execute messages on a sovereign rollup.  These depend on the specific chain
// configuration; the values here are a reasonable default.
const (
	// CosmosBaseTxGas is the flat per-tx overhead (signature verify, ante handler).
	CosmosBaseTxGas uint64 = 200_000
	// CosmosGasPerMsgByte is the incremental gas per byte of the MsgExecuteContract.Msg field.
	CosmosGasPerMsgByte uint64 = 10
	// CosmosGasPerStoreByte is the incremental gas per byte written to contract KV state.
	CosmosGasPerStoreByte uint64 = 30
)

// CostBreakdown itemises the gas cost of a single storage approach.
type CostBreakdown struct {
	// DAGas is the Celestia PayForBlobs gas (blob data + fixed overhead).
	// Zero for pure on-chain approaches.
	DAGas uint64 `json:"da_gas"`
	// OnChainGas is the Cosmos SDK execution gas (WASM msg + state writes).
	OnChainGas uint64 `json:"on_chain_gas"`
	// TotalGas = DAGas + OnChainGas.
	TotalGas uint64 `json:"total_gas"`
	// EstFeeTIA is the estimated fee in TIA at the given gas price.
	// Value is in TIA (not uTIA); divide by 1e6 for display if needed.
	EstFeeTIA float64 `json:"est_fee_tia"`
}

// CostEstimate compares two approaches for storing the same data.
type CostEstimate struct {
	// DataBytes is the total raw (uncompressed) data size.
	DataBytes int `json:"data_bytes"`
	// CompressedBytes is the size after gzip compression (if beneficial).
	CompressedBytes int `json:"compressed_bytes"`
	// DirectTx is the cost of embedding all data inside WASM execute messages.
	DirectTx CostBreakdown `json:"direct_tx"`
	// BlobCommit is the cost using the blob-first pattern (DA + 32-byte root).
	BlobCommit CostBreakdown `json:"blob_commit"`
	// SavingsPercent = (1 - BlobCommit.TotalGas / DirectTx.TotalGas) × 100.
	SavingsPercent float64 `json:"savings_percent"`
	// NumBatches is the number of DA submissions required (accounting for
	// chunk splitting if data exceeds MaxBlobSize).
	NumBatches int `json:"num_batches"`
}

// EstimateCostRequest configures the cost estimator.
type EstimateCostRequest struct {
	// DataBytes is the total raw data size in bytes.
	DataBytes int
	// GasPriceTIA is the Celestia gas price in uTIA per gas unit.
	// Common values: 0.002 (low), 0.01 (medium), 0.04 (high).
	// Zero defaults to 0.002.
	GasPriceTIA float64
	// MaxBlobSize is the per-blob DA limit.  Zero defaults to 4 MB
	// (executor blob store limit).
	MaxBlobSize int
}

// EstimateCost compares the gas cost of storing DataBytes via:
//   - Direct on-chain: embedding raw data in WASM execute messages
//   - Blob + commit:  data → DA (compressed), 32-byte root → WASM contract
//
// The estimate mirrors Celestia's DefaultEstimateGas model from the gas
// calculator (docs/guides/celestia-gas-calculator.md) and adds Cosmos SDK
// on-chain gas modelling.
func EstimateCost(req EstimateCostRequest) *CostEstimate {
	if req.GasPriceTIA <= 0 {
		req.GasPriceTIA = 0.002
	}
	if req.MaxBlobSize <= 0 {
		req.MaxBlobSize = DefaultMaxBlobSize
	}

	rawSize := req.DataBytes
	if rawSize <= 0 {
		rawSize = 1
	}

	// --- Compression estimate ------------------------------------------------
	// Assume ~50 % compression ratio for structured data (JSON game events).
	// For random/encrypted data the ratio is ~1.0 (CompressIfBeneficial would
	// skip compression).  We use 50 % as a planning estimate.
	compressedSize := rawSize / 2
	if compressedSize < 1 {
		compressedSize = 1
	}

	// --- Approach 1: Direct on-chain (all data in WASM messages) -------------
	directOnChainGas := CosmosBaseTxGas +
		uint64(rawSize)*CosmosGasPerMsgByte +
		uint64(rawSize)*CosmosGasPerStoreByte
	directDA := celestiaGas(uint64(rawSize))
	directTotal := directDA + directOnChainGas

	// --- Approach 2: Blob-first (data → DA, 32-byte root → chain) ------------
	numBatches := (compressedSize + req.MaxBlobSize - 1) / req.MaxBlobSize
	if numBatches < 1 {
		numBatches = 1
	}

	blobDAGas := uint64(numBatches) * celestiaGas(uint64(compressedSize/numBatches))
	// On-chain cost: one MsgExecuteContract with a tiny JSON root message (~128 B).
	blobOnChainGas := CosmosBaseTxGas + 128*CosmosGasPerMsgByte + 32*CosmosGasPerStoreByte
	blobTotal := blobDAGas + blobOnChainGas

	savings := 0.0
	if directTotal > 0 {
		savings = (1.0 - float64(blobTotal)/float64(directTotal)) * 100
	}

	return &CostEstimate{
		DataBytes:       rawSize,
		CompressedBytes: compressedSize,
		DirectTx: CostBreakdown{
			DAGas:      directDA,
			OnChainGas: directOnChainGas,
			TotalGas:   directTotal,
			EstFeeTIA:  gasToTIA(directTotal, req.GasPriceTIA),
		},
		BlobCommit: CostBreakdown{
			DAGas:      blobDAGas,
			OnChainGas: blobOnChainGas,
			TotalGas:   blobTotal,
			EstFeeTIA:  gasToTIA(blobTotal, req.GasPriceTIA),
		},
		SavingsPercent: math.Round(savings*100) / 100,
		NumBatches:     numBatches,
	}
}

// celestiaGas estimates Celestia PayForBlobs gas for dataBytes of blob data.
// Mirrors DefaultEstimateGas: fixed + ceil(dataBytes / shareSize) * shareSize * gasPerByte.
func celestiaGas(dataBytes uint64) uint64 {
	if dataBytes == 0 {
		return CelestiaFixedGas
	}
	shares := (dataBytes + CelestiaShareSize - 1) / CelestiaShareSize
	paddedBytes := shares * CelestiaShareSize
	return CelestiaFixedGas + paddedBytes*CelestiaGasPerByte
}

// gasToTIA converts gas to TIA fee.  gasPrice is in uTIA/gas.
func gasToTIA(gas uint64, gasPriceUTIA float64) float64 {
	uTIA := float64(gas) * gasPriceUTIA
	return uTIA / 1e6 // uTIA → TIA
}
