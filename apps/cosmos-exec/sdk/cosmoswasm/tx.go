package cosmoswasm

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
)

func DefaultSender() string {
	return sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20)).String()
}

func BuildStoreTx(wasmByteCode []byte, sender string) ([]byte, error) {
	if len(wasmByteCode) == 0 {
		return nil, errors.New("wasm bytecode cannot be empty")
	}

	sender = withDefaultSender(sender)
	return buildProtoTxBytes(&wasmtypes.MsgStoreCode{
		Sender:       sender,
		WASMByteCode: wasmByteCode,
	})
}

func BuildInstantiateTx(req InstantiateTxRequest) ([]byte, error) {
	if req.CodeID == 0 {
		return nil, errors.New("code id is required")
	}

	msgJSON, err := normalizeJSONMsg(req.Msg)
	if err != nil {
		return nil, fmt.Errorf("invalid instantiate msg: %w", err)
	}

	sender := withDefaultSender(req.Sender)
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "wasm-via-sdk"
	}

	instantiate := &wasmtypes.MsgInstantiateContract{
		Sender: sender,
		CodeID: req.CodeID,
		Label:  label,
		Msg:    msgJSON,
	}

	if admin := strings.TrimSpace(req.Admin); admin != "" {
		instantiate.Admin = admin
	}

	return buildProtoTxBytes(instantiate)
}

func BuildExecuteTx(req ExecuteTxRequest) ([]byte, error) {
	contract := strings.TrimSpace(req.Contract)
	if contract == "" {
		return nil, errors.New("contract is required")
	}

	msgJSON, err := normalizeJSONMsg(req.Msg)
	if err != nil {
		return nil, fmt.Errorf("invalid execute msg: %w", err)
	}

	return buildProtoTxBytes(&wasmtypes.MsgExecuteContract{
		Sender:   withDefaultSender(req.Sender),
		Contract: contract,
		Msg:      msgJSON,
	})
}

func EncodeTxBase64(tx []byte) string {
	return base64.StdEncoding.EncodeToString(tx)
}

func EncodeTxHex(tx []byte) string {
	return hex.EncodeToString(tx)
}

func buildProtoTxBytes(msgs ...sdk.Msg) ([]byte, error) {
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

func normalizeJSONMsg(msg any) ([]byte, error) {
	switch value := msg.(type) {
	case nil:
		return []byte("{}"), nil
	case json.RawMessage:
		return normalizeJSONBytes(value)
	case []byte:
		return normalizeJSONBytes(value)
	case string:
		return normalizeJSONBytes([]byte(value))
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return normalizeJSONBytes(encoded)
	}
}

func normalizeJSONBytes(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("json msg cannot be empty")
	}
	if !json.Valid(trimmed) {
		return nil, errors.New("msg must be valid json")
	}
	return trimmed, nil
}

func withDefaultSender(sender string) string {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return DefaultSender()
	}
	return sender
}
