// Package txcodec handles low-level protobuf transaction encoding for CosmWasm
// messages. This is an internal package — external code cannot import it.
// Use the public BuildStoreTx, BuildExecuteTx, etc. functions instead.
package txcodec

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
)

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

	authInfoBytes, err := proto.Marshal(&txv1beta1.AuthInfo{})
	if err != nil {
		return nil, err
	}

	return proto.Marshal(&txv1beta1.TxRaw{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
		Signatures:    nil,
	})
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
