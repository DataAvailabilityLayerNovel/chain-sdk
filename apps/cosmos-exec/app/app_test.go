package app

import (
	"encoding/json"
	"testing"

	"cosmossdk.io/log"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
)

func TestDefaultGenesisContainsCriticalModules(t *testing.T) {
	application := New(log.NewNopLogger(), dbm.NewMemDB())

	var genesis map[string]json.RawMessage
	if err := json.Unmarshal(application.DefaultGenesis(), &genesis); err != nil {
		t.Fatalf("default genesis json is invalid: %v", err)
	}

	requiredModules := []string{
		authtypes.ModuleName,
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

func TestAppLifecycleSmoke(t *testing.T) {
	application := New(log.NewNopLogger(), dbm.NewMemDB())

	if application.IBCKeeper == nil {
		t.Fatal("ibc keeper is nil")
	}

	application.InitChainWithDefaultGenesis("")

	// In SDK v0.50 with ABCI 2.0, block processing uses FinalizeBlock
	// instead of separate BeginBlock/DeliverTx/EndBlock calls.
	_, err := application.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: 1,
	})
	if err != nil {
		t.Fatalf("finalize block failed: %v", err)
	}

	_, err = application.Commit()
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	appHash := application.CommitMultiStore().LastCommitID().Hash
	if len(appHash) == 0 {
		t.Fatal("commit app hash is empty")
	}
}
