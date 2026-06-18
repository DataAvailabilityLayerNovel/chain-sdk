// package app: file này định nghĩa "ứng dụng blockchain" — gom mọi module
// (auth, bank, ibc, wasm...) và keeper lại thành 1 app Cosmos SDK hoàn chỉnh.
package app

import (
	"encoding/json" // mã hoá/giải mã JSON — dùng cho genesis state.
	"fmt"           // định dạng chuỗi và tạo lỗi (fmt.Errorf).
	"strings"       // xử lý chuỗi (Join, Contains...).
	"time"          // lấy thời gian hiện tại (time.Now) cho init chain.

	"cosmossdk.io/log"                      // logger có cấu trúc của Cosmos.
	storetypes "cosmossdk.io/store/types"   // kiểu store key (KV, transient, memory).
	"cosmossdk.io/x/tx/signing"             // tuỳ chọn ký giao dịch (sign options).
	wasmmodule "github.com/CosmWasm/wasmd/x/wasm"        // module CosmWasm (hợp đồng thông minh).
	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper" // keeper xử lý wasm.
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"   // type/hằng số của wasm.
	abci "github.com/cometbft/cometbft/abci/types"       // type ABCI (giao tiếp consensus<->app).
	dbm "github.com/cosmos/cosmos-db"                    // interface database (KV store).
	gogoproto "github.com/cosmos/gogoproto/proto"        // resolver protobuf.
	"github.com/cosmos/cosmos-sdk/baseapp"               // BaseApp: lõi ABCI app.
	"github.com/cosmos/cosmos-sdk/codec"                 // codec mã hoá nhị phân.
	"github.com/cosmos/cosmos-sdk/codec/types"           // InterfaceRegistry.
	"github.com/cosmos/cosmos-sdk/runtime"               // tiện ích runtime (KVStoreService).
	"github.com/cosmos/cosmos-sdk/std"                   // đăng ký type chuẩn.
	sdk "github.com/cosmos/cosmos-sdk/types"             // type lõi (Context, Config...).
	"github.com/cosmos/cosmos-sdk/types/module"          // quản lý module (Manager).
	authmodule "github.com/cosmos/cosmos-sdk/x/auth"     // module auth (tài khoản).
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"      // cấu hình tx (TxConfig).
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	authcodec "github.com/cosmos/cosmos-sdk/x/auth/codec" // codec địa chỉ bech32.
	bankmodule "github.com/cosmos/cosmos-sdk/x/bank"      // module bank (số dư token).
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/consensus"            // module tham số consensus.
	consensuskeeper "github.com/cosmos/cosmos-sdk/x/consensus/keeper"
	consensustypes "github.com/cosmos/cosmos-sdk/x/consensus/types"
	paramsmodule "github.com/cosmos/cosmos-sdk/x/params"  // module params (tham số module).
	paramskeeper "github.com/cosmos/cosmos-sdk/x/params/keeper"
	paramtypes "github.com/cosmos/cosmos-sdk/x/params/types"
	capabilitymodule "github.com/cosmos/ibc-go/modules/capability" // module capability (quyền IBC).
	capabilitykeeper "github.com/cosmos/ibc-go/modules/capability/keeper"
	capabilitytypes "github.com/cosmos/ibc-go/modules/capability/types"
	ibctransfermodule "github.com/cosmos/ibc-go/v8/modules/apps/transfer" // module chuyển token qua IBC.
	ibctransferkeeper "github.com/cosmos/ibc-go/v8/modules/apps/transfer/keeper"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	ibcmodule "github.com/cosmos/ibc-go/v8/modules/core" // module IBC lõi.
	porttypes "github.com/cosmos/ibc-go/v8/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibckeeper "github.com/cosmos/ibc-go/v8/modules/core/keeper"
)

