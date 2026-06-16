# Sequencer & Mô hình bảo mật (không có validator set)

Tài liệu này giải thích **vì sao một rollup ev-node không cần validator set kiểu Tendermint**, cái gì thay thế nó, và những đánh đổi bạn nhận khi build dApp trên stack cosmos-exec + cosmoswasm.

> Liên quan: [cosmos-vs-evnode.md](cosmos-vs-evnode.md) (so sánh kiến trúc), [chain-flow.md](chain-flow.md) (vòng đời tx/block), [auto-account-creation.md](auto-account-creation.md) (vì sao tx đầu tiên không cần funding).

## 1. Validator set lo việc gì — và ai lo thay ở đây

Một Cosmos chain truyền thống dùng validator set + Tendermint BFT + staking để lo **ba việc** cùng lúc:

1. **Ordering** — quyết thứ tự tx trong block.
2. **Finality** — chốt block bằng 2/3 phiếu validator.
3. **State correctness** — validator chạy state machine và đồng thuận trên kết quả.

ev-node **tách ba việc này ra**, không gộp vào một validator set:

| Việc | Cosmos thường | ev-node (rollup) |
|------|---------------|------------------|
| Ordering | Validator set + BFT | **Sequencer** (single hoặc based) |
| Data availability + bất biến | Validator set lưu | **Celestia DA** — header blob + data blob được publish lên Celestia |
| State correctness | 2/3 validator vote | **Sovereign verification** — bất kỳ full node nào tải data từ DA về *chạy lại và tự verify*, không cần tin sequencer |

Hệ quả mấu chốt: **sequencer không thể giả mạo state**. Nó chỉ ký `SignedHeader` rồi publish lên DA; full node khác chạy lại tx từ data blob, nếu `app_hash` không khớp thì block bị từ chối ngay. Bỏ validator set **không** đồng nghĩa "phải tin một bên về tính đúng".

## 2. Full node verify như thế nào — chi tiết kỹ thuật

Sovereign verification ở section 1 chỉ là khẩu hiệu — đây là *từng bước cụ thể* một full node làm khi nhận được block. Mọi check đều **không tin sequencer**; sai bất kỳ bước nào → block bị reject, syncer dừng tại height đó và người vận hành biết ngay.

### 2.1 Lấy block từ đâu

Full node có 3 nguồn (`block/internal/syncing/`):

| Nguồn | File | Vai trò |
|---|---|---|
| **DA layer** (Celestia) | `da_retriever.go`, `da_follower.go` | **Source of truth** — block chỉ được commit khi cả header và data blob đã trên DA |
| **P2P gossip** | `p2p_handler.go` | Tăng tốc; nhận trước khi DA xác nhận nhưng vẫn phải đợi DA mới commit |
| **Forced inclusion** | `block/internal/da/forced_inclusion_retriever.go` | Tx user gửi thẳng vào DA bypass sequencer |

Nếu sequencer gossip P2P một block mà không publish DA → block không bao giờ advance trên full node.

### 2.2 Validate header + data (chưa execute)

