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

---

## 1. Tx đầu tiên permissionless (không faucet)

### Vấn đề

Trong chain Cosmos SDK chuẩn, user mới không submit tx được tới khi account của
họ tồn tại trong state `x/auth`. Bình thường faucet gửi token trước (qua
`MsgSend`), side-effect gọi `AccountKeeper.NewAccount`. cosmos-exec chạy không
có fee token và không faucet, nên đường bootstrap đó không tồn tại.

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

`AutoCreateAccount` duyệt `tx.GetSignaturesV2()` và, với mỗi signer chưa có địa
chỉ trong `AccountKeeper`, gọi `NewAccountWithAddress` + `SetAccount`. Account
mới nhận `account_number` toàn cục duy nhất từ sequence `GlobalAccountNumber`.

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
