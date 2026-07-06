# Auto Account Creation & Tx Indexing

Trang này tài liệu hai tính năng backend cho phép luồng **ký permissionless từ
browser** (vd: dApp dùng Keplr nói chuyện với cosmos-exec qua HTTP):

1. **Auto-create account** ở tx ký đầu tiên — không cần faucet, không cần pre-fund.
2. **`tx_hashes` trong `BlockInfo`** — để explorer liệt kê tx theo block và link
   tới chi tiết từng tx.

Nếu bạn chỉ gọi cosmos-exec qua Go SDK với một keypair đã được cấp tiền, không
cần quan tâm — cả hai tính năng đều trong suốt (transparent).

## Mục lục

- [1. Tx đầu tiên permissionless (không faucet)](#1-tx-đầu-tiên-permissionless-không-faucet)
- [2. `tx_hashes` trên `BlockInfo`](#2-tx_hashes-trên-blockinfo)
- [3. Vì sao vẫn cần ante chain tuỳ biến dù đã có `x/auth`](#3-vì-sao-vẫn-cần-ante-chain-tuỳ-biến-dù-đã-có-xauth)
- [4. Signature verification hoạt động thế nào](#4-signature-verification-hoạt-động-thế-nào)
- [Luồng end-to-end cho Keplr dApp](#luồng-end-to-end-cho-keplr-dapp)
- [5. Ký multisig (nhiều chữ ký)](#5-ký-multisig-nhiều-chữ-ký)

---

## 1. Tx đầu tiên permissionless (không faucet)

### Nền tảng: **Keypair ≠ Account** (đọc trước khi đi tiếp)

Cả trang này chỉ dễ hiểu khi tách rạch ròi hai khái niệm hay bị gộp làm một:

| | **Keypair** — danh tính mật mã | **Account** — bản ghi on-chain |
|---|---|---|
| Bản chất | `private key` + `public key` + `address` suy ra từ pubkey | một `BaseAccount` nằm trong state module `x/auth` |
| Sống ở đâu | offline, trong ví (Keplr, keyring Go SDK…) — chain **không biết** nó tồn tại | trong DB của chain, tại đúng địa chỉ đó |
| Chứa gì | khả năng **ký** | `account_number`, `sequence`, `pubkey` (+ balance nằm ở `x/bank`) |
| Tạo ra bằng | random offline, **không cần chain** | phải được **ghi vào state** (faucet, hoặc AutoCreate, hoặc `MsgSend` tới nó) |
| Ai suy ra address | ai cũng được: `address = ripemd160(sha256(pubkey))` | — |

Hệ quả rút ra từ bảng này — dùng để trả lời 3 hiểu lầm kinh điển:

**① "Chữ ký gửi lên chain là private key à?"** → **Không.** Cái truyền đi là
`public key` + **bytes chữ ký**. Private key **không bao giờ** rời ví. Cấu trúc
mỗi chữ ký ([`GetSignaturesV2`](../../../app/ante.go#L225)):

```go
type SignatureV2 struct {
    PubKey   cryptotypes.PubKey // public key của signer
    Data     SignatureData      // BYTES chữ ký (kết quả ký) — KHÔNG phải private key
    Sequence uint64
}
```

Ký là: `privkey` + `digest(SignDoc)` → `signature bytes` (làm ở ví). Verify là:
`pubkey` + `digest` + `signature bytes` → true/false (làm ở chain). Chain chỉ cần
pubkey, và **không được** có privkey.

**② "Chưa có account thì ký kiểu gì được?"** → Ký **được bình thường**, vì ký chỉ
là phép toán mật mã trên keypair offline — **không đọc/ghi state chain nào**. Bạn
tạo keypair, suy ra address, ký tx — tất cả trước khi địa chỉ đó từng xuất hiện
on-chain. "Account chưa tồn tại" chỉ nghĩa là **chain chưa có bản ghi** ở địa chỉ
đó, không cản trở việc ký.

**③ "Ký rồi sao vẫn phải tạo account, chưa xong à?"** → Ký chỉ tạo ra cục bytes ở
**phía client**. Chain nhận về vẫn phải **xử lý** tx đó, và các bước xử lý **bắt
buộc phải có bản ghi account trong state**:

```
DeductFee        → tra account "fee payer" trong state; không có → "unknown address"
SigVerification  → LOAD account để đọc account_number + sequence, dựng lại SignDoc rồi verify
IncrementSequence→ +1 vào sequence CỦA account (chống replay)
```

Chữ ký commit cứng vào `account_number` + `chain_id` ([§4.1](#41-client-ký-gì)); muốn
verify thì chain phải đọc `account_number` **từ state** → phải có account. Fee trừ
từ balance của account → phải có account. Sequence lưu trong account → phải có
account. Bình thường **faucet** tạo bản ghi này trước khi user ký; cosmos-exec
mặc định không faucet nên **AutoCreate** tạo nó ngay trong lúc chạy ante (xem
[§1.a](#a-autocreateaccountdecorator-ante-handler)).

> **Một câu:** *ký* = chứng minh "tôi giữ private key"; *tạo account* = đăng ký một
> ô trong sổ cái để chain lưu `account_number`/`sequence`/pubkey/balance của bạn.
> Hai việc độc lập; chain cần **cả hai** mới xử lý xong một tx.

### Vấn đề

Trong chain Cosmos SDK chuẩn, user mới không submit tx được tới khi account của
họ tồn tại trong state `x/auth`. Bình thường faucet gửi token trước (qua
`MsgSend`), side-effect gọi `AccountKeeper.NewAccount`. **Ở cấu hình mặc định,
cosmos-exec không bật faucet** nên đường bootstrap đó không tồn tại — mọi account
số dư 0 và không có nguồn cấp tiền cho địa chỉ mới.

> **Đính chính cách hiểu — 3 công tắc độc lập, đừng gộp làm một:**
>
> | Thứ | Bật bằng | Mặc định |
> |-----|----------|----------|
> | **Faucet / treasury** (cấp tiền cho địa chỉ mới) | `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` (+ `COSMOS_EXEC_TREASURY_AMOUNT`, `COSMOS_EXEC_FAUCET_AMOUNT`) — xem [faucet.go](../../../cmd/cosmos-exec-grpc/faucet.go) | **tắt** (`loadFaucetConfig` trả `nil`) |
> | **Ante chain** (verify sig, DeductFee, AutoCreate…) | `COSMOS_EXEC_ENFORCE_SIGNATURES` — xem [app.go](../../../app/app.go) | **tắt** |
> | **Trừ phí thật** | `DeductFee` (nằm trong ante) + `COSMOS_EXEC_MIN_GAS_PRICE` | không trừ |
>
> - "Không faucet" phụ thuộc **treasury key**, **không** phụ thuộc `ENFORCE_SIGNATURES`.
>   Có thể bật faucet mà vẫn `ENFORCE_SIGNATURES=false`, và ngược lại.
> - Denom phí (vd `ustake`) **luôn tồn tại** trong chain. "Không có fee token" chỉ
>   là cách nói cho tình huống mặc định: chưa ai được cấp tiền và chưa trừ phí.
>   Việc phí có bị **trừ** hay không mới do `DeductFee` (⇒ gián tiếp phụ thuộc
>   `ENFORCE_SIGNATURES` + min gas price) quyết định.
> - Vì thế AutoCreate (mục dưới) sinh ra để bootstrap account **khi không có
>   faucet**; nếu bạn set `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` thì có thể dùng luồng
>   faucet cosmos chuẩn thay cho AutoCreate.

### Cách giải — hai phần

#### a) `AutoCreateAccountDecorator` (ante handler)

`apps/cosmos-exec/app/ante.go` chèn một decorator tuỳ biến vào ante chain:

```
SetUpContext → ExtensionOptions → ValidateBasic → TxTimeoutHeight
→ ValidateMemo → ConsumeGasForTxSize
→ AutoCreateAccount        ← tạo account nếu chưa có
→ DeductFee                ← cần account đã tồn tại
→ SetPubKey → ValidateSigCount → SigGasConsume → SigVerification
→ IncrementSequence
```

**Hàm cụ thể** ([`AutoCreateAccountDecorator.AnteHandle`](../../../app/ante.go#L217-L248)):

```go
func (d AutoCreateAccountDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
    // 1. tx phải là SigVerifiableTx để lấy được danh sách chữ ký.
    sigTx, ok := tx.(authsigning.SigVerifiableTx)
    if !ok {
        return ctx, errorsmod.Wrap(sdkerrors.ErrTxDecode, "tx must be a SigVerifiableTx")
    }
    sigs, err := sigTx.GetSignaturesV2() // mỗi phần tử mang theo PubKey của signer.
    if err != nil {
        return ctx, err
    }

    // 2. Với MỖI signer: suy địa chỉ từ pubkey; nếu chưa có account thì tạo.
    for _, sig := range sigs {
        if sig.PubKey == nil {
            continue // không có pubkey → không suy được địa chỉ, bỏ qua.
        }
        addr := sdk.AccAddress(sig.PubKey.Address()) // address = ripemd160(sha256(pubkey))
        if !d.ak.HasAccount(ctx, addr) {
            acc := d.ak.NewAccountWithAddress(ctx, addr) // ← cấp account_number ở đây
            d.ak.SetAccount(ctx, acc)                    // ← persist vào state x/auth
        }
    }

    return next(ctx, tx, simulate) // đi tiếp DeductFee → … → SigVerification.
}
```

Ba điểm cần nắm:

- **Địa chỉ suy từ pubkey, không tin field bên ngoài.** `addr = sdk.AccAddress(sig.PubKey.Address())` — cùng công thức mà `SigVerification` dùng, nên account tạo ra chắc chắn là account sẽ bị verify chữ ký. Không thể "tạo hộ" account cho địa chỉ khác.
- **Idempotent.** Có `HasAccount` guard → tx thứ 2, 3… của cùng địa chỉ không tạo lại (nhánh này bị skip, xem [bước 6a luồng cuối trang](#luồng-end-to-end-cho-keplr-dapp)).
- **Chưa ghi pubkey ở đây.** `NewAccountWithAddress` chỉ tạo `BaseAccount` rỗng pubkey; pubkey được `SetPubKeyDecorator` ghi sau (xem [§4.2](#42-server-verify-gì)). AutoCreate chỉ lo phần "account tồn tại + có account_number".

**Thứ tự cực kỳ quan trọng.** `AutoCreate` phải chạy **trước** `DeductFee`, vì
`DeductFeeDecorator` tra fee payer (= signer đầu tiên mặc định) trong state và
reject tx nếu không thấy. Đảo thứ tự sẽ gặp:

```
fee payer address: <garbled bytes> does not exist: unknown address
```

Byte rác là `[]byte` (raw 20-byte address) format bằng `%s` — `FeeTx.FeePayer()`
trả bytes, không phải bech32, trong cosmos-sdk v0.50.

#### b) `/auth/account` peek số account tương lai

Vẫn còn bài toán "con gà & quả trứng": client phải đặt `account_number` vào
`SignDoc` **trước khi** `AutoCreate` chạy. Nếu `/auth/account` trả zero cho địa
chỉ chưa tồn tại, client ký với `0`, nhưng `AutoCreate` gán (vd) `7`, và
`SigVerificationDecorator` reject với:

```
signature verification failed; please verify account number (0) and chain-id (...): unauthorized
```

Nên `executor.GetAccountInfo` **peek** giá trị sequence kế tiếp khi account chưa
tồn tại:

```go
acc := e.app.AccountKeeper.GetAccount(queryCtx, addr)
if acc == nil {
    nextNum, _ := e.app.AccountKeeper.AccountNumber.Peek(queryCtx)
    return AccountInfo{
        Address: bech32Addr, AccountNumber: nextNum, Sequence: 0, Exists: false,
    }, nil
}
```

`collections.Sequence.Peek` trả giá trị mà `Next()` sẽ trả — tức cùng số
`NewAccountWithAddress` sẽ gán — mà không tăng counter. Client ký với số đó,
`AutoCreate` gán cùng số, sig verification thành công.

#### c) `account_number` được cấp thế nào (chi tiết)

`account_number` **không phải** do cosmos-exec tự sinh — nó đến từ một **counter
toàn cục đơn điệu** của `x/auth`, lưu ngay trong state dưới key `GlobalAccountNumber`.
Trong cosmos-sdk v0.50 counter này là một [`collections.Sequence`](https://docs.cosmos.network/main/build/packages/collections#sequence)
tên `AccountKeeper.AccountNumber`.

**Call chain khi AutoCreate gọi `NewAccountWithAddress`:**

```
NewAccountWithAddress(ctx, addr)
   └─ proto() tạo BaseAccount rỗng, SetAddress(addr)
   └─ NewAccount(ctx, acc)
        └─ acc.SetAccountNumber( NextAccountNumber(ctx) )
                                    └─ AccountNumber.Next(ctx)   ← lấy giá trị hiện tại RỒI +1
```

- `Sequence.Next(ctx)` = "read-then-increment": trả về giá trị hiện tại (vd `7`)
  và ghi `8` trở lại store. Đây là số được gán cho account mới.
- `Sequence.Peek(ctx)` = **chỉ đọc, không tăng**: cũng trả `7`. Vì vậy giá trị
  `/auth/account` peek ở [§1.b](#b-authaccount-peek-số-account-tương-lai) **bằng đúng**
  số mà `Next` sắp gán → client ký đúng `account_number`, `SigVerification` khớp.

##### `Peek` — mổ xẻ kỹ

`collections.Sequence` bản chất chỉ là **một ô `uint64` duy nhất** trong KV store
(một key cố định, value là số đếm hiện tại). Hai thao tác khác nhau **một chỗ duy
nhất: có ghi lại state hay không.**

```go
// Rút gọn từ cosmossdk.io/collections
func (s Sequence) Peek(ctx) (uint64, error) {   // GET
    return s.Get(ctx)                            //   đọc ô đếm, KHÔNG ghi
}
func (s Sequence) Next(ctx) (uint64, error) {   // GET rồi SET
    v, _ := s.Get(ctx)                           //   đọc giá trị hiện tại v
    s.Set(ctx, v+1)                              //   GHI v+1 trở lại  ← đây là "tiêu" 1 số
    return v                                      //   trả v (số vừa cấp)
}
```

**Vì sao đường query BẮT BUỘC dùng `Peek`, không được dùng `Next`:**

- `GetAccountInfo` chạy trên một **query context read-only** — `NewContext(true)`
  ([executor.go](../../../executor/executor.go#L555)). Đây là đường **đọc**
  (`GET /auth/account`), phải **idempotent**: gọi 100 lần cho cùng địa chỉ phải
  trả cùng kết quả và **không được đổi state**.
- Nếu lỡ dùng `Next` ở đây thì **mỗi lần query lại "đốt" một account_number** và
  ghi state trong một ngữ cảnh đáng lẽ chỉ đọc → số nhảy lung tung, và tệ hơn là
  các node **tính state lệch nhau** (query trên full node vs sequencer) → nguy cơ
  fork. `Peek` không ghi gì nên an toàn tuyệt đối để gọi ở đường đọc.

**`Peek` trả về một DỰ ĐOÁN, không phải một chỗ đã đặt trước (reservation):**

- Ý nghĩa chính xác của giá trị peek là: *"NẾU giao dịch tạo account tiếp theo là
  của bạn, bạn sẽ nhận số này."* Nó **không giữ chỗ** số đó cho bạn.
- Giữa lúc bạn peek (`7`) và lúc tx của bạn land, **nếu có địa chỉ mới khác** land
  trước, `Next` của nó ăn mất `7`, counter thành `8`; tx của bạn khi `AutoCreate`
  chạy sẽ được gán `8` — **lệch** số `7` bạn đã ký → `SigVerification` fail. Đây
  đúng là [race ở mục Caveat](#caveat--race-khi-nhiều-tx-đầu-submit-đồng-thời) bên
  dưới; client chỉ cần refetch (peek lại thấy `8`) và ký lại.
- Ngược lại, với tx **thứ hai trở đi** của cùng account, không còn dùng peek nữa:
  account đã tồn tại → `GetAccountInfo` đi nhánh `acc != nil` và trả
  `acc.GetAccountNumber()` (số thật, cố định), không đụng counter.

**Tính xác định (determinism):** `Peek` đọc ô đếm từ **state đã commit tại một
height** (`WithBlockHeight`). Cùng height thì mọi node đọc ra cùng số — nên dù
`/auth/account` được gọi ở sequencer hay full node, kết quả peek giống nhau. Điều
làm nó "có thể sai" **không phải** do non-determinism, mà do **thời gian**: state
tiến lên giữa lúc peek và lúc tx land.

**Vì sao counter không bắt đầu từ 0 cho account của user:** lúc `InitGenesis`,
các **module account** (`fee_collector`, `mint`, `bonded_tokens_pool`, `gov`,
`wasm`…) được tạo trước và chiếm dần `0, 1, 2, …`. Nên khi user đầu tiên xuất
hiện, `Next` đã ở một số dương (vd `7` trong ví dụ xuyên suốt trang này). Không
thể ép mọi account user về `account_number = 0` vì `x/auth` có `Unique` index
`account_number → address`, ghi trùng `0` sẽ panic:

```
collections: conflict: index uniqueness constrain violation: 0
```

(xem thêm [§ "Vì sao không ghim account_number = 0"](#vì-sao-không-ghim-account_number--0-cho-mọi-account-auto-created)).

**Vòng đời một account mới, đầu-đến-cuối:**

| Bước | Ai làm | account_number | Sequence |
|------|--------|----------------|----------|
| 1. Client hỏi `/auth/account/{addr}` (addr chưa tồn tại) | `GetAccountInfo` → `AccountNumber.Peek` | trả `7` (peek, **không** đổi counter) | `0` |
| 2. Client ký `SignDoc` với `account_number=7, sequence=0` | wallet | ký cứng vào digest | ký cứng |
| 3. Tx vào block, ante chạy `AutoCreate` | `NewAccountWithAddress` → `Next` | gán `7`, counter → `8` | account mới có sequence `0` |
| 4. `SigVerification` dựng lại `SignDoc` với `account_number=7` server-side | `SigVerificationDecorator` | `7` khớp diged client → pass | `0` khớp |
| 5. `IncrementSequence` | `IncrementSequenceDecorator` | `7` (cố định vĩnh viễn) | `0 → 1` |

Từ tx thứ 2, `HasAccount` trả true → AutoCreate skip; `account_number` giữ nguyên
`7` mãi mãi, chỉ `sequence` tăng dần để chống replay.

### Caveat — race khi nhiều tx-đầu submit đồng thời

Hai caller poll `/auth/account` cho **hai địa chỉ mới khác nhau** cùng lúc sẽ
nhận cùng số peek. Tx nào land trước thắng; tx thứ hai sig sẽ fail (số
`account_number` đã ký giờ lệch 1) và client phải retry (sẽ refetch và thấy
số peek đã tăng). OK cho dev một user và đa số dApp UI. **Không** phù hợp khi
kỳ vọng một loạt user lần-đầu từ nhiều client độc lập.

Cho kịch bản đó, thay bằng:

- faucet thật cấp tiền cho địa chỉ mới trước khi ký (cosmos flow chuẩn), hoặc
- endpoint registration tạo account đồng bộ và trả `account_number` đã gán.

### Vì sao không ghim `account_number = 0` cho mọi account auto-created?

Keeper `x/auth` enforce `Unique` index `account_number → address`. Module account
(`fee_collector`, `mint`, ...) đã chiếm `0, 1, 2 …`. `SetAccount` account thứ
hai với `account_number = 0` panic:

```
collections: conflict: index uniqueness constrain violation: 0
```

Nên ta giữ account number duy nhất (như SDK kỳ vọng) và trả đúng số cho client
lúc query.

### Hardening cho production

`COSMOS_EXEC_ENFORCE_SIGNATURES=true` kích hoạt toàn bộ ante chain (sig
verification, increment sequence, ...) — không bật thì AutoCreate cũng không
chạy tới. Luôn set `true` ở prod, bất kể có giữ AutoCreate hay không.

Nếu không muốn auto-creation, bỏ `NewAutoCreateAccountDecorator` khỏi chain
trong `NewPermissionlessAnteHandler`. Khi đó địa chỉ mới phải được cấp tiền qua
`MsgSend` trước.

---

## 2. `tx_hashes` trên `BlockInfo`

`/blocks/latest` và `/blocks/{height}` giờ kèm hash của các tx trong block đó:

```json
{
  "height": 42,
  "time": "2026-05-14T10:00:00Z",
  "app_hash": "ab12…",
  "num_txs": 2,
  "tx_hashes": [
    "72b2169c6d86cd70ba96da1ffdc096dda2c8e462db2e00c95b872520847e4445",
    "f1e3a8c2…"
  ]
}
```

Field được điền trong `executor.ExecuteTxs` từ cùng slice `validTxs` đưa cho
`FinalizeBlock`, dùng cùng `hashTx` (SHA-256 lowercase của tx bytes) mà
`/tx/{hash}` dùng làm key. Mỗi hash trong list resolve được trực tiếp qua
`GET /tx/{hash}`.

Kết hợp với endpoint `/tx/{hash}`, đủ để dựng explorer kiểu etherscan mà không
cần index thêm:

```
GET /blocks/latest                 → block gần nhất + tx_hashes của nó
GET /tx/{hash}                      → code, log, events của từng hash
```

### Tương thích ngược

- `tx_hashes` bị bỏ (`omitempty`) khi block không có tx.
- Block lưu **trước** thay đổi này load lại từ đĩa với `tx_hashes` rỗng.
  Explorer nên xử lý field thiếu — coi `num_txs > 0` mà không có `tx_hashes` là
  "block này có trước indexing; không tái dựng hash từ đĩa được".

---

## 3. Vì sao vẫn cần ante chain tuỳ biến dù đã có `x/auth`

Giả định thường gặp: "chain bật `x/auth` nên signature tự được check." **Không.**
`x/auth` cung cấp **building block** — `AccountKeeper`, account types, và một bộ
`Decorator` trong `x/auth/ante` — nhưng **không** wire chúng vào pipeline tx.
Chain phải tự ráp `AnteHandler` từ các decorator đó.
[`NewPermissionlessAnteHandler`](../../../app/ante.go) chính là phần wiring đó.

Ngoài wiring, cosmos-exec có 2 ràng buộc buộc phải dùng chain *tuỳ biến* (không stock):

### 3.1 cosmos-exec không có bước admission CheckTx

Chain Cosmos-SDK bình thường chạy ante handler **2 lần**: 1 lần ở CheckTx
(admission mempool) và 1 lần ở DeliverTx (thực thi block). Stage mempool reject
rác rẻ tiền trước khi vào block.

cosmos-exec không có stage đó:

```
POST /tx/submit  →  InjectTx (queue raw bytes)  →  FinalizeBlock
                                                   └─► ante chain chạy ở đây, 1 lần
```

Hệ quả:

- **Không có ante chain thì không gì reject tx hỏng.** Nó fail sâu trong thực
  thi message, sau khi đã metered gas và có thể đã chạm state.
- Ante chain là **điểm DUY NHẤT** validate signature / sequence / fee, nên phải
  đầy đủ. Bỏ một decorator mà stock chain có không phải "tối ưu free" — đó là lỗ
  hổng bảo mật.

### 3.2 Auto-account-creation buộc thứ tự non-stock

`authante.NewAnteHandler` stock viết cho chain mà mọi signer đều pre-funded.
Thứ tự của nó: `… → DeductFee → SetPubKey → SigVerification → IncrementSequence`.
Không có chỗ cho "tạo account trước". Chèn `AutoCreateAccount` cần đúng vị trí
(sau `ValidateBasic` để không tạo account cho tx hỏng; trước `DeductFee` để fee
payer lookup thấy account mới — xem §1.a). Vì vậy ta ship constructor riêng thay
vì gọi `authante.NewAnteHandler`.

### 3.3 `x/auth` cung cấp gì vs ante chain cung cấp gì

| Mối quan tâm | Module `x/auth` | Ante chain |
|--------------|-----------------|------------|
| Lưu account (`AccountKeeper`) | Có — `BaseAccount`, account_number, sequence | Đọc/ghi nó |
| Crypto primitive cho chữ ký | Có — sign-mode handler, pubkey types | Gọi chúng |
| **Quyết định check nào chạy, theo thứ tự nào, cho mỗi tx** | **Không** | **Có — đây là việc của ante chain** |
| Replay protection | Chỉ lưu `sequence` | `IncrementSequenceDecorator` thực sự tăng nó |
| Auto-create / fee policy / size limit | Không opinionated | Decorator chain-specific (`AutoCreate`, `DeductFee`, ...) |

Tóm lại: `x/auth` là **library**; ante chain là **policy** nói khi nào và cách
nào library được gọi.

---

## 4. Signature verification hoạt động thế nào

### 4.1 Client ký gì

Wallet (Keplr, signer của Go SDK, ...) dựng `SignDoc` gồm 4 field:

| Field | Nguồn |
|-------|-------|
| `body_bytes` | Messages + memo + timeout height |
| `auth_info_bytes` | Fee, gas limit, signer infos (pubkey + sequence) |
| `chain_id` | Từ `/status` (hoặc hard-code) |
| `account_number` | Từ `/auth/account/{addr}` (peeked, xem §1.b) |

4 phần này nối lại và hash; wallet ký digest bằng private key. Signature +
`body_bytes`/`auth_info_bytes` gốc gói vào `TxRaw` và submit.

**Quan trọng:** chữ ký commit vào `account_number` và `chain_id`. Nếu server
dựng lại `SignDoc` với giá trị khác cho 1 trong 2 → chữ ký không verify — đó là
failure mode mà `Peek` (§1.b) tồn tại để ngăn.

### 4.2 Server verify gì

5 decorator tham gia, mỗi cái làm 1 việc:

1. **`SetPubKeyDecorator`** — với account chưa có pubkey, copy pubkey từ
   `SignerInfo` của tx vào account và persist (case tx-đầu: `AutoCreate` tạo
   account nhưng chưa biết pubkey; bước này ghi lại).
2. **`ValidateSigCountDecorator`** — giới hạn số chữ ký/tx (chống DOS pubkey array).
3. **`SigGasConsumeDecorator`** — tính gas theo chi phí verify (multi-sig đắt hơn).
4. **`SigVerificationDecorator`** — crypto thật:
   - Load account từ `AccountKeeper`, đọc `account_number` lưu + `sequence` hiện tại.
   - Dựng lại `SignDoc` với giá trị server-side + `chain_id`.
   - Gọi sign-mode handler (`SIGN_MODE_DIRECT`, `SIGN_MODE_LEGACY_AMINO_JSON`, ...) tính lại digest.
   - Verify digest với chữ ký đã gửi bằng pubkey lưu.
   - Bất kỳ mismatch nào (sai account number/sequence/chain-id, đổi pubkey, sửa body) → `signature verification failed … unauthorized`.
5. **`IncrementSequenceDecorator`** — tăng `sequence` thêm 1, để cùng tx ký không replay được.

Replay protection chạy được vì `(account_number, sequence)` là cặp dùng-1-lần:
sau khi `IncrementSequence` chạy, resubmit cùng bytes sẽ fail ở bước 4 (server
giờ dựng `SignDoc` với `sequence+1`, digest không khớp chữ ký cũ).

### 4.3 Vì sao không decorator nào optional

| Bỏ decorator | Hỏng gì |
|--------------|---------|
| `SetPubKey` | Sig verify fail — không có pubkey để check |
| `SigVerification` | Ai cũng giả mạo tx của người khác — không auth gì cả |
| `IncrementSequence` | 1 tx ký replay được mãi mãi |
| `ValidateBasic` | Tx hỏng vào tới thực thi message → panic / burn gas |
| `ConsumeGasForTxSize` | DOS free bằng tx body khổng lồ |
| `DeductFee` | (Chỉ chain có fee.) Tx free dù yêu cầu phí |

Công tắc tổng là `COSMOS_EXEC_ENFORCE_SIGNATURES`. Khi **tắt** (default cho
dev/test), `app.go` bỏ qua `SetAnteHandler` — không ante chain nào chạy, tx
chưa ký vẫn được nhận. Khi **bật**, toàn bộ chain trên chạy trong `FinalizeBlock`
và tx chưa ký / sai sig bị reject. Production phải set `true`.

---

## Luồng end-to-end cho Keplr dApp

```
1. User connect Keplr, chọn chain "cosmos-wasm-local"
2. Frontend: GET /auth/account/{user_addr}
                       └─→ trả {account_number: 7, sequence: 0, exists: false}
                           (peek từ GlobalAccountNumber)
3. Frontend: build SignDoc với account_number=7, sequence=0
4. Keplr.signDirect(...) → user approve → trả signed TxRaw
5. Frontend: POST /tx/submit { tx_base64 }
6. Backend ante chain:
   a. AutoCreate: addr chưa có state → NewAccountWithAddress → account_number=7
   b. DeductFee: account 7 tồn tại, fee 0 → pass
   c. SigVerify: dựng lại SignDoc với account_number=7 → khớp sig của client
   d. IncrementSequence: account 7 sequence thành 1
7. Tx thực thi (MsgStoreCode / MsgInstantiateContract / MsgExecuteContract)
8. Frontend poll GET /tx/result?hash=… tới khi found
9. Frontend follow link tx_hash từ /blocks/{height}.tx_hashes để render
   danh sách tx theo block trong explorer
```

Tx tiếp theo từ cùng user chỉ chạy 6b → 6d (nhánh `addr chưa có state` ở 6a bị
skip vì `HasAccount` giờ true).

---

## 5. Ký multisig (nhiều chữ ký)

Chain **verify được** tx multisig K-of-N vì ante chain dùng nguyên bộ decorator
chuẩn của cosmos-sdk. Nhưng SDK Go hiện tại ([`Signer`](../signer.go)) **chỉ ký
một khoá secp256k1**, nên muốn multisig phải build tx bằng txbuilder của cosmos-sdk.
Mục này giải thích (5.1) chain verify multisig ra sao, rồi hướng dẫn dựng
(5.2) một helper trong SDK và (5.3) một example chạy thật.

> **Điều kiện tiên quyết:** `COSMOS_EXEC_ENFORCE_SIGNATURES=true`. Không bật thì
> ante chain không chạy → không verify gì → multisig vô nghĩa (xem [§4.3](#43-vì-sao-không-decorator-nào-optional)).
> Trong repo **chưa có** code/test multisig; các đoạn dưới là template đã đối
> chiếu với cosmos-sdk v0.50.11, cần tự thêm test khi đưa vào dùng.

### 5.1 Chain / ante verify multisig thế nào (deep dive)

Điểm cốt lõi: **một tx multisig chỉ có ĐÚNG MỘT signer** — địa chỉ multisig — và
**đúng một `SignatureV2`**, nhưng `SignatureV2.Data` là `*signing.MultiSignatureData`
(gói K chữ ký con + một `BitArray` đánh dấu trong N thành viên ai đã ký), còn
`SignatureV2.PubKey` là `*kmultisig.LegacyAminoPubKey` (pubkey gộp của N thành viên).
Địa chỉ multisig = `LegacyAminoPubKey.Address()` — suy ra từ **cả bộ N pubkey +
ngưỡng K**, nên đổi thành viên hay đổi K là ra địa chỉ khác.

Từng decorator xử lý (đối chiếu `x/auth/ante/sigverify.go` của cosmos-sdk v0.50.11):

| Decorator | Với multisig |
|-----------|--------------|
| **AutoCreate** ([ante.go](../../../app/ante.go#L231-L244)) | `addr = sig.PubKey.Address()` = **địa chỉ multisig** → tạo **một** account cho cả nhóm (không tạo cho từng thành viên) |
| **SetPubKey** | Ghi `LegacyAminoPubKey` (cả N sub-key + K) vào account ở tx đầu; tx sau verify bằng pubkey đã lưu |
| **ValidateSigCount** | Cộng `CountSubKeys` — với multisig **đếm đệ quy toàn bộ N sub-key**, reject nếu tổng > `TxSigLimit` (**mặc định 7**). ⇒ **N ≤ 7** trừ khi tăng param |
| **SigGasConsume** | `ConsumeMultisignatureVerificationGas` duyệt `BitArray`, tính gas cho **mỗi chữ ký con thực sự có mặt** (K cái), mỗi cái theo cost secp256k1 ⇒ đây là lý do "**multi-sig đắt hơn**" |
| **SigVerification** | Check `len(sigs)==len(signers)` (1==1 cho multisig), rồi `VerifyMultisignature`: dựng lại sign bytes, xác nhận **≥ K** chữ ký con hợp lệ trong N, trên đúng `SignDoc` (commit `account_number`+`chain_id` của account multisig) |
| **IncrementSequence** | Tăng `sequence` của **account multisig** (một sequence chung cho cả nhóm, không phải per-thành-viên) |

Hệ quả cần nhớ:
- `account_number`/`sequence` gắn vào **địa chỉ multisig**, không phải từng thành viên.
  Query `/auth/account/{multisig_addr}` (peek như [§1.b](#b-authaccount-peek-số-account-tương-lai) áp dụng y hệt cho tx đầu).
- Tất cả K thành viên phải ký trên **cùng** `SignDoc` (cùng `account_number`,
  `sequence`, `chain_id`, body, auth_info). Lệch một field → `VerifyMultisignature` fail.
- N > 7 → phải tăng `auth` param `TxSigLimit` trong genesis, nếu không `ValidateSigCount` reject.

### 5.2 Option 1 — helper `BuildMultisigTx` trong SDK

Ý tưởng: cung cấp 3 primitive để caller không phải đụng thẳng txbuilder cosmos-sdk:
tạo pubkey multisig → mỗi thành viên ký ra một "partial" → gộp thành TxRaw. Đặt vào
một file mới, vd `apps/cosmos-exec/sdk/cosmoswasm/multisig.go`.

```go
package cosmoswasm

import (
	"github.com/cosmos/cosmos-sdk/client"
	kmultisig "github.com/cosmos/cosmos-sdk/crypto/keys/multisig"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/crypto/types/multisig"
	sdk "github.com/cosmos/cosmos-sdk/types"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
)

// MultisigKey mô tả nhóm K-of-N: N pubkey thành viên + ngưỡng K.
type MultisigKey struct {
	Threshold uint32                 // K — số chữ ký tối thiểu
	PubKeys   []cryptotypes.PubKey   // N — pubkey các thành viên (thứ tự CỐ ĐỊNH)
}

func (m MultisigKey) AminoPubKey() *kmultisig.LegacyAminoPubKey {
	return kmultisig.NewLegacyAminoPubKey(int(m.Threshold), m.PubKeys)
}

// Address trả địa chỉ bech32 của account multisig (nơi gắn account_number/sequence/balance).
func (m MultisigKey) Address() string {
	return sdk.AccAddress(m.AminoPubKey().Address()).String()
}

// BuildMultisigTx dựng TxRaw đã ký K-of-N. members là các partial signature
// gom từ (đủ K) thành viên; đều phải ký CÙNG SignDoc (cùng accNum/seq/chainID/msgs).
//
// Luồng thực tế: (1) mỗi thành viên gọi PartialSign offline → gửi lại partial;
// (2) một "combiner" gọi BuildMultisigTx để gộp và submit.
func BuildMultisigTx(
	txConfig client.TxConfig,
	key MultisigKey,
	msgs []sdk.Msg,
	gasLimit uint64,
	fee sdk.Coins,
	accNum, seq uint64,
	partials []signingtypes.SignatureV2, // partial của từng thành viên (SingleSignatureData)
) ([]byte, error) {
	msPub := key.AminoPubKey()

	// 1. Gộp các partial vào một MultiSignatureData theo đúng BitArray.
	msData := multisig.NewMultisig(len(key.PubKeys))
	for _, p := range partials {
		if err := multisig.AddSignatureV2(msData, p, key.PubKeys); err != nil {
			return nil, err
		}
	}

	// 2. Dựng tx: set msg/fee/gas rồi set MỘT SignatureV2 cho signer multisig.
	b := txConfig.NewTxBuilder()
	if err := b.SetMsgs(msgs...); err != nil {
		return nil, err
	}
	b.SetGasLimit(gasLimit)
	b.SetFeeAmount(fee)
	sig := signingtypes.SignatureV2{
		PubKey:   msPub,
		Data:     msData,
		Sequence: seq,
	}
	if err := b.SetSignatures(sig); err != nil {
		return nil, err
	}
	return txConfig.TxEncoder()(b.GetTx())
}

// PartialSign: một thành viên ký SignDoc bằng khoá riêng của mình, trả về partial.
// signMode nên đồng nhất giữa các thành viên (DIRECT hoặc LEGACY_AMINO_JSON).
func PartialSign(
	txConfig client.TxConfig,
	member cryptotypes.PrivKey,
	msPub *kmultisig.LegacyAminoPubKey,
	chainID string,
	accNum, seq uint64,
	msgs []sdk.Msg, gasLimit uint64, fee sdk.Coins,
	signMode signingtypes.SignMode,
) (signingtypes.SignatureV2, error) {
	b := txConfig.NewTxBuilder()
	_ = b.SetMsgs(msgs...)
	b.SetGasLimit(gasLimit)
	b.SetFeeAmount(fee)
	// Placeholder sig để builder biết signer là multisig (cần cho GetSignBytes).
	_ = b.SetSignatures(signingtypes.SignatureV2{PubKey: msPub, Data: &signingtypes.MultiSignatureData{
		BitArray: nil, Signatures: nil,
	}, Sequence: seq})

	signerData := authsigning.SignerData{
		ChainID: chainID, AccountNumber: accNum, Sequence: seq, PubKey: msPub,
	}
	bytesToSign, err := authsigning.GetSignBytesAdapter(
		nil, txConfig.SignModeHandler(), signMode, signerData, b.GetTx(),
	)
	if err != nil {
		return signingtypes.SignatureV2{}, err
	}
	sigBytes, err := member.Sign(bytesToSign)
	if err != nil {
		return signingtypes.SignatureV2{}, err
	}
	return signingtypes.SignatureV2{
		PubKey: member.PubKey(),
		Data:   &signingtypes.SingleSignatureData{SignMode: signMode, Signature: sigBytes},
		Sequence: seq,
	}, nil
}
```

Trong đó `txConfig` lấy từ app: `tx.NewTxConfig(codec, tx.DefaultSignModes)` (hoặc
tái dùng `app.TxConfig()`). `accNum`/`seq` lấy từ `client.FetchAccount(multisigAddr)`
(dùng lại đúng đường [§1.b](#b-authaccount-peek-số-account-tương-lai)).

> Vì sao cần `PartialSign` tách riêng: mỗi thành viên thường ở **máy/ví khác nhau**,
> chỉ trao đổi *partial signature* (không lộ khoá riêng). "Combiner" gom đủ K partial
> rồi mới `BuildMultisigTx`. Nếu mọi khoá nằm cùng một chỗ (test), có thể gọi tuần tự.

### 5.3 Option 2 — example 2-of-3 chạy trên local devnet

Đặt vào `apps/cosmos-exec/sdk/cosmoswasm/examples/multisig/main.go`. Khung luồng
(ghép với devnet ở [local-devnet](../../examples/local-devnet)):

```go
func main() {
	// 0. Boot devnet (hoặc dùng endpoint có sẵn); yêu cầu ENFORCE_SIGNATURES=true.
	//    Xem examples/local-devnet để biết cách StartDALChain.
	client := cosmoswasm.NewClient(execAPI)
	ctx := context.Background()

	// 1. Tạo 3 khoá thành viên → nhóm 2-of-3.
	m1, m2, m3 := secp256k1.GenPrivKey(), secp256k1.GenPrivKey(), secp256k1.GenPrivKey()
	key := cosmoswasm.MultisigKey{
		Threshold: 2,
		PubKeys:   []cryptotypes.PubKey{m1.PubKey(), m2.PubKey(), m3.PubKey()},
	}
	msAddr := key.Address()
	fmt.Println("multisig address:", msAddr)

	// 2. Cấp tiền cho địa chỉ multisig NẾU chain ép phí:
	//    - hoặc bật faucet (COSMOS_EXEC_TREASURY_PRIVKEY_HEX) rồi POST /faucet {address: msAddr}
	//    - hoặc để fee = 0 khi COSMOS_EXEC_MIN_GAS_PRICE không set (AutoCreate lo phần account).

	// 3. Lấy accNum/seq của account multisig (peek nếu tx đầu).
	info, _ := client.FetchAccount(ctx, msAddr)

	// 4. Soạn msg (vd MsgStoreCode / MsgExecuteContract) + fee/gas.
	msgs := []sdk.Msg{ /* ... */ }
	gas, fee := uint64(2_000_000), sdk.NewCoins( /* ustake nếu ép phí */ )

	// 5. Hai thành viên ký partial (đủ ngưỡng K=2), CÙNG SignDoc.
	mode := signingtypes.SignMode_SIGN_MODE_LEGACY_AMINO_JSON
	p1, _ := cosmoswasm.PartialSign(txCfg, m1, key.AminoPubKey(), chainID, info.AccountNumber, info.Sequence, msgs, gas, fee, mode)
	p2, _ := cosmoswasm.PartialSign(txCfg, m2, key.AminoPubKey(), chainID, info.AccountNumber, info.Sequence, msgs, gas, fee, mode)

	// 6. Gộp + encode + submit.
	raw, _ := cosmoswasm.BuildMultisigTx(txCfg, key, msgs, gas, fee, info.AccountNumber, info.Sequence, []signingtypes.SignatureV2{p1, p2})
	sub, _ := client.SubmitTxBytes(ctx, raw)

	// 7. Chờ kết quả — ante verify 2/3 chữ ký con qua VerifyMultisignature.
	res, _ := client.WaitTxResult(ctx, sub.Hash, cosmoswasm.DefaultPollInterval)
	fmt.Printf("multisig tx=%s code=%d height=%d\n", sub.Hash, res.Code, res.Height)
}
```

**Kịch bản test nên phủ:**
- **2/3 hợp lệ** → `code=0`. Đây là happy path.
- **1/3** (thiếu ngưỡng) → `SigVerification` reject (`VerifyMultisignature` thất bại).
- **2/3 nhưng một partial ký sai `SignDoc`** (vd lệch `sequence`) → reject.
- **Tx thứ hai** của cùng multisig account → nhánh AutoCreate skip, chỉ tăng `sequence`.
- **N = 8** với `TxSigLimit` mặc định 7 → `ValidateSigCount` reject (kiểm tra ràng buộc N ≤ 7).

> **Lưu ý fee với multisig:** nếu bật `COSMOS_EXEC_MIN_GAS_PRICE`, account multisig
> phải có số dư để `DeductFee` trừ. AutoCreate tạo account nhưng **không cấp tiền** →
> phải qua faucet ([§ đầu trang](#vấn-đề)) hoặc `MsgSend` cấp trước, y như account thường.
