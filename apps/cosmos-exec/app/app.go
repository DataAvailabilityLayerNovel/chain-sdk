package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	wasmmodule "github.com/CosmWasm/wasmd/x/wasm"
	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
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
	capabilitymodule "github.com/cosmos/cosmos-sdk/x/capability"
	capabilitykeeper "github.com/cosmos/cosmos-sdk/x/capability/keeper"
	capabilitytypes "github.com/cosmos/cosmos-sdk/x/capability/types"
	paramsmodule "github.com/cosmos/cosmos-sdk/x/params"
	paramskeeper "github.com/cosmos/cosmos-sdk/x/params/keeper"
	paramtypes "github.com/cosmos/cosmos-sdk/x/params/types"
	ibctransfermodule "github.com/cosmos/ibc-go/v7/modules/apps/transfer"
	ibctransferkeeper "github.com/cosmos/ibc-go/v7/modules/apps/transfer/keeper"
	ibctransfertypes "github.com/cosmos/ibc-go/v7/modules/apps/transfer/types"
	ibcmodule "github.com/cosmos/ibc-go/v7/modules/core"
	porttypes "github.com/cosmos/ibc-go/v7/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v7/modules/core/exported"
	ibckeeper "github.com/cosmos/ibc-go/v7/modules/core/keeper"
)

var (
	maccPerms = map[string][]string{
		authtypes.FeeCollectorName:  nil,
		ibctransfertypes.ModuleName: {authtypes.Minter, authtypes.Burner},
		wasmtypes.ModuleName:        nil,
	}
)

type App struct {
	*baseapp.BaseApp

	appCodec     codec.Codec
	keys         map[string]*storetypes.KVStoreKey
	tkeys        map[string]*storetypes.TransientStoreKey
	memKeys      map[string]*storetypes.MemoryStoreKey
	ModuleBasics module.BasicManager

	ParamsKeeper         paramskeeper.Keeper
	CapabilityKeeper     *capabilitykeeper.Keeper
	ScopedIBCKeeper      capabilitykeeper.ScopedKeeper
	ScopedTransferKeeper capabilitykeeper.ScopedKeeper
	ScopedWasmKeeper     capabilitykeeper.ScopedKeeper
	IBCKeeper            *ibckeeper.Keeper
	TransferKeeper       ibctransferkeeper.Keeper

	AccountKeeper authkeeper.AccountKeeper
	BankKeeper    bankkeeper.BaseKeeper
	WasmKeeper    wasmkeeper.Keeper

	ModuleManager *module.Manager
}

