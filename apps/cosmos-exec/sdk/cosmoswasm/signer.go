package cosmoswasm

import (
	"context"
	"errors"
	"fmt"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/txcodec"
)

// Signer holds the credentials required to produce a signed tx. Create one
// per user (or per app key) and pass it to BuildStoreTx / BuildInstantiateTx /
// BuildExecuteTx. Sequence is fetched from the chain automatically when the tx
// is built — callers don't need to track it.
type Signer struct {
	privKey cryptotypes.PrivKey
	chainID string
	gasLimit uint64
}

// NewSignerFromHex creates a Signer from a 32-byte hex-encoded secp256k1
// private key and the rollup chain ID.
func NewSignerFromHex(hexPrivKey, chainID string) (*Signer, error) {
	pk, err := txcodec.NewSecp256k1FromHex(hexPrivKey)
	if err != nil {
		return nil, err
	}
	if chainID == "" {
		return nil, errors.New("chainID is required")
	}
	return &Signer{privKey: pk, chainID: chainID, gasLimit: 100_000_000}, nil
}

// WithGasLimit overrides the default per-tx gas limit (100M). Lower it to
// constrain compute; raise it only for unusually heavy WASM uploads.
func (s *Signer) WithGasLimit(limit uint64) *Signer {
	s.gasLimit = limit
	return s
}

// Address returns the signer's bech32 account address.
func (s *Signer) Address() string {
	return sdk.AccAddress(s.privKey.PubKey().Address()).String()
}

// resolveSignerInput queries the chain for account number + sequence and
// packages everything txcodec needs to sign a tx.
func (s *Signer) resolveSignerInput(ctx context.Context, client *Client) (txcodec.SignerInput, error) {
	addr := s.Address()
	info, err := client.FetchAccount(ctx, addr)
	if err != nil {
		return txcodec.SignerInput{}, fmt.Errorf("fetch account %s: %w", addr, err)
	}
	return txcodec.SignerInput{
		PrivKey:       s.privKey,
		ChainID:       s.chainID,
		AccountNumber: info.AccountNumber,
		Sequence:      info.Sequence,
		GasLimit:      s.gasLimit,
	}, nil
}
