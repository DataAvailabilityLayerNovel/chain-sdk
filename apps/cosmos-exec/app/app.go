package app

import (
	"encoding/json"
	"time"

	db "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	storetypes "github.com/cosmos/cosmos-sdk/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authmodule "github.com/cosmos/cosmos-sdk/x/auth"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankmodule "github.com/cosmos/cosmos-sdk/x/bank"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

var (
	ModuleBasics = module.NewBasicManager(
		authmodule.AppModuleBasic{},
		bankmodule.AppModuleBasic{},
	)

	maccPerms = map[string][]string{
		authtypes.FeeCollectorName: nil,
	}
)

type App struct {
	*baseapp.BaseApp

	appCodec codec.Codec
	keys     map[string]*storetypes.KVStoreKey

	AccountKeeper authkeeper.AccountKeeper
	BankKeeper    bankkeeper.BaseKeeper

	ModuleManager *module.Manager
}

func New(logger log.Logger, database db.DB) *App {
	interfaceRegistry := types.NewInterfaceRegistry()
	ModuleBasics.RegisterInterfaces(interfaceRegistry)
	std.RegisterInterfaces(interfaceRegistry)

	legacyAmino := codec.NewLegacyAmino()
	ModuleBasics.RegisterLegacyAminoCodec(legacyAmino)
	std.RegisterLegacyAminoCodec(legacyAmino)

	appCodec := codec.NewProtoCodec(interfaceRegistry)
	txConfig := authtx.NewTxConfig(appCodec, authtx.DefaultSignModes)

	base := baseapp.NewBaseApp("cosmos-exec", logger, database, txConfig.TxDecoder())
	base.SetInterfaceRegistry(interfaceRegistry)

	keys := sdk.NewKVStoreKeys(
		authtypes.StoreKey,
		banktypes.StoreKey,
	)
	for _, key := range keys {
		base.MountStore(key, storetypes.StoreTypeIAVL)
	}

	app := &App{
		BaseApp:  base,
		appCodec: appCodec,
		keys:     keys,
	}

	app.AccountKeeper = authkeeper.NewAccountKeeper(
		appCodec,
		keys[authtypes.StoreKey],
		authtypes.ProtoBaseAccount,
		maccPerms,
		sdk.GetConfig().GetBech32AccountAddrPrefix(),
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
	)

	blockedAddrs := make(map[string]bool)
	for acc := range maccPerms {
		blockedAddrs[authtypes.NewModuleAddress(acc).String()] = true
	}

	app.BankKeeper = bankkeeper.NewBaseKeeper(
		appCodec,
		keys[banktypes.StoreKey],
		app.AccountKeeper,
		blockedAddrs,
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
	)

	app.ModuleManager = module.NewManager(
		authmodule.NewAppModule(appCodec, app.AccountKeeper, nil, nil),
		bankmodule.NewAppModule(appCodec, app.BankKeeper, app.AccountKeeper, nil),
	)
	app.ModuleManager.SetOrderInitGenesis(
		authtypes.ModuleName,
		banktypes.ModuleName,
	)

	app.ModuleManager.RegisterServices(module.NewConfigurator(app.appCodec, app.MsgServiceRouter(), app.GRPCQueryRouter()))

	app.SetInitChainer(app.InitChainer)
	app.SetBeginBlocker(app.BeginBlocker)
	app.SetEndBlocker(app.EndBlocker)

	if err := app.LoadLatestVersion(); err != nil {
		panic(err)
	}

	return app
}

func (app *App) BeginBlocker(ctx sdk.Context, req abci.RequestBeginBlock) abci.ResponseBeginBlock {
	return app.ModuleManager.BeginBlock(ctx, req)
}

func (app *App) EndBlocker(ctx sdk.Context, req abci.RequestEndBlock) abci.ResponseEndBlock {
	return app.ModuleManager.EndBlock(ctx, req)
}

func (app *App) InitChainer(ctx sdk.Context, req abci.RequestInitChain) abci.ResponseInitChain {
	var genesisState map[string]json.RawMessage
	if len(req.AppStateBytes) == 0 {
		genesisState = ModuleBasics.DefaultGenesis(app.appCodec)
	} else {
		if err := json.Unmarshal(req.AppStateBytes, &genesisState); err != nil {
			panic(err)
		}
	}

	return app.ModuleManager.InitGenesis(ctx, app.appCodec, genesisState)
}

func (app *App) DefaultGenesis() []byte {
	bz, err := json.Marshal(ModuleBasics.DefaultGenesis(app.appCodec))
	if err != nil {
		panic(err)
	}

	return bz
}

func (app *App) InitChainWithDefaultGenesis() abci.ResponseInitChain {
	return app.InitChain(abci.RequestInitChain{
		Time:          time.Now(),
		ChainId:       "cosmos-exec-local",
		AppStateBytes: app.DefaultGenesis(),
	})
}
