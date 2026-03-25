package app

import (
	"encoding/json"
	"testing"
	"time"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	db "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/libs/log"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	paramtypes "github.com/cosmos/cosmos-sdk/x/params/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v7/modules/apps/transfer/types"
	ibcexported "github.com/cosmos/ibc-go/v7/modules/core/exported"
)

func TestDefaultGenesisContainsCriticalModules(t *testing.T) {
	application := New(log.NewNopLogger(), db.NewMemDB())

	var genesis map[string]json.RawMessage
	if err := json.Unmarshal(application.DefaultGenesis(), &genesis); err != nil {
		t.Fatalf("default genesis json is invalid: %v", err)
	}

	requiredModules := []string{
		paramtypes.ModuleName,
		types.ModuleName,
		banktypes.ModuleName,
		ibcexported.ModuleName,
		ibctransfertypes.ModuleName,
		wasmtypes.ModuleName,
	}

	for _, moduleName := range requiredModules {
		if _, exists := genesis[moduleName]; !exists {
			t.Fatalf("default genesis missing module %q", moduleName)
		}
	}
}

func TestAppLifecycleAndIBCRoutingSmoke(t *testing.T) {
	application := New(log.NewNopLogger(), db.NewMemDB())

	if application.IBCKeeper == nil {
		t.Fatal("ibc keeper is nil")
	}
	if application.IBCKeeper.Router == nil {
		t.Fatal("ibc router is nil")
	}
	if !application.IBCKeeper.Router.Sealed() {
		t.Fatal("ibc router is not sealed")
	}
	if !application.IBCKeeper.Router.HasRoute(ibctransfertypes.ModuleName) {
		t.Fatalf("ibc router missing %q route", ibctransfertypes.ModuleName)
	}

	application.InitChainWithDefaultGenesis()

	application.BeginBlock(abci.RequestBeginBlock{
		Header: tmproto.Header{
			Height:  1,
			Time:    time.Now(),
			ChainID: "",
		},
	})
	application.EndBlock(abci.RequestEndBlock{Height: 1})
	commitResp := application.Commit()

	if len(commitResp.Data) == 0 {
		t.Fatal("commit app hash is empty")
	}
}
