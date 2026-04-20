package app

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	db "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/libs/log"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	"github.com/CosmWasm/wasmd/x/wasm/keeper/testdata"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
)

func TestReflectContractLifecycle(t *testing.T) {
	application := New(log.NewNopLogger(), db.NewMemDB())
	application.InitChainWithDefaultGenesis("")

	ctx := application.BaseApp.NewContext(false, tmproto.Header{
		Height:  1,
		Time:    time.Now(),
		ChainID: "",
	})

	msgServer := wasmkeeper.NewMsgServerImpl(&application.WasmKeeper)
	sender := sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20))
	newOwner := sdk.AccAddress(bytes.Repeat([]byte{0x22}, 20))

	storeResp, err := msgServer.StoreCode(sdk.WrapSDKContext(ctx), &wasmtypes.MsgStoreCode{
		Sender:       sender.String(),
		WASMByteCode: testdata.ReflectContractWasm(),
	})
	if err != nil {
		t.Fatalf("store code failed: %v", err)
	}
	if storeResp.CodeID == 0 {
		t.Fatal("store code returned empty code id")
	}

	instantiateResp, err := msgServer.InstantiateContract(sdk.WrapSDKContext(ctx), &wasmtypes.MsgInstantiateContract{
		Sender: sender.String(),
		CodeID: storeResp.CodeID,
		Label:  "reflect-lifecycle",
		Msg:    []byte("{}"),
		Funds:  nil,
	})
	if err != nil {
		t.Fatalf("instantiate contract failed: %v", err)
	}
	if instantiateResp.Address == "" {
		t.Fatal("instantiate contract returned empty address")
	}

	contractAddr, err := sdk.AccAddressFromBech32(instantiateResp.Address)
	if err != nil {
		t.Fatalf("invalid contract address: %v", err)
	}

	queryOwnerMsg, err := json.Marshal(testdata.ReflectQueryMsg{Owner: &struct{}{}})
	if err != nil {
		t.Fatalf("marshal owner query failed: %v", err)
	}

	queryBeforeResp, err := application.WasmKeeper.QuerySmart(ctx, contractAddr, queryOwnerMsg)
	if err != nil {
		t.Fatalf("query owner before execute failed: %v", err)
	}

	var ownerBefore testdata.OwnerResponse
	if err := json.Unmarshal(queryBeforeResp, &ownerBefore); err != nil {
		t.Fatalf("unmarshal owner before failed: %v", err)
	}
	if ownerBefore.Owner != sender.String() {
		t.Fatalf("unexpected initial owner: got %q want %q", ownerBefore.Owner, sender.String())
	}

	execMsg, err := json.Marshal(testdata.ReflectHandleMsg{
		ChangeOwner: &testdata.OwnerPayload{Owner: newOwner},
	})
	if err != nil {
		t.Fatalf("marshal execute message failed: %v", err)
	}

	if _, err := msgServer.ExecuteContract(sdk.WrapSDKContext(ctx), &wasmtypes.MsgExecuteContract{
		Sender:   sender.String(),
		Contract: instantiateResp.Address,
		Msg:      execMsg,
		Funds:    nil,
	}); err != nil {
		t.Fatalf("execute contract failed: %v", err)
	}

	queryAfterResp, err := application.WasmKeeper.QuerySmart(ctx, contractAddr, queryOwnerMsg)
	if err != nil {
		t.Fatalf("query owner after execute failed: %v", err)
	}

	var ownerAfter testdata.OwnerResponse
	if err := json.Unmarshal(queryAfterResp, &ownerAfter); err != nil {
		t.Fatalf("unmarshal owner after failed: %v", err)
	}
	if ownerAfter.Owner != newOwner.String() {
		t.Fatalf("owner not updated: got %q want %q", ownerAfter.Owner, newOwner.String())
	}
}
