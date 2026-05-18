package app

import (
	"encoding/json"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// GenesisWithBalances returns the default genesis with the given bank balances
// patched into the bank module. It is the supported way to fund a treasury at
// chain init (the codebase has no x/mint or genesis-account config otherwise,
// so this is the only place a fee token's supply comes into existence).
//
// Supply is intentionally left empty: bank's InitGenesis recomputes total
// supply from balances when Supply is absent, which sidesteps the
// "supply must equal sum of balances" invariant that trips up hand-written
// genesis. Balances are appended, so calling this with no balances is a no-op
// equivalent to DefaultGenesis().
func (app *App) GenesisWithBalances(balances []banktypes.Balance) ([]byte, error) {
	var state map[string]json.RawMessage
	if err := json.Unmarshal(app.DefaultGenesis(), &state); err != nil {
		return nil, fmt.Errorf("unmarshal default genesis: %w", err)
	}

	var bankGen banktypes.GenesisState
	if raw, ok := state[banktypes.ModuleName]; ok {
		if err := app.appCodec.UnmarshalJSON(raw, &bankGen); err != nil {
			return nil, fmt.Errorf("unmarshal bank genesis: %w", err)
		}
	}

	bankGen.Balances = append(bankGen.Balances, balances...)
	// Drop any pre-existing supply so InitGenesis recomputes it from balances.
	bankGen.Supply = sdk.NewCoins()

	patched, err := app.appCodec.MarshalJSON(&bankGen)
	if err != nil {
		return nil, fmt.Errorf("marshal bank genesis: %w", err)
	}
	state[banktypes.ModuleName] = patched

	out, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("marshal genesis: %w", err)
	}
	return out, nil
}