// var (...) : khối khai báo biến cấp package.
var (
	// maccPerms: map ánh xạ "tên module account" -> danh sách quyền của nó.
	// nil = không có quyền đặc biệt; {Minter,Burner} = được đúc/huỷ token.
	maccPerms = map[string][]string{
		authtypes.FeeCollectorName:  nil,                               // ví gom phí: không quyền đặc biệt.
		ibctransfertypes.ModuleName: {authtypes.Minter, authtypes.Burner}, // IBC transfer: đúc/huỷ token.
		wasmtypes.ModuleName:        nil,                               // wasm: không quyền đặc biệt.
	}
)

// App: struct đại diện cho toàn bộ ứng dụng blockchain.
type App struct {
	*baseapp.BaseApp // nhúng (embed) BaseApp → App thừa kế mọi method của nó.

	appCodec     codec.Codec                            // codec mã hoá nhị phân chính.
	keys         map[string]*storetypes.KVStoreKey      // các store bền (ghi xuống đĩa).
	tkeys        map[string]*storetypes.TransientStoreKey // store tạm theo block.
	memKeys      map[string]*storetypes.MemoryStoreKey  // store trong RAM.
	ModuleBasics module.BasicManager                    // gom phần "tĩnh" của các module.

	ParamsKeeper         paramskeeper.Keeper          // quản lý tham số module.
	ConsensusKeeper      consensuskeeper.Keeper       // quản lý tham số consensus.
	CapabilityKeeper     *capabilitykeeper.Keeper     // quản lý capability (quyền IBC).
	ScopedIBCKeeper      capabilitykeeper.ScopedKeeper // capability giới hạn cho IBC.
	ScopedTransferKeeper capabilitykeeper.ScopedKeeper // ... cho transfer.
	ScopedWasmKeeper     capabilitykeeper.ScopedKeeper // ... cho wasm.
	IBCKeeper            *ibckeeper.Keeper            // keeper IBC lõi.
	TransferKeeper       ibctransferkeeper.Keeper     // keeper chuyển token IBC.

	AccountKeeper authkeeper.AccountKeeper // CRUD tài khoản.
	BankKeeper    bankkeeper.BaseKeeper    // số dư & chuyển token.
	WasmKeeper    wasmkeeper.Keeper        // chạy hợp đồng wasm.

	ModuleManager *module.Manager // điều phối vòng đời mọi module.
}

