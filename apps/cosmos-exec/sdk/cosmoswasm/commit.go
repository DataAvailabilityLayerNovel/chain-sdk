package cosmoswasm

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// BlobRef identifies a single blob within a committed batch.
type BlobRef struct {
	// Root is the Merkle root of the batch (committed on-chain).
	Root string `json:"root"`
	// Commitment is the SHA-256 of this blob's data.
	Commitment string `json:"commitment"`
	// Index is the zero-based position of this blob in the batch.
	Index int `json:"index"`
}

// CommitReceipt is returned by CommitRoot and CommitCritical.
// It contains everything needed to verify and replay the committed data.
type CommitReceipt struct {
	// Root is the Merkle root submitted to the WASM contract.
	Root string `json:"root"`
	// Refs are per-blob references for retrieval and proof generation.
	Refs []BlobRef `json:"refs"`
	// TxHash is the on-chain transaction hash that recorded the root.
	TxHash string `json:"tx_hash"`
	// Tag is the application-level label passed in the request.
	Tag string `json:"tag,omitempty"`
	// CommittedAt is the wall-clock time of the commit call.
	CommittedAt time.Time `json:"committed_at"`
}

// CommitRootRequest is the input to CommitRoot.
type CommitRootRequest struct {
	// Blobs is the list of raw data payloads (game events, snapshots, logs…).
	Blobs [][]byte
	// Contract is the bech32 address of the WASM contract that records roots.
	Contract string
	// Sender is optional; defaults to DefaultSender.
	Sender string
	// Tag is an optional label stored alongside the root on-chain.
	Tag string
	// Extra holds any extra fields to merge into the on-chain message.
	Extra map[string]any
}

// CommitRoot stores every blob in the executor blob store, computes a Merkle
// root over their SHA-256 commitments, and records the root in a WASM contract
// with a single tiny on-chain transaction.
//
// This is the canonical "cheap DA" pattern for data-heavy dApps:
//
//	N × large blobs → executor blob store (off WASM state)
//	1 × 32-byte root → WASM contract (on-chain, minimal gas)
//
// The returned CommitReceipt contains per-blob BlobRefs for retrieval and
// Merkle inclusion proofs.
func (c *Client) CommitRoot(ctx context.Context, req CommitRootRequest) (*CommitReceipt, error) {
	if len(req.Blobs) == 0 {
		return nil, sdkErr("CommitRoot", errors.New("blobs cannot be empty"),
			"pass at least one blob; for single values use SubmitBlob instead")
	}
	if strings.TrimSpace(req.Contract) == "" {
		return nil, sdkErr("CommitRoot", ErrContractMissing,
			"set CommitRootRequest.Contract to your WASM contract bech32 address")
	}

	// 1. Upload all blobs as a batch → get root + per-blob commitments.
	batchRes, err := c.SubmitBatch(ctx, req.Blobs)
	if err != nil {
		return nil, classifyHTTPError("CommitRoot/SubmitBatch", err)
	}

	// 2. Record the Merkle root in the WASM contract (one small tx).
	tx, err := BuildBatchRootTx(BatchRootTxRequest{
		Sender:   req.Sender,
		Contract: req.Contract,
		Root:     batchRes.Root,
		Count:    batchRes.Count,
		Tag:      req.Tag,
		Extra:    req.Extra,
	})
	if err != nil {
		return nil, sdkErr("CommitRoot/BuildBatchRootTx", err,
			"ensure your contract address is valid bech32")
	}

	submitRes, err := c.SubmitTxBytes(ctx, tx)
	if err != nil {
		return nil, classifyHTTPError("CommitRoot/SubmitTx", err)
	}

	// 3. Build per-blob refs.
	refs := make([]BlobRef, len(batchRes.Commitments))
	for i, commitment := range batchRes.Commitments {
		refs[i] = BlobRef{
			Root:       batchRes.Root,
			Commitment: commitment,
			Index:      i,
		}
	}

	return &CommitReceipt{
		Root:        batchRes.Root,
		Refs:        refs,
		TxHash:      submitRes.Hash,
		Tag:         req.Tag,
		CommittedAt: time.Now().UTC(),
	}, nil
}

// CommitCritical is like CommitRoot but intended for events that must be
// submitted immediately (purchases, rewards, settlements) rather than buffered
// in a BatchBuilder.  It bypasses any pending batch and always flushes inline.
// Semantically identical to CommitRoot — the distinction is in call-site intent.
func (c *Client) CommitCritical(ctx context.Context, req CommitRootRequest) (*CommitReceipt, error) {
	return c.CommitRoot(ctx, req)
}

// SubmitBatch is the low-level call that uploads multiple blobs to the executor
// in a single request and returns the Merkle root + per-blob commitments.
func (c *Client) SubmitBatch(ctx context.Context, blobs [][]byte) (*BlobBatchResponse, error) {
	if len(blobs) == 0 {
		return nil, sdkErr("SubmitBatch", errors.New("blobs cannot be empty"),
			"pass at least one blob")
	}

	encoded := make([]string, len(blobs))
	for i, b := range blobs {
		encoded[i] = base64.StdEncoding.EncodeToString(b)
	}

	res := BlobBatchResponse{}
	if err := c.doJSON(ctx, "POST", blobBatchPath, map[string]any{"blobs_base64": encoded}, &res); err != nil {
		return nil, classifyHTTPError("SubmitBatch", err)
	}

	return &res, nil
}

// GetProof returns a Merkle inclusion proof for the blob at the given index
// within the batch identified by commitments.  commitments must match the
// Refs[*].Commitment slice returned in a CommitReceipt.
func GetProof(commitments []string, leafIndex int) (*MerkleProof, error) {
	return BuildMerkleProof(commitments, leafIndex)
}
