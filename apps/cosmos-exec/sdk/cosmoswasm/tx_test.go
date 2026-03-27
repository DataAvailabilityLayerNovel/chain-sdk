package cosmoswasm

import (
	"encoding/base64"
	"testing"

	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
)

func TestBuildStoreTx(t *testing.T) {
	tx, err := BuildStoreTx([]byte{0x00, 0x61, 0x73, 0x6d}, "")
	if err != nil {
		t.Fatalf("build store tx: %v", err)
	}

	parsed := txv1beta1.TxRaw{}
	if err := proto.Unmarshal(tx, &parsed); err != nil {
		t.Fatalf("unmarshal tx raw: %v", err)
	}
	if len(parsed.BodyBytes) == 0 {
		t.Fatal("body bytes should not be empty")
	}
}

func TestBuildInstantiateTxRejectInvalidMsg(t *testing.T) {
	_, err := BuildInstantiateTx(InstantiateTxRequest{
		CodeID: 1,
		Msg:    "{invalid-json}",
	})
	if err == nil {
		t.Fatal("expected invalid json to fail")
	}
}

func TestBuildExecuteTxAndEncode(t *testing.T) {
	tx, err := BuildExecuteTx(ExecuteTxRequest{
		Contract: "cosmos1contract",
		Msg: map[string]any{
			"ping": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("build execute tx: %v", err)
	}

	b64 := EncodeTxBase64(tx)
	if _, err := base64.StdEncoding.DecodeString(b64); err != nil {
		t.Fatalf("encoded tx should be valid base64: %v", err)
	}

	hexValue := EncodeTxHex(tx)
	if len(hexValue) == 0 {
		t.Fatal("hex encoding should not be empty")
	}
}
