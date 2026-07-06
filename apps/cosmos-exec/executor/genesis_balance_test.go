package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
)

// Verifies approach A: a treasury balance injected via app.GenesisWithBalances
// is present in the bank genesis with no Supply set (so bank's InitGenesis
// recomputes it instead of tripping the supply-must-equal-balances invariant),
// and that InitChain accepts the patched genesis without error.
func TestWithGenesisFundsTreasury(t *testing.T) {
	ctx := context.Background()
	application := app.New(log.NewNopLogger(), dbm.NewMemDB(), t.TempDir())

	treasury := sdk.AccAddress(bytes.Repeat([]byte{0x42}, 20))
	want := sdk.NewCoins(sdk.NewInt64Coin("ustake", 1_000_000_000_000))

	genesis, err := application.GenesisWithBalances([]banktypes.Balance{
		{Address: treasury.String(), Coins: want},
	})
	if err != nil {
		t.Fatalf("GenesisWithBalances: %v", err)
	}

	// Genesis must carry the treasury balance and no pre-set supply.
	var state map[string]json.RawMessage
	if err := json.Unmarshal(genesis, &state); err != nil {
		t.Fatalf("unmarshal genesis: %v", err)
	}
	var bankGen banktypes.GenesisState
	if err := json.Unmarshal(state[banktypes.ModuleName], &bankGen); err != nil {
		t.Fatalf("unmarshal bank genesis: %v", err)
	}
	found := false
	for _, b := range bankGen.Balances {
		if b.Address == treasury.String() && b.Coins.Equal(want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("treasury balance not in bank genesis: %+v", bankGen.Balances)
	}
	if !sdk.Coins(bankGen.Supply).IsZero() {
		t.Fatalf("supply should be empty so InitGenesis recomputes it, got %s", bankGen.Supply)
	}

	// InitChain must accept the patched genesis — this is what actually runs
	// bank InitGenesis and would fail if the supply invariant were violated.
	exec := New(application, WithGenesis(genesis))
	if _, err := exec.InitChain(ctx, time.Now(), 1, "cosmos-exec-local"); err != nil {
		t.Fatalf("InitChain rejected funded genesis: %v", err)
	}
}

// Without WithGenesis the bank genesis carries no balances (the zero-balance
// default that makes the faucet mandatory once fees are enforced).
func TestDefaultGenesisHasNoBalance(t *testing.T) {
	application := app.New(log.NewNopLogger(), dbm.NewMemDB(), t.TempDir())

	var state map[string]json.RawMessage
	if err := json.Unmarshal(application.DefaultGenesis(), &state); err != nil {
		t.Fatalf("unmarshal default genesis: %v", err)
	}
	var bankGen banktypes.GenesisState
	if err := json.Unmarshal(state[banktypes.ModuleName], &bankGen); err != nil {
		t.Fatalf("unmarshal bank genesis: %v", err)
	}
	if len(bankGen.Balances) != 0 {
		t.Fatalf("default genesis should have no balances, got %+v", bankGen.Balances)
	}
}