`Syncer.ValidateBlock` ([block/internal/syncing/syncer.go:837](../../../../../block/internal/syncing/syncer.go#L837)) chạy 2 lớp check rẻ trước khi tốn CPU exec:

**Lớp 1 — `SignedHeader.ValidateBasicWithData(data)`** ([types/signed_header.go:189](../../../../../types/signed_header.go#L189)):

- Field hợp lệ: height > 0, time > 0, chain ID khớp.
- **Chữ ký sequencer hợp lệ trên payload header** — sequencer không thể giả mạo header của người khác, và không ai khác có thể giả mạo header của sequencer.
- `ProposerAddress == Signer.Address` (kẻ ký = kẻ tuyên bố là proposer).
- `data.DACommitment() == header.DataHash` ([types/data.go:68-70](../../../../../types/data.go#L68-L70)) — data blob đúng là cái header cam kết. Sequencer **không thể** "header một đằng, data một nẻo".

**Lớp 2 — `State.AssertValidForNextState(header, data)`** ([types/state.go:59](../../../../../types/state.go#L59)) — chuỗi liên tục:

- `ChainID` khớp.
- `Height == LastBlockHeight + 1` (không nhảy/lùi).
- `Time >= LastBlockTime` (không lùi).
- `LastHeaderHash == hash(header trước đó full node đã commit)` (chain liên tục, không fork).
- **`header.AppHash == state.AppHash`** ([types/state.go:106-108](../../../../../types/state.go#L106-L108)) — chốt chống state-fork: nếu sequencer chạy nhánh state khác thì lệch ngay từ field này.

Bước này không execute, chỉ so field. Chi phí thấp nên chạy đầu tiên.

### 2.3 Execute lại tx → so AppHash

Đây mới là phần "tự chạy state machine":

```go
// block/internal/syncing/syncer.go:807
newAppHash := exec.ExecuteTxs(ctx, rawTxs, header.Height(), header.Time(), currentState.AppHash)
```

Trong cosmos-exec ([apps/cosmos-exec/executor/executor.go:326](../../../executor/executor.go#L326)) `ExecuteTxs`:

1. Kiểm `prevStateRoot == e.stateRoot` — double-check chain liên tục ở tầng executor.
2. Kiểm `blockHeight == lastHeight + 1`.
3. `app.FinalizeBlock(...)` — chạy đầy đủ ante chain (verify sig, sequence, gas, DeductFee) + msg handler cho từng tx. **Same code path** mà sequencer dùng khi sản xuất block, nên kết quả deterministic.
4. `app.Commit()` ghi IAVL, lấy `LastCommitID().Hash` làm `newAppHash` ([executor.go:403](../../../executor/executor.go#L403)).

`newAppHash` được lưu vào `state.AppHash` của full node, **không** được so trực tiếp với `header.AppHash` của block N — vì sequencer ghi `header.AppHash` của block N = state SAU khi exec block N-1, chứ không phải sau N. Cơ chế thực sự là:

- Block N đến → `state.AppHash` (full node) = AppHash sau block N-1 → so với `header.AppHash` của N → OK → execute → `state.AppHash` cập nhật thành AppHash sau N.
- Block N+1 đến → `header.AppHash` (sequencer ghi) = AppHash sau N → so với `state.AppHash` (full node tự tính) = AppHash sau N → **đây là điểm phát hiện cheat**.

Nếu sequencer cheat ở block N (ví dụ trừ phí sai, skip 1 tx, mint token lậu):

- Full node exec lại → ra `newAppHash_real`.
- Block N+1 đến mang `header.AppHash = newAppHash_cheat` (sequencer cam kết theo state nhánh giả).
- `AssertValidSequence` so 2 giá trị → mismatch → trả `invalid last app hash` → block N+1 reject → **chain halt** ở full node.

Sequencer **không thể** ép full node accept state giả — cùng lắm là làm full node treo cho tới khi có block đúng. Đây là ý nghĩa cụ thể của "sovereign".

### 2.4 Audit forced-inclusion sau commit

Ngay sau commit, syncer chạy thêm 1 check riêng cho censorship ([syncer.go:893](../../../../../block/internal/syncing/syncer.go#L893) `VerifyForcedInclusionTxs`):

- Lấy danh sách tx đã publish vào forced-inclusion namespace trên DA trong epoch đã qua grace period.
- Mỗi tx đó **phải** xuất hiện trong một block đã commit trước hạn (bất kỳ block nào).
- Nếu sequencer skip tx forced-inclusion quá grace period → trả `errMaliciousProposer` ([syncer.go:851](../../../../../block/internal/syncing/syncer.go#L851)) → full node dừng chain.

→ Censorship không bền vững được: hoặc sequencer include trong grace period, hoặc full node tự dừng và operator buộc phải xử lý.

### 2.5 Bảng tóm tắt "ai check gì, fail trả lỗi gì"

| Câu hỏi | Bằng chứng | Reject với lỗi |
|---|---|---|
| Header có đúng sequencer ký? | Verify chữ ký trên payload header | `ErrSignatureVerificationFailed` |
| Data có khớp header? | `DACommitment(data) == header.DataHash` | `header-data validation failed` |
| Có chèn lén / skip block? | `LastHeaderHash`, `Height == prev+1` | `invalid last header hash` / `invalid block height` |
| Sequencer cheat state? | Re-execute → so AppHash ở block kế | `invalid last app hash` |
| Sequencer censor? | Forced-inclusion audit qua epoch | `errMaliciousProposer` |
| Data còn tồn tại? | Header + data đều phải on Celestia | DA fetch fail → block không advance |

**Trust assumption duy nhất còn lại:** DA layer (Celestia) live và phục vụ data. Mất Celestia → không verify được nữa, nhưng kẻ tấn công cũng không lừa được state — chỉ là chain ngừng tiến.

## 3. Hai chế độ sequencer

ev-node hỗ trợ hai sequencer (xem `pkg/sequencers/single` và `pkg/sequencers/based`):

| Khía cạnh | **Single** (hybrid) | **Based** (pure DA) |
|-----------|---------------------|---------------------|
| Mempool | Có (`BatchQueue` persistent) | Không |
| Nguồn tx | Mempool **+** forced inclusion | **Chỉ** forced inclusion từ DA |
| `SubmitBatchTxs` | Lưu vào queue | No-op (bỏ qua mempool) |
| `VerifyBatch` | Validate proof | Luôn `true` (tx đều từ DA, đã verified) |
| Liveness | Phụ thuộc sequencer sống | Cao nhất — sống chừng nào DA sống |
| Use case | Rollup truyền thống (mặc định cho dApp) | Cần đảm bảo liveness/chống kiểm duyệt tối đa |

Bật qua `NodeConfig`:

```go
type NodeConfig struct {
    Aggregator     bool // bật block production
    BasedSequencer bool // dùng based sequencer (yêu cầu Aggregator)
    LazyMode       bool // chỉ ra block khi có tx
}
```

> **Based sequencer** chính là cách ev-node "bỏ luôn cả sequencer như một điểm tin": không có mempool, mọi tx phải đi qua DA, nên thứ tự do chính DA layer quyết. Đổi lại độ trễ cao hơn (chờ epoch DA) và không có mempool UX.

## 4. Forced Inclusion — chống kiểm duyệt khi vẫn dùng single sequencer

Đây là cơ chế khiến single sequencer **không** thể kiểm duyệt vĩnh viễn tx của bạn.

```
User gửi tx thẳng vào forced-inclusion namespace trên DA
        │
        ▼
DA lưu tx tại height H
        │
        ▼
Sequencer chạm epoch boundary (mặc định epoch = 50 DA block —
  Genesis.DAEpochForcedInclusion)
        │
        ▼
ForcedInclusionRetriever.Retrieve(epochStart, epochEnd)
  (AsyncBlockRetriever prefetch 2x epoch để giảm latency)
        │
        ▼
GetNextBatch trả tx kèm ForceIncludedMask[i] = true
        │
        ▼
Execution layer validate tx forced (skip validation cho tx mempool đã verified)
```

Điểm cần nhớ:

- Tx forced-inclusion được nhận diện qua **namespace riêng thứ ba** trên DA (ngoài header namespace và data namespace).
- `ForceIncludedMask` phân biệt tx "từ DA — phải validate" với tx "từ mempool — đã validate", vừa bảo mật vừa tối ưu hiệu năng.
- Nếu sequencer cố tình bỏ qua tx đã nằm trong forced-inclusion namespace quá grace period → sequencer bị coi là **malicious**, tx vẫn được đưa vào.
- Xử lý theo **epoch** (không query DA mỗi block) + **checkpoint** (`DAHeight` + `TxIndex`) để resume được sau crash.

→ Tức là kể cả single sequencer, người dùng luôn có "đường vòng" qua DA để ép tx vào chain. Censorship chỉ làm *chậm*, không *chặn vĩnh viễn*.

## 5. Vậy "không có validator" mất gì?

| Rủi ro | Ảnh hưởng thực tế | Giảm nhẹ trong ev-node |
|--------|-------------------|------------------------|
| **Liveness / SPOF** | Single sequencer chết → chain **ngừng ra block** (không sai state, chỉ treo) | Chạy **based sequencer** hoặc thêm cơ chế failover |
| **Censorship tạm thời** | Sequencer có thể trì hoãn tx | **Forced inclusion** đảm bảo cuối cùng vẫn vào |
| **Reorg ngắn** | Trước khi block lên DA, thứ tự về lý thuyết có thể đổi | Sau khi ghi lên Celestia coi như chốt |
| **Không BFT finality** | "Finality" = lúc data nằm trên Celestia, không phải vote validator | Đây là tính chất của sovereign rollup, không phải bug |

**Không mất:** tính đúng của state (sovereign verification), bất biến lịch sử (DA).

## 6. Liên hệ tới phí (0-fee) trên cosmos-exec

Vì không có validator set cần thưởng staking, stack cosmos-exec không bắt buộc phí. Lưu ý đúng theo code:

- Mặc định (`COSMOS_EXEC_ENFORCE_SIGNATURES` không set) → **không có ante handler nào chạy** (`app.go:295`): không verify chữ ký, không sequence, không gas, **không** fee.
- Khi bật `COSMOS_EXEC_ENFORCE_SIGNATURES=true` → ante chain chạy nhưng `TxFeeChecker` vẫn **chấp nhận tx phí 0**; `AutoCreateAccount` chạy trước `DeductFee` nên account Keplr mới gửi tx đầu tiên không cần nạp tiền.

Đánh đổi: **0-fee = không có lớp chống spam kinh tế**. Phù hợp app sovereign/permissioned/dev. Muốn bật fee > 0 **không chỉ** là đổi `TxFeeChecker` — còn phải bật ante (bước 0) và thêm cơ chế faucet/cấp vốn (account mặc định số dư 0). Xem đầy đủ ở [fee-economics.md](fee-economics.md) mục 6 & 6b. Chi phí không biến mất hẳn — **operator rollup vẫn trả blob fee cho Celestia** khi publish data.

## 7. Khi nào chọn gì (cho dApp của bạn)

- **Dev / demo / app sovereign nội bộ**: single sequencer + 0-fee. Đơn giản nhất, UX tốt nhất (vào là dùng, không phí). Chấp nhận sequencer là điểm tin về *liveness*.
- **Cần chống kiểm duyệt mạnh / liveness cao**: bật `BasedSequencer = true`. Mất mempool UX và độ trễ cao hơn, đổi lấy không có điểm tin ordering.
- **Public, giá trị cao**: single sequencer + forced inclusion bật sẵn + thêm fee token thật + kế hoạch multi-sequencer/failover.

## 8. Câu hỏi thường gặp — EVM có validator không? Cosmos ABCI có còn CometBFT không?

Hai cách hiểu sai phổ biến nhất khi tiếp cận stack này. Cùng một câu trả lời: **không, ev-node tự đóng vai consensus, không có process validator/CometBFT nào chạy** — nhưng cơ chế thực hiện ở hai backend khác nhau.

### 8.1 EVM backend — không có validator

[execution/evm/execution.go](https://github.com/evstack/ev-node/blob/main/execution/evm/execution.go) chỉ giao tiếp với một Ethereum execution client (Geth/Reth/Erigon) qua **Engine API**. Đây đúng cách Ethereum hậu-Merge tách execution layer khỏi consensus layer — nhưng "CL" trong stack này là ev-node sequencer chứ không phải Beacon chain.

| Quan sát | Bằng chứng |
|----------|-----------|
| Không có khái niệm validator | Grep `validator` trong `execution/evm/execution.go`: 0 kết quả thuộc về cấu trúc consensus |
| Single aggregator, không có set | [apps/evm/cmd/init.go:61](https://github.com/evstack/ev-node/blob/main/apps/evm/cmd/init.go#L61) — `CreateSigner(...)` sinh **một** `proposerAddress` |
| Finality là mock theo offset | `SafeBlockLag = 2`, `FinalizedBlockLag = 3` trong `execution.go` — comment ghi rõ *"temporary mock value until proper DA-based finalization is wired up"* |
| Không có BFT vote | Block hợp lệ khi Engine API trả `VALID`, không cần ⅔ ai cả |

### 8.2 Cosmos ABCI backend — không chạy CometBFT process

App Cosmos SDK **import** `github.com/cometbft/cometbft/abci/types` (xem [apps/cosmos-exec/app/app.go:17](../../app/app.go#L17), [executor/executor.go:15](../../executor/executor.go#L15)) nhưng đó chỉ là tái sử dụng các struct `RequestFinalizeBlock`, `ResponseInitChain` làm **schema** của API giữa consensus và app. Không có CometBFT process nào chạy.

| Quan sát | Bằng chứng |
|----------|-----------|
| CosmosExecutor gọi thẳng BaseApp | [executor/executor.go:358-396](../../executor/executor.go#L358-L396) — `e.app.FinalizeBlock(...)` rồi `e.app.Commit()`, in-process, không qua mạng |
| Không có staking/distribution thật | [app/wasm_deps.go](../../app/wasm_deps.go) — `noopStakingKeeper`, `noopDistributionKeeper` đứng thay `x/staking` và `x/distribution` |
| Code "đánh lừa" SDK qua check validator | [app/app.go:437-441](../../app/app.go#L437-L441) — chủ động bắt và nuốt lỗi `"validator set is empty after InitGenesis"`, trả lại đúng `req.Validators` |
| `ValidatorAddressCodec` chỉ là codec | [app/app.go:109](../../app/app.go#L109) — chuẩn bị để bech32-encode địa chỉ nếu sau này có validator, chứ không định nghĩa set |

### 8.3 Bản đồ kiến trúc rút gọn

```
EVM stack:
  ev-node sequencer ──Engine API──► Geth/Reth (state + EVM)

Cosmos stack:
  ev-node sequencer ──in-process──► CosmosExecutor ──► BaseApp (+WasmKeeper)
                                    │
                                    └─ dùng abci/types làm struct
                                       (không có process CometBFT)
```

### 8.4 Hệ quả thực tế của việc bỏ CometBFT

| Hệ quả | Mô tả |
|--------|-------|
| **IBC `07-tendermint` không xác minh được header** | Light client counterparty cần `ValidatorsHash`, `NextValidatorsHash` và chữ ký ⅔ voting power — tất cả đều rỗng. Đây là lý do IBC core compile được nhưng channel không mở được với chain ngoài. Xem [ibc-integration.md](ibc-integration.md). |
| **Không có "voting power"** | Mọi cơ chế Cosmos SDK dựa trên `staking.BondedTokens` (governance, slashing) cũng không hoạt động được; do đó đồ án bỏ qua `x/gov`, `x/upgrade`. |
| **Bù lại — sequencer không thể giả mạo state** | Full node verify lại như mô tả ở section 2; vai trò "validator vote on correctness" được thay bằng *sovereign verification* tại mỗi node. |

→ Việc bỏ CometBFT/validator **không** mất tính đúng của state (vì sovereign verification), nhưng **mất** khả năng tương thích với mọi light client dựa trên BFT — đặc biệt IBC truyền thống. Hai hướng giải quyết được trình bày trong [ibc-integration.md](ibc-integration.md) và [roadmap.md](roadmap.md).

## Tham chiếu code

| Thành phần | File |
|------------|------|
| Sequencer interface | `core/sequencer/sequencing.go` |
| Single (hybrid) sequencer | `pkg/sequencers/single/sequencer.go`, `queue.go` |
| Based (pure DA) sequencer | `pkg/sequencers/based/sequencer.go` |
| Checkpoint dùng chung | `pkg/sequencers/common/checkpoint.go` |
| Forced inclusion retrieval | `block/internal/da/forced_inclusion_retriever.go` |
| Async prefetch | `block/internal/da/async_block_retriever.go` |
| Block production | `block/internal/executing/executor.go` |
| Sync (DA + P2P + forced) | `block/internal/syncing/syncer.go` |
| 0-fee ante | `apps/cosmos-exec/app/ante.go` |
| EVM Engine API client | `execution/evm/execution.go`, `engine_rpc_client.go` |
| EVM single-aggregator init | `apps/evm/cmd/init.go` |
| CosmosExecutor gọi BaseApp | `apps/cosmos-exec/executor/executor.go` |
| Stub keeper thay validator/staking | `apps/cosmos-exec/app/wasm_deps.go` |
| Bypass "validator set is empty" | `apps/cosmos-exec/app/app.go` (~L437) |
