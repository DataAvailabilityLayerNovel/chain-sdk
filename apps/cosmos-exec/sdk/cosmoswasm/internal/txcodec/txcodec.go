// Package txcodec handles low-level protobuf transaction encoding for CosmWasm
// messages. This is an internal package — external code cannot import it.
// Use the public BuildStoreTx, BuildExecuteTx, etc. functions instead.
package txcodec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/gogoproto/proto"
)

// SignerInput carries everything required to sign a tx with the SDK direct-mode
// signing scheme.
type SignerInput struct {
	PrivKey       cryptotypes.PrivKey
	ChainID       string
	AccountNumber uint64
	Sequence      uint64
	GasLimit      uint64
	// FeeAmount is the tx fee. Leave nil/empty for a 0-fee tx (dev default);
	// set it to satisfy the ante feePolicy when COSMOS_EXEC_MIN_GAS_PRICE > 0.
	FeeAmount sdk.Coins
}

// Address returns the bech32 account address corresponding to PrivKey.
func (s SignerInput) Address() string {
	return sdk.AccAddress(s.PrivKey.PubKey().Address()).String()
}

// NewSecp256k1FromHex parses a 32-byte hex secret into a secp256k1 PrivKey.
func NewSecp256k1FromHex(hexKey string) (cryptotypes.PrivKey, error) {
	hexKey = strings.TrimSpace(hexKey)
	hexKey = strings.TrimPrefix(hexKey, "0x")
	bz, err := decodeHex(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex private key: %w", err)
	}
	if len(bz) != 32 {
		return nil, fmt.Errorf("private key must be 32 bytes, got %d", len(bz))
	}
	return &secp256k1.PrivKey{Key: bz}, nil
}

// DefaultSender returns a deterministic placeholder sender address.
func DefaultSender() string {
	return sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20)).String()
}

// WithDefaultSender returns sender if non-empty, otherwise DefaultSender().
func WithDefaultSender(sender string) string {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return DefaultSender()
	}
	return sender
}

// BuildProtoTxBytes encodes one or more SDK messages into a TxRaw protobuf byte slice.
func BuildProtoTxBytes(msgs ...sdk.Msg) ([]byte, error) {
	packedMsgs := make([]*codectypes.Any, 0, len(msgs))
	for _, msg := range msgs {
		anyMsg, err := codectypes.NewAnyWithValue(msg)
		if err != nil {
			return nil, err
		}
		packedMsgs = append(packedMsgs, anyMsg)
	}

	bodyBytes, err := proto.Marshal(&txv1beta1.TxBody{Messages: packedMsgs})
	if err != nil {
		return nil, err
	}

	// AuthInfo must carry a non-nil Fee — SDK 0.50's Tx.GetSigners dereferences
	// AuthInfo.Fee.Payer unconditionally and would panic on a nil Fee.
	authInfoBytes, err := proto.Marshal(&txv1beta1.AuthInfo{Fee: &txv1beta1.Fee{}})
	if err != nil {
		return nil, err
	}

	return proto.Marshal(&txv1beta1.TxRaw{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
		Signatures:    nil,
	})
}

// BuildSignedProtoTxBytes builds a fully signed TxRaw using SIGN_MODE_DIRECT.
// The signer's pubkey is embedded in AuthInfo.SignerInfos, and the signature
// is computed over the canonical SignDoc bytes.
func BuildSignedProtoTxBytes(signer SignerInput, msgs ...sdk.Msg) ([]byte, error) {
	if signer.PrivKey == nil {
		return nil, errors.New("signer.PrivKey is required")
	}
	if signer.ChainID == "" {
		return nil, errors.New("signer.ChainID is required")
	}

	packedMsgs := make([]*codectypes.Any, 0, len(msgs))
	for _, msg := range msgs {
		anyMsg, err := codectypes.NewAnyWithValue(msg)
		if err != nil {
			return nil, err
		}
		packedMsgs = append(packedMsgs, anyMsg)
	}

	bodyBytes, err := proto.Marshal(&txv1beta1.TxBody{Messages: packedMsgs})
	if err != nil {
		return nil, err
	}

	pubKeyAny, err := codectypes.NewAnyWithValue(signer.PrivKey.PubKey())
	if err != nil {
		return nil, fmt.Errorf("pack pubkey: %w", err)
	}

	gasLimit := signer.GasLimit
	if gasLimit == 0 {
		gasLimit = 100_000_000 // generous default — wasm uploads are expensive
	}

	authInfo := &txv1beta1.AuthInfo{
		SignerInfos: []*txv1beta1.SignerInfo{
			{
				PublicKey: pubKeyAny,
				ModeInfo: &txv1beta1.ModeInfo{
					Sum: &txv1beta1.ModeInfo_Single_{
						Single: &txv1beta1.ModeInfo_Single{Mode: signingtypes.SignMode_SIGN_MODE_DIRECT},
					},
				},
				Sequence: signer.Sequence,
			},
		},
		Fee: &txv1beta1.Fee{GasLimit: gasLimit, Amount: signer.FeeAmount},
	}
	authInfoBytes, err := proto.Marshal(authInfo)
	if err != nil {
		return nil, err
	}

	signDoc := &txv1beta1.SignDoc{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
		ChainId:       signer.ChainID,
		AccountNumber: signer.AccountNumber,
	}
	signBytes, err := proto.Marshal(signDoc)
	if err != nil {
		return nil, err
	}

	sig, err := signer.PrivKey.Sign(signBytes)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	return proto.Marshal(&txv1beta1.TxRaw{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
		Signatures:    [][]byte{sig},
	})
}

// decodeHex parses a hex string (case-insensitive, no 0x prefix) into bytes.
func decodeHex(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("hex length must be even")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, err := hexNibble(s[2*i])
		if err != nil {
			return nil, err
		}
		lo, err := hexNibble(s[2*i+1])
		if err != nil {
			return nil, err
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("invalid hex char %q", c)
}

// NormalizeJSONMsg converts various Go types into a valid JSON byte slice
// suitable for a CosmWasm message field.
func NormalizeJSONMsg(msg any) ([]byte, error) {
	switch value := msg.(type) {
	case nil:
		return []byte("{}"), nil
	case json.RawMessage:
		return NormalizeJSONBytes(value)
	case []byte:
		return NormalizeJSONBytes(value)
	case string:
		return NormalizeJSONBytes([]byte(value))
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return NormalizeJSONBytes(encoded)
	}
}

// NormalizeJSONBytes validates and trims whitespace from raw JSON.
func NormalizeJSONBytes(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("json msg cannot be empty")
	}
	if !json.Valid(trimmed) {
		return nil, errors.New("msg must be valid json")
	}
	return trimmed, nil
}