// New: hàm khởi tạo App. Tham số:
//   - logger: nơi ghi log.
//   - database: DB lưu state.
//   - chainID ...string: tham số biến đổi (0 hoặc nhiều) — chain id tuỳ chọn.
//
// Trả về: con trỏ *App đã dựng đầy đủ keeper/module.
func New(logger log.Logger, database dbm.DB, chainID ...string) *App {
	// SDK 0.50+ requires explicit address codecs on the InterfaceRegistry,
	// otherwise tx signing/decoding fails with
	// "InterfaceRegistry requires a proper address codec implementation".
	// VI: SDK 0.50+ bắt buộc khai báo codec địa chỉ rõ ràng, nếu không
	// việc ký/giải mã tx sẽ lỗi.
	cfg := sdk.GetConfig() // lấy cấu hình toàn cục (tiền tố bech32...).
	// Tạo InterfaceRegistry — nơi đăng ký mọi kiểu proto + cách ký.
	interfaceRegistry, err := types.NewInterfaceRegistryWithOptions(types.InterfaceRegistryOptions{
		ProtoFiles: gogoproto.HybridResolver, // nguồn định nghĩa proto.
		SigningOptions: signing.Options{
			// codec chuyển địa chỉ <-> chuỗi bech32 cho account/validator.
			AddressCodec:          authcodec.NewBech32Codec(cfg.GetBech32AccountAddrPrefix()),
			ValidatorAddressCodec: authcodec.NewBech32Codec(cfg.GetBech32ValidatorAddrPrefix()),
		},
	})
	if err != nil {
		// panic: dừng chương trình ngay vì lỗi này không thể phục hồi.
		panic(fmt.Errorf("init InterfaceRegistry: %w", err))
	}

	legacyAmino := codec.NewLegacyAmino() // codec cũ (Amino) cho tương thích.

	appCodec := codec.NewProtoCodec(interfaceRegistry) // codec proto chính.
	// BasicManager: gom phần "tĩnh" (đăng ký codec, genesis mặc định) của các module.
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

	// Đăng ký các interface/codec của mọi module vào registry.
	moduleBasics.RegisterInterfaces(interfaceRegistry)
	std.RegisterInterfaces(interfaceRegistry)
	moduleBasics.RegisterLegacyAminoCodec(legacyAmino)
	std.RegisterLegacyAminoCodec(legacyAmino)

	// TxConfig: cấu hình cách mã hoá/giải mã & ký giao dịch.
	txConfig := authtx.NewTxConfig(appCodec, authtx.DefaultSignModes)

	// baseOpts: danh sách tuỳ chọn truyền vào BaseApp (slice rỗng ban đầu).
	var baseOpts []func(*baseapp.BaseApp)
	// Nếu có truyền chainID và không rỗng → thêm option đặt chain id.
	if len(chainID) > 0 && chainID[0] != "" {
		baseOpts = append(baseOpts, baseapp.SetChainID(chainID[0]))
	}

	// Tạo BaseApp: tên "cosmos-exec", gắn logger/db/bộ giải mã tx + options.
	base := baseapp.NewBaseApp("cosmos-exec", logger, database, txConfig.TxDecoder(), baseOpts...)
	base.SetInterfaceRegistry(interfaceRegistry) // gắn registry vào base.

	// Khai báo các KV store key (mỗi module 1 vùng lưu trữ riêng).
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
	tkeys := storetypes.NewTransientStoreKeys(paramtypes.TStoreKey)        // store tạm.
	memKeys := storetypes.NewMemoryStoreKeys(capabilitytypes.MemStoreKey) // store RAM.

	// "Mount" (gắn) từng store vào base với đúng loại lưu trữ.
	for _, key := range keys {
		base.MountStore(key, storetypes.StoreTypeIAVL) // IAVL: cây Merkle bền.
	}
	for _, tkey := range tkeys {
		base.MountStore(tkey, storetypes.StoreTypeTransient)
	}
	for _, memKey := range memKeys {
		base.MountStore(memKey, storetypes.StoreTypeMemory)
	}

	// Tạo struct App và gán các field cơ bản (composite literal).
	app := &App{
		BaseApp:      base,
		appCodec:     appCodec,
		keys:         keys,
		tkeys:        tkeys,
		memKeys:      memKeys,
		ModuleBasics: moduleBasics,
	}

	// Khởi tạo ParamsKeeper (quản lý tham số có thể chỉnh runtime).
	app.ParamsKeeper = initParamsKeeper(appCodec, legacyAmino, keys[paramtypes.StoreKey], tkeys[paramtypes.TStoreKey])

	// ConsensusKeeper: lưu tham số consensus (block size, gas...).
	app.ConsensusKeeper = consensuskeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[consensustypes.StoreKey]), // bọc store key thành service.
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(), // địa chỉ "authority" (quyền chỉnh).
		runtime.EventService{},
	)

	// CapabilityKeeper: cấp/giới hạn quyền (object capability) cho IBC.
	app.CapabilityKeeper = capabilitykeeper.NewKeeper(appCodec, keys[capabilitytypes.StoreKey], memKeys[capabilitytypes.MemStoreKey])
	app.ScopedIBCKeeper = app.CapabilityKeeper.ScopeToModule(ibcexported.ModuleName)        // capability riêng cho IBC.
	app.ScopedTransferKeeper = app.CapabilityKeeper.ScopeToModule(ibctransfertypes.ModuleName) // ... transfer.
	app.ScopedWasmKeeper = app.CapabilityKeeper.ScopeToModule(wasmtypes.ModuleName)         // ... wasm.
	app.CapabilityKeeper.Seal() // "niêm phong" — không cho scope thêm sau điểm này.

	// AccountKeeper: quản lý tài khoản (đọc/ghi/đúc địa chỉ module).
	app.AccountKeeper = authkeeper.NewAccountKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[authtypes.StoreKey]),
		authtypes.ProtoBaseAccount, // hàm tạo account rỗng mặc định.
		maccPerms,                  // quyền của các module account.
		authcodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		sdk.GetConfig().GetBech32AccountAddrPrefix(),
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
	)

	// blockedAddrs: tập địa chỉ bị cấm nhận token (các module account).
	blockedAddrs := make(map[string]bool)
	for acc := range maccPerms { // duyệt key của map (tên module).
		blockedAddrs[authtypes.NewModuleAddress(acc).String()] = true
	}

	// BankKeeper: quản lý số dư & chuyển token giữa địa chỉ.
	app.BankKeeper = bankkeeper.NewBaseKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[banktypes.StoreKey]),
		app.AccountKeeper, // bank phụ thuộc account keeper.
		blockedAddrs,      // địa chỉ cấm nhận.
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
		logger,
	)

	// Các keeper "rút gọn" thay cho staking/upgrade thật (rollup không cần).
	ibcStakingKeeper := ibcClientStakingKeeper{enabled: true}
	ibcUpgradeKeeper := ibcClientUpgradeKeeper{enabled: true}
	// IBCKeeper: lõi IBC (kết nối, kênh, client).
	app.IBCKeeper = ibckeeper.NewKeeper(
		appCodec,
		keys[ibcexported.StoreKey],
		app.GetSubspace(ibcexported.ModuleName), // subspace tham số IBC.
		ibcStakingKeeper,
		ibcUpgradeKeeper,
		app.ScopedIBCKeeper,
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
	)

	// TransferKeeper: xử lý gói tin chuyển token qua IBC.
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

	wasmConfig := wasmtypes.DefaultWasmConfig() // cấu hình wasm mặc định.
	homePath := ".cosmos-exec-wasm"             // thư mục lưu mã wasm đã build.
	// availableCapabilities: danh sách "năng lực" wasm bật, nối bằng dấu phẩy.
	availableCapabilities := strings.Join([]string{
		"iterator",
		"staking",
		"stargate",
		"cosmwasm_1_1",
		"cosmwasm_1_2",
		"cosmwasm_1_3",
		"cosmwasm_1_4",
	}, ",")

	// Keeper giả cho staking/distribution (rollup không có validator/phần thưởng).
	stakingKeeper := noopStakingKeeper{}
	distributionKeeper := noopDistributionKeeper{}

	// WasmKeeper: nạp & chạy hợp đồng thông minh CosmWasm.
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
		app.MsgServiceRouter(),  // router xử lý message tx.
		app.GRPCQueryRouter(),   // router xử lý truy vấn gRPC.
		homePath,
		wasmConfig,
		availableCapabilities,
		authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
	)

	// transferStack: module IBC xử lý luồng chuyển token.
	transferStack := ibctransfermodule.NewIBCModule(app.TransferKeeper)
	// ibcRouter: định tuyến gói IBC theo tên module (port).
	ibcRouter := porttypes.NewRouter().AddRoute(ibctransfertypes.ModuleName, transferStack)
	app.IBCKeeper.SetRouter(ibcRouter) // gắn router vào IBC keeper.

	// ModuleManager: điều phối vòng đời (genesis, begin/end block) các module.
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
	// Thứ tự chạy InitGenesis — phải đúng thứ tự phụ thuộc (auth trước bank...).
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

	// Đăng ký các service (Msg + Query) của mọi module vào router.
	app.ModuleManager.RegisterServices(module.NewConfigurator(app.appCodec, app.MsgServiceRouter(), app.GRPCQueryRouter()))

	// Gắn các "hook" vòng đời ABCI vào base.
	app.SetInitChainer(app.InitChainer)   // chạy 1 lần khi khởi tạo chain.
	app.SetBeginBlocker(app.BeginBlocker) // chạy đầu mỗi block.
	app.SetEndBlocker(app.EndBlocker)     // chạy cuối mỗi block.

	// AnteHandler: verify signatures + sequence + tx-size gas. Opt-in via
	// COSMOS_EXEC_ENFORCE_SIGNATURES=true so existing tests and dev flows that
	// submit unsigned txs keep working by default. Production / public-submit
	// setups should set this env var. See app/ante.go.
	// VI: chỉ gắn ante handler (kiểm tra chữ ký...) khi ENV bật, để dev/test
	// gửi tx chưa ký vẫn chạy được mặc định.
	if enforceSignaturesEnv() {
		base.SetAnteHandler(NewPermissionlessAnteHandler(
			app.AccountKeeper,
			app.BankKeeper,
			txConfig.SignModeHandler(),
			runtime.NewKVStoreService(keys[wasmtypes.StoreKey]), // wasm tx-counter store
			wasmConfig.SimulationGasLimit,                       // giới hạn gas simulate
		))
	}

	// LoadLatestVersion: nạp state mới nhất từ DB. Lỗi → panic (không chạy được).
	if err := app.LoadLatestVersion(); err != nil {
		panic(err)
	}

	return app // trả con trỏ App đã sẵn sàng.
}

