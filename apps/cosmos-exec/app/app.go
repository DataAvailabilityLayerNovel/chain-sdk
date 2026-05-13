package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	"cosmossdk.io/x/tx/signing"
	wasmmodule "github.com/CosmWasm/wasmd/x/wasm"
	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authmodule "github.com/cosmos/cosmos-sdk/x/auth"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	authcodec "github.com/cosmos/cosmos-sdk/x/auth/codec"
	bankmodule "github.com/cosmos/cosmos-sdk/x/bank"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/consensus"
	consensuskeeper "github.com/cosmos/cosmos-sdk/x/consensus/keeper"
	consensustypes "github.com/cosmos/cosmos-sdk/x/consensus/types"
	paramsmodule "github.com/cosmos/cosmos-sdk/x/params"
	paramskeeper "github.com/cosmos/cosmos-sdk/x/params/keeper"
	paramtypes "github.com/cosmos/cosmos-sdk/x/params/types"
	capabilitymodule "github.com/cosmos/ibc-go/modules/capability"
	capabilitykeeper "github.com/cosmos/ibc-go/modules/capability/keeper"
	capabilitytypes "github.com/cosmos/ibc-go/modules/capability/types"
	ibctransfermodule "github.com/cosmos/ibc-go/v8/modules/apps/transfer"
	ibctransferkeeper "github.com/cosmos/ibc-go/v8/modules/apps/transfer/keeper"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	ibcmodule "github.com/cosmos/ibc-go/v8/modules/core"
	porttypes "github.com/cosmos/ibc-go/v8/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibckeeper "github.com/cosmos/ibc-go/v8/modules/core/keeper"
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
	ConsensusKeeper      consensuskeeper.Keeper
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

func New(logger log.Logger, database dbm.DB, chainID ...string) *App {
	// SDK 0.50+ requires explicit address codecs on the InterfaceRegistry,
	// otherwise tx signing/decoding fails with
	// "InterfaceRegistry requires a proper address codec implementation".
	cfg := sdk.GetConfig()
	interfaceRegistry, err := types.NewInterfaceRegistryWithOptions(types.InterfaceRegistryOptions{
		ProtoFiles: gogoproto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          authcodec.NewBech32Codec(cfg.GetBech32AccountAddrPrefix()),
			ValidatorAddressCodec: authcodec.NewBech32Codec(cfg.GetBech32ValidatorAddrPrefix()),
		},
	})
	if err != nil {
		panic(fmt.Errorf("init InterfaceRegistry: %w", err))
	}

	legacyAmino := codec.NewLegacyAmino()

	appCodec := codec.NewProtoCodec(interfaceRegistry)
	moduleBasics := module.NewBasicManager(
		authmodule.AppModuleBasic{},
		bankmodule.AppModuleBasic{},
		paramsmodule.AppModuleBasic{},
		capabilitymodule.AppModuleBasic{},
		consensus.AppModuleBasic{},
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

	keys := storetypes.NewKVStoreKeys(
		authtypes.StoreKey,
		banktypes.StoreKey,
		paramtypes.StoreKey,
		consensustypes.StoreKey,
		capabilitytypes.StoreKey,
		ibcexported.StoreKey,
		ibctransfertypes.StoreKey,
		wasmtypes.StoreKey,
	)
	tkeys := storetypes.NewTransientStoreKeys(paramtypes.TStoreKey)
	memKeys := storetypes.NewMemoryStoreKeys(capabilitytypes.MemStoreKey)

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

	app.ConsensusKeeper = consensuskeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[consensustypes.StoreKey]),
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
		runtime.EventService{},
	)

	app.CapabilityKeeper = capabilitykeeper.NewKeeper(appCodec, keys[capabilitytypes.StoreKey], memKeys[capabilitytypes.MemStoreKey])
	app.ScopedIBCKeeper = app.CapabilityKeeper.ScopeToModule(ibcexported.ModuleName)
	app.ScopedTransferKeeper = app.CapabilityKeeper.ScopeToModule(ibctransfertypes.ModuleName)
	app.ScopedWasmKeeper = app.CapabilityKeeper.ScopeToModule(wasmtypes.ModuleName)
	app.CapabilityKeeper.Seal()

	app.AccountKeeper = authkeeper.NewAccountKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[authtypes.StoreKey]),
		authtypes.ProtoBaseAccount,
		maccPerms,
		authcodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		sdk.GetConfig().GetBech32AccountAddrPrefix(),
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
	)

	blockedAddrs := make(map[string]bool)
	for acc := range maccPerms {
		blockedAddrs[authtypes.NewModuleAddress(acc).String()] = true
	}

	app.BankKeeper = bankkeeper.NewBaseKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[banktypes.StoreKey]),
		app.AccountKeeper,
		blockedAddrs,
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
		logger,
	)

	ibcStakingKeeper := ibcClientStakingKeeper{enabled: true}
	ibcUpgradeKeeper := ibcClientUpgradeKeeper{enabled: true}
	app.IBCKeeper = ibckeeper.NewKeeper(
		appCodec,
		keys[ibcexported.StoreKey],
		app.GetSubspace(ibcexported.ModuleName),
		ibcStakingKeeper,
		ibcUpgradeKeeper,
		app.ScopedIBCKeeper,
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
	)

	app.TransferKeeper = ibctransferkeeper.NewKeeper(
		appCodec,
		keys[ibctransfertypes.StoreKey],
		app.GetSubspace(ibctransfertypes.ModuleName),
		app.IBCKeeper.ChannelKeeper,
		app.IBCKeeper.ChannelKeeper,
		app.IBCKeeper.PortKeeper,
		app.AccountKeeper,
		app.BankKeeper,
		app.ScopedTransferKeeper,
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
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
		runtime.NewKVStoreService(keys[wasmtypes.StoreKey]),
		app.AccountKeeper,
		app.BankKeeper,
		stakingKeeper,
		distributionKeeper,
		app.IBCKeeper.ChannelKeeper,
		app.IBCKeeper.ChannelKeeper,
		app.IBCKeeper.PortKeeper,
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
		consensus.NewAppModule(appCodec, app.ConsensusKeeper),
		authmodule.NewAppModule(appCodec, app.AccountKeeper, nil, app.GetSubspace(authtypes.ModuleName)),
		bankmodule.NewAppModule(appCodec, app.BankKeeper, app.AccountKeeper, app.GetSubspace(banktypes.ModuleName)),
		ibcmodule.NewAppModule(app.IBCKeeper),
		ibctransfermodule.NewAppModule(app.TransferKeeper),
		wasmmodule.NewAppModule(appCodec, &app.WasmKeeper, stakingKeeper, app.AccountKeeper, app.BankKeeper, app.MsgServiceRouter(), app.GetSubspace(wasmtypes.ModuleName)),
	)
	app.ModuleManager.SetOrderInitGenesis(
		paramtypes.ModuleName,
		capabilitytypes.ModuleName,
		authtypes.ModuleName,
		banktypes.ModuleName,
		consensustypes.ModuleName,
		ibcexported.ModuleName,
		ibctransfertypes.ModuleName,
		wasmtypes.ModuleName,
	)

	app.ModuleManager.RegisterServices(module.NewConfigurator(app.appCodec, app.MsgServiceRouter(), app.GRPCQueryRouter()))

	app.SetInitChainer(app.InitChainer)
	app.SetBeginBlocker(app.BeginBlocker)
	app.SetEndBlocker(app.EndBlocker)

	// AnteHandler: verify signatures + sequence + tx-size gas. Opt-in via
	// COSMOS_EXEC_ENFORCE_SIGNATURES=true so existing tests and dev flows that
	// submit unsigned txs keep working by default. Production / public-submit
	// setups should set this env var. See app/ante.go.
	if enforceSignaturesEnv() {
		base.SetAnteHandler(NewPermissionlessAnteHandler(
			app.AccountKeeper,
			app.BankKeeper,
			txConfig.SignModeHandler(),
		))
	}

	if err := app.LoadLatestVersion(); err != nil {
		panic(err)
	}

	return app
}

