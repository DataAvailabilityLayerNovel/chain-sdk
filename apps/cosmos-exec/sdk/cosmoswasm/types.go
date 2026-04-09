package cosmoswasm

type SubmitTxResponse struct {
	Hash string `json:"hash"`
}

type GetTxResultResponse struct {
	Found  bool               `json:"found"`
	Result *TxExecutionResult `json:"result,omitempty"`
}

type TxEventAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type TxEvent struct {
	Type       string             `json:"type"`
	Attributes []TxEventAttribute `json:"attributes"`
}

type TxExecutionResult struct {
	Hash   string    `json:"hash"`
	Height uint64    `json:"height"`
	Code   uint32    `json:"code"`
	Log    string    `json:"log"`
	Events []TxEvent `json:"events,omitempty"`
}

type QuerySmartResponse struct {
	Data    any    `json:"data,omitempty"`
	DataRaw string `json:"data_raw,omitempty"`
}

type InstantiateTxRequest struct {
	Sender string
	CodeID uint64
	Msg    any
	Label  string
	Admin  string
}

type ExecuteTxRequest struct {
	Sender   string
	Contract string
	Msg      any
}

// BlobSubmitResponse is returned by Client.SubmitBlob.
type BlobSubmitResponse struct {
	// Commitment is a hex-encoded SHA-256 of the stored data.
	// Store this on-chain (e.g. in a WASM contract) to keep gas costs minimal.
	Commitment string `json:"commitment"`
	// Size is the number of bytes stored.
	Size int `json:"size"`
}

// BlobRetrieveResponse is returned by Client.RetrieveBlob.
type BlobRetrieveResponse struct {
	Commitment string `json:"commitment"`
	// DataBase64 is the stored data encoded as standard base64.
	DataBase64 string `json:"data_base64"`
	Size       int    `json:"size"`
}

// BlobCommitTxRequest is used with BuildBlobCommitTx to record a single blob
// commitment inside a CosmWasm contract.
type BlobCommitTxRequest struct {
	// Sender is the message sender (optional, uses DefaultSender if empty).
	Sender string
	// Contract is the bech32 address of the target WASM contract.
	Contract string
	// Commitment is the hex-encoded SHA-256 returned by SubmitBlob.
	Commitment string
	// Tag is an optional application-level label (e.g. "snapshot", "event-log").
	Tag string
	// Extra holds any additional fields to merge into the contract message.
	Extra map[string]any
}

// BlobBatchResponse is returned by Client.SubmitBatch and POST /blob/batch.
type BlobBatchResponse struct {
	// Root is the Merkle root of the batch (hex SHA-256 tree of commitments).
	// Commit this on-chain — it is the only on-chain cost for N blobs.
	Root string `json:"root"`
	// Commitments are the per-blob SHA-256 hashes, in submission order.
	Commitments []string `json:"commitments"`
	// Count is len(Commitments).
	Count int `json:"count"`
}

// BatchRootTxRequest is used with BuildBatchRootTx to record a Merkle batch
// root in a CosmWasm contract.
type BatchRootTxRequest struct {
	// Sender is optional; uses DefaultSender when empty.
	Sender string
	// Contract is the bech32 address of the target WASM contract.
	Contract string
	// Root is the Merkle root returned by SubmitBatch / CommitRoot.
	Root string
	// Count is the number of blobs in the batch.
	Count int
	// Tag is an optional application-level label (e.g. "game-events").
	Tag string
	// Extra holds any additional fields to merge into the contract message.
	Extra map[string]any
}