// BeginBlocker: hook chạy đầu mỗi block. Receiver `app *App`.
// Tham số ctx; trả về BeginBlock + error. Uỷ thác cho ModuleManager.
func (app *App) BeginBlocker(ctx sdk.Context) (sdk.BeginBlock, error) {
	return app.ModuleManager.BeginBlock(ctx)
}

// EndBlocker: hook chạy cuối mỗi block. Uỷ thác cho ModuleManager, sau đó
// quét phí gom được trong block về địa chỉ treasury (nếu đã cấu hình).
func (app *App) EndBlocker(ctx sdk.Context) (sdk.EndBlock, error) {
	resp, err := app.ModuleManager.EndBlock(ctx)
	if err != nil {
		return resp, err
	}
	if err := app.sweepFeesToTreasury(ctx); err != nil {
		return resp, err
	}
	return resp, nil
}

// sweepFeesToTreasury moves the full balance of the fee_collector module
// account to the address in COSMOS_EXEC_TREASURY_ADDR at the end of every
// block. When the env var is unset/invalid the function is a no-op and fees
// remain in the collector (preserves dev default).
//
// Runs after ModuleManager.EndBlock so any module that might have minted INTO
// fee_collector during the block is reflected in the swept amount.
//
// VI: chuyển toàn bộ số dư của ví module "fee_collector" về địa chỉ treasury
// cấu hình qua ENV. ENV trống → không làm gì (giữ phí trong collector).
func (app *App) sweepFeesToTreasury(ctx sdk.Context) error {
	treasury := treasuryAddrFromEnv()
	if treasury.Empty() {
		return nil
	}
	feeAddr := app.AccountKeeper.GetModuleAddress(authtypes.FeeCollectorName)
	balance := app.BankKeeper.GetAllBalances(ctx, feeAddr)
	if balance.IsZero() {
		return nil
	}
	return app.BankKeeper.SendCoinsFromModuleToAccount(
		ctx, authtypes.FeeCollectorName, treasury, balance,
	)
}

