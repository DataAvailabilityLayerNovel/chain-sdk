package cosmoswasm

import (
	"context"
	"time"
)

// DAClient is the interface for interacting with a Data Availability layer
// (e.g. Celestia) from the SDK. It provides namespace-aware blob operations
// and subscription support, enabling each app-chain to isolate its data
// in a dedicated namespace.
//
// Implementations:
//   - Production: CelestiaDAClient (wraps pkg/da/jsonrpc)
//   - Testing:    MockDAClient (in-memory, no network)
type DAClient interface {
	// SubmitBlobs submits one or more blobs to the DA layer under the given namespace.
	// Returns the DA height at which blobs were included.
	SubmitBlobs(ctx context.Context, namespace *Namespace, blobs [][]byte, opts *DASubmitOptions) (*DASubmitResult, error)

	// GetBlobs retrieves all blobs at a given DA height for the specified namespace.
	GetBlobs(ctx context.Context, namespace *Namespace, height uint64) ([]*DABlob, error)

	// GetBlobByCommitment retrieves a specific blob by its commitment at a given height.
	GetBlobByCommitment(ctx context.Context, namespace *Namespace, height uint64, commitment []byte) (*DABlob, error)

	// Subscribe returns a channel that receives blob events for the given namespace.
	// The channel is closed when ctx is cancelled.
	// This enables app-chains to watch for incoming blobs in real-time.
	Subscribe(ctx context.Context, namespace *Namespace) (<-chan *DABlobEvent, error)

	// GetHeight returns the latest DA layer height.
	GetHeight(ctx context.Context) (uint64, error)
}

// DASubmitOptions controls blob submission parameters.
type DASubmitOptions struct {
	// GasPrice is the gas price for the DA transaction (e.g. uTIA/gas on Celestia).
	// Zero uses the node default.
	GasPrice float64
	// GasLimit overrides the gas estimate. Zero means auto-estimate.
	GasLimit uint64
}

// DASubmitResult is returned after successfully submitting blobs to the DA layer.
type DASubmitResult struct {
	// Height is the DA layer height at which blobs were included.
	Height uint64
	// Namespace is the namespace the blobs were submitted under.
	Namespace *Namespace
	// BlobCount is the number of blobs submitted.
	BlobCount int
	// TotalSize is the total size in bytes of all submitted blobs.
	TotalSize int
}

// DABlob represents a blob retrieved from the DA layer.
type DABlob struct {
	// Namespace is the blob's namespace.
	Namespace *Namespace
	// Data is the raw blob payload.
	Data []byte
	// Commitment is the blob's cryptographic commitment (Merkle subtree root).
	Commitment []byte
	// Height is the DA height at which this blob was included.
	Height uint64
	// Index is the blob's position within the namespace at this height.
	Index int
}

// DABlobEvent is emitted by Subscribe when new blobs are finalized on the DA layer.
type DABlobEvent struct {
	// Height is the DA layer height.
	Height uint64
	// Blobs are the blobs found in the subscribed namespace at this height.
	Blobs []*DABlob
	// Timestamp is the DA block timestamp.
	Timestamp time.Time
}

// DANamespaceConfig holds configuration for a namespace-aware DA connection.
// This is typically set once per app-chain and reused across all DA operations.
type DANamespaceConfig struct {
	// Namespace is the app-chain's dedicated namespace on the DA layer.
	Namespace *Namespace
	// DANodeAddr is the address of the DA node RPC (e.g. "http://localhost:26658").
	DANodeAddr string
	// AuthToken is the bearer token for DA node authentication.
	AuthToken string
	// SubmitOptions are default options for blob submissions.
	SubmitOptions *DASubmitOptions
}

// Validate checks that the config has required fields set.
func (c *DANamespaceConfig) Validate() error {
	if c.Namespace == nil {
		return sdkErr("DANamespaceConfig.Validate", ErrContractMissing,
			"set Namespace — use NamespaceFromString(\"my-app\") for a deterministic namespace")
	}
	if c.DANodeAddr == "" {
		return sdkErr("DANamespaceConfig.Validate", ErrNotReachable,
			"set DANodeAddr to your Celestia node RPC address (e.g. http://localhost:26658)")
	}
	return nil
}