func (app *App) BeginBlocker(ctx sdk.Context) (sdk.BeginBlock, error) {
	return app.ModuleManager.BeginBlock(ctx)
}

func (app *App) EndBlocker(ctx sdk.Context) (sdk.EndBlock, error) {
	return app.ModuleManager.EndBlock(ctx)
}

func (app *App) InitChainer(ctx sdk.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	var genesisState map[string]json.RawMessage
	if len(req.AppStateBytes) == 0 {
		genesisState = app.ModuleBasics.DefaultGenesis(app.appCodec)
	} else {
		if err := json.Unmarshal(req.AppStateBytes, &genesisState); err != nil {
			return nil, err
		}
	}

	resp, err := app.ModuleManager.InitGenesis(ctx, app.appCodec, genesisState)
	if err != nil {
		// Recover from "validator set is empty" panic-as-error
		if strings.Contains(err.Error(), "validator set is empty after InitGenesis") {
			return &abci.ResponseInitChain{Validators: req.Validators}, nil
		}
		return nil, err
	}

	return resp, nil
}

func (app *App) DefaultGenesis() []byte {
	bz, err := json.Marshal(app.ModuleBasics.DefaultGenesis(app.appCodec))
	if err != nil {
		panic(err)
	}

	return bz
}

func (app *App) InitChainWithDefaultGenesis(chainID string) *abci.ResponseInitChain {
	resp, err := app.InitChain(&abci.RequestInitChain{
		Time:          time.Now(),
		ChainId:       chainID,
		AppStateBytes: app.DefaultGenesis(),
	})
	if err != nil {
		panic(fmt.Sprintf("init chain failed: %v", err))
	}
	return resp
}

func (app *App) GetSubspace(moduleName string) paramtypes.Subspace {
	subspace, _ := app.ParamsKeeper.GetSubspace(moduleName)
	return subspace
}

func initParamsKeeper(appCodec codec.BinaryCodec, legacyAmino *codec.LegacyAmino, key, tkey storetypes.StoreKey) paramskeeper.Keeper {
	paramsKeeper := paramskeeper.NewKeeper(appCodec, legacyAmino, key, tkey)
	return paramsKeeper
}