// InitChainer: chạy 1 lần khi chain khởi tạo (block 0). Nhận RequestInitChain
// (chứa app state ban đầu), trả ResponseInitChain.
func (app *App) InitChainer(ctx sdk.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	// Sentinel writes for stores that are mounted but otherwise unused
	// (params, consensus). Without at least one KV pair, every Commit
	// calls iavl.SaveEmptyRoot, which writes []byte{} — and cosmos-db's
	// GoLevelDB.Get returns nil for both missing keys and empty values,
	// so the next LoadLatestVersion fails with "version does not exist".
	//
	// Write through the commit-level store (not ctx.KVStore, which is the
	// FinalizeBlock cache). The executor force-commits app.cms right
	// after InitChain returns and discards finalizeBlockState before the
	// first ExecuteTxs runs Write(); anything in the cache at that point
	// is lost. The commit store goes straight to the IAVL tree.
	//
	// MUST run before InitGenesis: the "validator set is empty" path
	// below returns early, skipping anything written after it.
	cms := app.CommitMultiStore()
	cms.GetCommitKVStore(app.keys[consensustypes.StoreKey]).Set([]byte("init"), []byte{1})
	cms.GetCommitKVStore(app.keys[paramtypes.StoreKey]).Set([]byte("init"), []byte{1})

	// genesisState: map tên module -> JSON genesis của module đó.
	var genesisState map[string]json.RawMessage
	if len(req.AppStateBytes) == 0 {
		// Không có state truyền vào → dùng genesis mặc định.
		genesisState = app.ModuleBasics.DefaultGenesis(app.appCodec)
	} else {
		// Có → giải mã JSON vào genesisState (truyền con trỏ &).
		if err := json.Unmarshal(req.AppStateBytes, &genesisState); err != nil {
			return nil, err
		}
	}

	// Chạy InitGenesis cho từng module theo thứ tự đã set.
	resp, err := app.ModuleManager.InitGenesis(ctx, app.appCodec, genesisState)
	if err != nil {
		// Recover from "validator set is empty" panic-as-error
		// VI: rollup không có validator set → lỗi này bỏ qua được,
		// trả lại đúng validator có sẵn trong request.
		if strings.Contains(err.Error(), "validator set is empty after InitGenesis") {
			return &abci.ResponseInitChain{Validators: req.Validators}, nil
		}
		return nil, err
	}

	return resp, nil
}