func New(logger log.Logger, database db.DB, chainID ...string) *App {
	interfaceRegistry := types.NewInterfaceRegistry()

	legacyAmino := codec.NewLegacyAmino()

	appCodec := codec.NewProtoCodec(interfaceRegistry)
	moduleBasics := module.NewBasicManager(
		authmodule.AppModuleBasic{},
		bankmodule.AppModuleBasic{},
		paramsmodule.AppModuleBasic{},
		capabilitymodule.NewAppModuleBasic(appCodec),
		ibcmodule.AppModuleBasic{},
		ibctransfermodule.AppModuleBasic{},
		wasmmodule.AppModuleBasic{},
	)

	moduleBasics.RegisterInterfaces(interfaceRegistry)
	std.RegisterInterfaces(interfaceRegistry)
	moduleBasics.RegisterLegacyAminoCodec(legacyAmino)
	std.RegisterLegacyAminoCodec(legacyAmino)

	txConfig := authtx.NewTxConfig(appCodec, authtx.DefaultSignModes)

	var baseOpts []func(*baseapp.BaseApp)
	if len(chainID) > 0 && chainID[0] != "" {
		baseOpts = append(baseOpts, baseapp.SetChainID(chainID[0]))
	}

	base := baseapp.NewBaseApp("cosmos-exec", logger, database, txConfig.TxDecoder(), baseOpts...)
	base.SetInterfaceRegistry(interfaceRegistry)

	keys := sdk.NewKVStoreKeys(
		authtypes.StoreKey,
		banktypes.StoreKey,
		paramtypes.StoreKey,
		capabilitytypes.StoreKey,
		ibcexported.StoreKey,
		ibctransfertypes.StoreKey,
		wasmtypes.StoreKey,
	)
	tkeys := sdk.NewTransientStoreKeys(paramtypes.TStoreKey)
	memKeys := sdk.NewMemoryStoreKeys(capabilitytypes.MemStoreKey)

	for _, key := range keys {
		base.MountStore(key, storetypes.StoreTypeIAVL)
	}
	for _, tkey := range tkeys {
		base.MountStore(tkey, storetypes.StoreTypeTransient)
	}
	for _, memKey := range memKeys {
		base.MountStore(memKey, storetypes.StoreTypeMemory)
	}

	app := &App{
		BaseApp:      base,
		appCodec:     appCodec,
		keys:         keys,
		tkeys:        tkeys,
		memKeys:      memKeys,
		ModuleBasics: moduleBasics,
	}

	app.ParamsKeeper = initParamsKeeper(appCodec, legacyAmino, keys[paramtypes.StoreKey], tkeys[paramtypes.TStoreKey])

	app.CapabilityKeeper = capabilitykeeper.NewKeeper(appCodec, keys[capabilitytypes.StoreKey], memKeys[capabilitytypes.MemStoreKey])
	app.ScopedIBCKeeper = app.CapabilityKeeper.ScopeToModule(ibcexported.ModuleName)
	app.ScopedTransferKeeper = app.CapabilityKeeper.ScopeToModule(ibctransfertypes.ModuleName)
	app.ScopedWasmKeeper = app.CapabilityKeeper.ScopeToModule(wasmtypes.ModuleName)
	app.CapabilityKeeper.Seal()

	ibcSubspace := app.ParamsKeeper.Subspace(ibcexported.ModuleName)
	transferSubspace := app.ParamsKeeper.Subspace(ibctransfertypes.ModuleName)
	wasmSubspace := app.ParamsKeeper.Subspace(wasmtypes.ModuleName)

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

	ibcStakingKeeper := ibcClientStakingKeeper{enabled: true}
	ibcUpgradeKeeper := ibcClientUpgradeKeeper{enabled: true}
	app.IBCKeeper = ibckeeper.NewKeeper(
		appCodec,
		keys[ibcexported.StoreKey],
		ibcSubspace,
		ibcStakingKeeper,
		ibcUpgradeKeeper,
		app.ScopedIBCKeeper,
	)

	app.TransferKeeper = ibctransferkeeper.NewKeeper(
		appCodec,
		keys[ibctransfertypes.StoreKey],
		transferSubspace,
		app.IBCKeeper.ChannelKeeper,
		app.IBCKeeper.ChannelKeeper,
		&app.IBCKeeper.PortKeeper,
		app.AccountKeeper,
		app.BankKeeper,
		app.ScopedTransferKeeper,
	)

	wasmConfig := wasmtypes.DefaultWasmConfig()
	homePath := ".cosmos-exec-wasm"
	availableCapabilities := strings.Join([]string{
		"iterator",
		"staking",
		"stargate",
		"cosmwasm_1_1",
		"cosmwasm_1_2",
		"cosmwasm_1_3",
		"cosmwasm_1_4",
	}, ",")

	stakingKeeper := noopStakingKeeper{}
	distributionKeeper := noopDistributionKeeper{}

	app.WasmKeeper = wasmkeeper.NewKeeper(
		appCodec,
		keys[wasmtypes.StoreKey],
		app.AccountKeeper,
		app.BankKeeper,
		stakingKeeper,
		distributionKeeper,
		app.IBCKeeper.ChannelKeeper,
		app.IBCKeeper.ChannelKeeper,
		&app.IBCKeeper.PortKeeper,
		app.ScopedWasmKeeper,
		app.TransferKeeper,
		app.MsgServiceRouter(),
		app.GRPCQueryRouter(),
		homePath,
		wasmConfig,
		availableCapabilities,
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
	)

	transferStack := ibctransfermodule.NewIBCModule(app.TransferKeeper)
	ibcRouter := porttypes.NewRouter().AddRoute(ibctransfertypes.ModuleName, transferStack)
	app.IBCKeeper.SetRouter(ibcRouter)

	app.ModuleManager = module.NewManager(
		paramsmodule.NewAppModule(app.ParamsKeeper),
		capabilitymodule.NewAppModule(appCodec, *app.CapabilityKeeper, false),
		authmodule.NewAppModule(appCodec, app.AccountKeeper, nil, nil),
		bankmodule.NewAppModule(appCodec, app.BankKeeper, app.AccountKeeper, nil),
		ibcmodule.NewAppModule(app.IBCKeeper),
		ibctransfermodule.NewAppModule(app.TransferKeeper),
		wasmmodule.NewAppModule(appCodec, &app.WasmKeeper, nil, app.AccountKeeper, app.BankKeeper, app.MsgServiceRouter(), wasmSubspace),
	)
	app.ModuleManager.SetOrderInitGenesis(
		paramtypes.ModuleName,
		capabilitytypes.ModuleName,
		authtypes.ModuleName,
		banktypes.ModuleName,
		ibcexported.ModuleName,
		ibctransfertypes.ModuleName,
		wasmtypes.ModuleName,
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

func (app *App) InitChainer(ctx sdk.Context, req abci.RequestInitChain) (resp abci.ResponseInitChain) {
	var genesisState map[string]json.RawMessage
	if len(req.AppStateBytes) == 0 {
		genesisState = app.ModuleBasics.DefaultGenesis(app.appCodec)
	} else {
		if err := json.Unmarshal(req.AppStateBytes, &genesisState); err != nil {
			panic(err)
		}
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			message := fmt.Sprint(recovered)
			if strings.Contains(message, "validator set is empty after InitGenesis") {
				resp = abci.ResponseInitChain{Validators: req.Validators}
				return
			}
			panic(recovered)
		}
	}()

	return app.ModuleManager.InitGenesis(ctx, app.appCodec, genesisState)
}

func (app *App) DefaultGenesis() []byte {
	bz, err := json.Marshal(app.ModuleBasics.DefaultGenesis(app.appCodec))
	if err != nil {
		panic(err)
	}

	return bz
}

func (app *App) InitChainWithDefaultGenesis(chainID string) abci.ResponseInitChain {
	return app.InitChain(abci.RequestInitChain{
		Time:          time.Now(),
		ChainId:       chainID,
		AppStateBytes: app.DefaultGenesis(),
	})
}

func initParamsKeeper(appCodec codec.BinaryCodec, legacyAmino *codec.LegacyAmino, key, tkey storetypes.StoreKey) paramskeeper.Keeper {
	paramsKeeper := paramskeeper.NewKeeper(appCodec, legacyAmino, key, tkey)
	return paramsKeeper
}
