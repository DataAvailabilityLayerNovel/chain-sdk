package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	db "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"

	"github.com/CosmWasm/wasmd/x/wasm/keeper/testdata"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	"github.com/evstack/ev-node/apps/cosmos-exec/app"
	"github.com/evstack/ev-node/core/execution"
)

func TestCosmosExecutorLifecycle(t *testing.T) {
	ctx := context.Background()
	exec := New(app.New(log.NewNopLogger(), db.NewMemDB()))

	stateRoot, err := exec.InitChain(ctx, time.Now(), 1, "cosmos-exec-local")
	if err != nil {
		t.Fatalf("init chain failed: %v", err)
	}
	if len(stateRoot) == 0 {
		t.Fatal("init state root is empty")
	}

	stateRootAgain, err := exec.InitChain(ctx, time.Now(), 1, "cosmos-exec-local")
	if err != nil {
		t.Fatalf("re-init with same chain id should succeed: %v", err)
	}
	if len(stateRootAgain) == 0 {
		t.Fatal("re-init state root is empty")
	}

	if _, err := exec.InitChain(ctx, time.Now(), 1, "other-chain"); err == nil {
		t.Fatal("expected re-init with different chain id to fail")
	}

	nextRoot, err := exec.ExecuteTxs(ctx, nil, 1, time.Now(), stateRoot)
	if err != nil {
		t.Fatalf("execute txs failed: %v", err)
	}
	if len(nextRoot) == 0 {
		t.Fatal("next state root is empty")
	}

	if err := exec.SetFinal(ctx, 1); err != nil {
		t.Fatalf("set final failed: %v", err)
	}

	if err := exec.SetFinal(ctx, 2); err == nil {
		t.Fatal("expected finalizing future height to fail")
	}

	info, err := exec.GetExecutionInfo(ctx)
	if err != nil {
		t.Fatalf("get execution info failed: %v", err)
	}
	if info.MaxGas != 0 {
		t.Fatalf("unexpected max gas: %d", info.MaxGas)
	}
}

func TestCosmosExecutorValidation(t *testing.T) {
	ctx := context.Background()
	exec := New(app.New(log.NewNopLogger(), db.NewMemDB()))

	stateRoot, err := exec.InitChain(ctx, time.Now(), 1, "cosmos-exec-local")
	if err != nil {
		t.Fatalf("init chain failed: %v", err)
	}

	if _, err := exec.ExecuteTxs(ctx, nil, 1, time.Now(), []byte("wrong-root")); err == nil {
		t.Fatal("expected execute with wrong prev state root to fail")
	}

	if _, err := exec.ExecuteTxs(ctx, nil, 2, time.Now(), stateRoot); err == nil {
		t.Fatal("expected execute with wrong height to fail")
	}
}

func TestCosmosExecutorFilterTxs(t *testing.T) {
	ctx := context.Background()
	exec := New(app.New(log.NewNopLogger(), db.NewMemDB()))

	txs := [][]byte{
		[]byte{0x01},
		{},
		[]byte{0x02, 0x03, 0x04},
	}

	statuses, err := exec.FilterTxs(ctx, txs, 3, 0, false)
	if err != nil {
		t.Fatalf("filter txs failed: %v", err)
	}

	if len(statuses) != len(txs) {
		t.Fatalf("unexpected statuses length: got %d want %d", len(statuses), len(txs))
	}

	if statuses[0] != execution.FilterOK {
		t.Fatalf("unexpected status for tx[0]: %v", statuses[0])
	}
	if statuses[1] != execution.FilterRemove {
		t.Fatalf("unexpected status for tx[1]: %v", statuses[1])
	}
	if statuses[2] != execution.FilterPostpone {
		t.Fatalf("unexpected status for tx[2]: %v", statuses[2])
	}
}

func TestCosmosExecutorExecuteTxsWasmLifecycle(t *testing.T) {
	ctx := context.Background()
	exec := New(app.New(log.NewNopLogger(), db.NewMemDB()))

	sender := sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20))
	newOwner := sdk.AccAddress(bytes.Repeat([]byte{0x22}, 20))

	stateRoot, err := exec.InitChain(ctx, time.Now(), 1, "cosmos-exec-local")
	if err != nil {
		t.Fatalf("init chain failed: %v", err)
	}

	storeTx, err := buildProtoTxBytes(&wasmtypes.MsgStoreCode{
		Sender:       sender.String(),
		WASMByteCode: testdata.ReflectContractWasm(),
	})
	if err != nil {
		t.Fatalf("build store tx failed: %v", err)
	}

	stateRoot1, err := exec.ExecuteTxs(ctx, [][]byte{storeTx}, 1, time.Now(), stateRoot)
	if err != nil {
		t.Fatalf("execute store tx failed: %v", err)
	}
	if bytes.Equal(stateRoot, stateRoot1) {
		t.Fatal("state root did not change after store tx")
	}

	instantiateTx, err := buildProtoTxBytes(&wasmtypes.MsgInstantiateContract{
		Sender: sender.String(),
		CodeID: 1,
		Label:  "reflect-through-executor",
		Msg:    []byte("{}"),
		Funds:  nil,
	})
	if err != nil {
		t.Fatalf("build instantiate tx failed: %v", err)
	}

	stateRoot2, err := exec.ExecuteTxs(ctx, [][]byte{instantiateTx}, 2, time.Now(), stateRoot1)
	if err != nil {
		t.Fatalf("execute instantiate tx failed: %v", err)
	}
	if bytes.Equal(stateRoot1, stateRoot2) {
		t.Fatal("state root did not change after instantiate tx")
	}

	execMsg, err := json.Marshal(testdata.ReflectHandleMsg{
		ChangeOwner: &testdata.OwnerPayload{Owner: newOwner},
	})
	if err != nil {
		t.Fatalf("marshal execute msg failed: %v", err)
	}

	executeTx, err := buildProtoTxBytes(&wasmtypes.MsgExecuteContract{
		Sender:   sender.String(),
		Contract: "cosmos1m8h8f5f3z62j3rf0x4exr8af9z8r7j3m8qk5nn",
		Msg:      execMsg,
		Funds:    nil,
	})
	if err != nil {
		t.Fatalf("build execute tx failed: %v", err)
	}

	if _, err := exec.ExecuteTxs(ctx, [][]byte{executeTx}, 3, time.Now(), stateRoot2); err == nil {
		t.Fatal("expected execute tx with invalid contract address to fail")
	}
}

func buildProtoTxBytes(msgs ...sdk.Msg) ([]byte, error) {
	packedMsgs := make([]*types.Any, 0, len(msgs))
	for _, msg := range msgs {
		anyMsg, err := types.NewAnyWithValue(msg)
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