// DefaultGenesis: trả về genesis mặc định dưới dạng []byte JSON.
func (app *App) DefaultGenesis() []byte {
	// json.Marshal: chuyển map genesis thành JSON bytes.
	bz, err := json.Marshal(app.ModuleBasics.DefaultGenesis(app.appCodec))
	if err != nil {
		panic(err) // lỗi mã hoá genesis mặc định là bất thường nghiêm trọng.
	}

	return bz
}

// InitChainWithDefaultGenesis: tiện ích — init chain dùng genesis mặc định.
// Tham số chainID; trả ResponseInitChain.
func (app *App) InitChainWithDefaultGenesis(chainID string) *abci.ResponseInitChain {
	resp, err := app.InitChain(&abci.RequestInitChain{
		Time:          time.Now(),          // thời điểm khởi tạo.
		ChainId:       chainID,
		AppStateBytes: app.DefaultGenesis(), // state ban đầu mặc định.
	})
	if err != nil {
		panic(fmt.Sprintf("init chain failed: %v", err))
	}
	return resp
}

// GetSubspace: lấy "subspace" tham số của 1 module theo tên.
// Subspace = vùng tham số riêng của module trong ParamsKeeper.
func (app *App) GetSubspace(moduleName string) paramtypes.Subspace {
	// `subspace, _ :=` — bỏ qua giá trị trả về thứ hai (bool tồn tại).
	subspace, _ := app.ParamsKeeper.GetSubspace(moduleName)
	return subspace
}

// initParamsKeeper: hàm phụ tạo ParamsKeeper từ codec + store key.
func initParamsKeeper(appCodec codec.BinaryCodec, legacyAmino *codec.LegacyAmino, key, tkey storetypes.StoreKey) paramskeeper.Keeper {
	paramsKeeper := paramskeeper.NewKeeper(appCodec, legacyAmino, key, tkey)
	return paramsKeeper
}
