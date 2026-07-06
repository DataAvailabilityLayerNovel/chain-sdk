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

#### "So AppHash" cụ thể là so cái gì

**AppHash = IAVL merkle root commit TOÀN BỘ app state** sau `Commit()`: mọi balance account, mọi ô contract state, mọi module store gộp thành **một** hash 32 byte. Đổi đúng 1 utoken ở 1 account → root đổi hoàn toàn. Nên `AppHash` là "dấu vân tay" không giả được của state.

**Vì sao re-execute cho ra đúng cùng root** (điều kiện để so sánh có nghĩa):

- Full node chạy `ExecuteTxs` qua **đúng code path** sequencer dùng khi sản xuất: `FinalizeBlock` → ante chain → msg handler → `Commit`.
- **Không có nguồn bất định**: `time` lấy từ `header.Time()` chứ không phải `time.Now()`; không random; IAVL ghi deterministic.
- ⇒ cùng bộ `(prevAppHash, txs, height, time)` **luôn** ra cùng `newAppHash`. Nếu sequencer commit một AppHash khác kết quả canonical → full node phát hiện.

**Cheat được phát hiện thế nào — ví dụ số (chú ý độ trễ 1 block).** Giả sử sequencer chạy **state machine sửa đổi** (binary vá) để mint lậu ở block N; data blob của N vẫn chứa các tx thật:

| Block đến | `header.AppHash` (sequencer ghi = state SAU block trước) | `state.AppHash` (full node tự tính, canonical) | `AssertValidSequence` |
|-----------|--------------------------------------------------------|-----------------------------------------------|-----------------------|
| N-1 | `R0` (sau N-2) | `R0` | khớp → exec N-1 → full node lưu `R1` |
| N | `R1` (sau N-1) | `R1` | khớp → exec N bằng **code canonical** → full node lưu `R2_thật` (KHÔNG phải `R2_cheat`) |
| N+1 | `R2_cheat` (sau N theo nhánh giả của sequencer) | `R2_thật` | **lệch → `invalid last app hash` → halt** |

Hai điểm rút ra:

- **Full node không bao giờ "nhận" state giả.** Ngay ở block N nó đã tự tính `R2_thật` và lưu cái đó; AppHash sequencer *tự nghĩ* (`R2_cheat`) không đi vào state full node. Cheat chỉ **lộ** khi N+1 tới mang `header.AppHash = R2_cheat`.
- **Độ trễ đúng 1 block.** `header.AppHash` của block N cam kết state *sau N-1*, nên sai lệch do cheat ở N chỉ so được khi block N+1 xuất hiện (chính là dòng cuối bảng). Nếu sequencer cheat rồi **không ra N+1** → chain dừng, và honest full node vẫn đang giữ `R2_thật` — cheat vô hại, chỉ mất liveness.

### 2.4 Audit forced-inclusion sau commit

Ngay sau commit, syncer chạy thêm 1 check riêng cho censorship ([syncer.go:893](../../../../../block/internal/syncing/syncer.go#L893) `VerifyForcedInclusionTxs`):

- Lấy danh sách tx đã publish vào forced-inclusion namespace trên DA trong epoch đã qua grace period.
- Mỗi tx đó **phải** xuất hiện trong một block đã commit trước hạn (bất kỳ block nào).
- Nếu sequencer skip tx forced-inclusion quá grace period → trả `errMaliciousProposer` ([syncer.go:851](../../../../../block/internal/syncing/syncer.go#L851)) → full node dừng chain.

→ Censorship không bền vững được: hoặc sequencer include trong grace period, hoặc full node tự dừng và operator buộc phải xử lý.

### 2.5 Sequencer skip tx hoặc cố tình sắp xếp sai thì sao?

Đây là ranh giới tin cậy quan trọng nhất của single sequencer — cần tách bạch **cái full node CHẶN được** khỏi **cái nó KHÔNG chặn**:

#### Trước tiên: full node re-execute tx lấy từ ĐÂU? (KHÔNG phải mempool)

Hiểu lầm hay gặp: "full node biết mempool nên bắt được reorder". **Sai** — full node **không nhìn thấy mempool của sequencer**. Nó tính lại AppHash từ 3 đầu vào, tất cả đều đã commit và **tải về từ Celestia DA**:

| Đầu vào để re-execute | Lấy từ đâu |
|---|---|
| `prevAppHash` | **state của chính full node** sau khi exec block N-1 |
| **Danh sách tx + đúng thứ tự** | **data blob** sequencer đã publish lên DA (khóa bởi `header.DataHash == DACommitment(data)`, [§2.2](#22-validate-header--data-chưa-execute)) |
| `height`, `time` | `header` (cũng trên DA) |

Nên full node chỉ biết **những tx sequencer đã CHỌN đưa vào block**, theo **đúng thứ tự đã commit** — chứ **không** biết mempool ban đầu có tx nào, đến theo thứ tự nào. Hệ quả trực tiếp:

- Verify được **KẾT QUẢ** (AppHash của list đã commit) ✅ — đây là lý do cheat state bị bắt ([§2.3](#23-execute-lại-tx--so-apphash)).
- Nhưng **không có gì để đối chiếu SỰ LỰA CHỌN & THỨ TỰ** ❌ — không biết tx nào bị drop, tx nào bị tráo chỗ.

> Ẩn dụ: full node thấy được "sequencer nấu món gì, có đúng công thức không", nhưng **không thấy trong bếp còn nguyên liệu nào bị vứt đi hay bị đảo thứ tự**. Đó là gốc rễ của mọi ô ❌ trong bảng dưới.

| Hành vi sequencer | Full node chặn? | Vì sao | Cứu cánh |
|---|---|---|---|
| Sửa state (mint lậu, trừ phí sai, đổi balance) | ✅ **Halt** | AppHash mismatch ([§2.3](#23-execute-lại-tx--so-apphash)) | tự động |
| Nhân đôi / bỏ / fork block | ✅ **Halt** | `Height == prev+1`, `LastHeaderHash` liên tục ([§2.2](#22-validate-header--data-chưa-execute)) | tự động |
| Skip tx **forced-inclusion** quá grace period | ✅ **Halt** | audit epoch → `errMaliciousProposer` ([§2.4](#24-audit-forced-inclusion-sau-commit)) | tự động |
| Skip / drop tx trong **mempool** | ❌ **Không** | không có bằng chứng on-chain rằng tx từng được gửi tới sequencer | user **re-submit qua forced inclusion** |
| **Sắp xếp lại thứ tự** tx mempool (MEV, front-run, sandwich) | ❌ **Không** | không tồn tại "thứ tự đúng" chuẩn để đối chiếu; full node chỉ **chạy lại đúng thứ tự sequencer đã commit** | based sequencer; encrypted mempool (tương lai) |
| Đổi thứ tự để ra state khác | ⚠️ **Một phần** | thứ tự là quyền của sequencer, NHƯNG `AppHash` **phải khớp** thứ tự đã commit → không nói dối được *kết quả* của thứ tự đó | — |

**Chốt lại — full node đảm bảo 3 thứ, KHÔNG đảm bảo thứ 4:**

1. **State correctness** — kết quả của *đúng những tx theo đúng thứ tự đã commit* là canonical (không mint lậu, không trừ sai). ✅
2. **No-fork / liên tục** — không chèn lén, không tua lại, không hai nhánh. ✅
3. **Eventual inclusion** — tx bạn đẩy vào forced-inclusion namespace **chắc chắn** vào chain trong grace period, nếu không chain halt. ✅
4. **Ordering fairness cho tx mempool** — **KHÔNG**. Sequencer toàn quyền chọn thứ tự (và drop) tx mempool. Đây chính là **quyền lực còn lại duy nhất** của single sequencer và là nguồn **MEV/front-running**.

Nói cách khác: bạn **không phải tin** sequencer về *tính đúng của state* hay *chống kiểm duyệt vĩnh viễn*, nhưng **vẫn phải tin** nó về *thứ tự công bằng* của tx mempool. Muốn bỏ luôn điểm tin này:

- **Based sequencer** ([§3](#3-hai-chế-độ-sequencer)) — không mempool, thứ tự = thứ tự tx trên DA → sequencer **hết** quyền sắp xếp.
- **Forced inclusion** ([§4](#4-forced-inclusion--chống-kiểm-duyệt-khi-vẫn-dùng-single-sequencer)) — đảm bảo tx **vào được** chain (vị trí trong block vẫn linh hoạt, nhưng không bị chặn).
- **Encrypted/threshold mempool** (hướng tương lai) — sequencer sắp thứ tự khi **chưa** giải mã nội dung → không front-run được.

> Lưu ý phân biệt **skip** vs **reorder** với tx mempool: cả hai đều **không bị full node bắt** vì cùng một lý do — mempool là vùng *ngoài DA*, không có dấu vết để chứng minh. Chỉ khi tx đã nằm trên DA (forced-inclusion namespace) thì việc bỏ qua mới trở thành **bằng chứng** để buộc tội `errMaliciousProposer`.

#### MEV / front-run / sandwich là gì

**MEV (Maximal Extractable Value)** = lợi nhuận bên sản xuất block (ở đây là **sequencer**) bòn rút được nhờ **toàn quyền quyết include tx nào, bỏ tx nào, xếp thứ tự nào**. Vì mempool thường cho sequencer thấy **nội dung plaintext** của tx *trước khi* chốt thứ tự, nó chèn được lệnh của chính mình vào vị trí có lợi:

- **Front-run** (chạy trước): thấy nạn nhân sắp mua lớn (sẽ đẩy giá lên) → chèn lệnh mua của mình **ngay TRƯỚC** → mua giá rẻ, lời khi giá tăng.
- **Back-run** (chạy sau): chèn lệnh **ngay SAU** một tx đã biết (vd arbitrage sau một cú swap lớn).
- **Sandwich** (kẹp): front-run + back-run cùng lúc, kẹp nạn nhân ở giữa — dạng thao túng phổ biến và độc nhất.

Điều kiện để thao túng = **biết nội dung tx** + **quyền xếp thứ tự**. Bỏ một trong hai là chặn được.

#### Ví dụ: sequencer sandwich một lệnh swap

Nạn nhân gửi vào mempool: **`swap 10.000 USDC → TOKEN`** trên một AMM (lệnh mua lớn, sẽ đẩy giá TOKEN lên ~5%). Sequencer thấy plaintext, tự dựng thứ tự block:

```
vị trí 1 (sequencer chèn):  swap 5.000 USDC → TOKEN     ← mua TRƯỚC, giá còn rẻ
vị trí 2 (nạn nhân):        swap 10.000 USDC → TOKEN    ← đẩy giá lên, nhận ÍT TOKEN hơn (trượt giá)
vị trí 3 (sequencer chèn):  swap TOKEN → USDC           ← bán NGAY ở giá vừa bị đẩy cao
```

Sequencer lời phần chênh; nạn nhân mua đắt (có thể chạm slippage). **Full node chạy lại đúng 3 tx theo đúng thứ tự này → AppHash khớp → block HỢP LỆ, không bị reject.** State hoàn toàn "đúng" *cho thứ tự đó* — chỉ là thứ tự do sequencer cố ý dựng. Đây chính xác là ô "⚠️ Một phần / ❌ Không" ở bảng trên: full node đảm bảo *kết quả đúng với thứ tự đã commit*, **không** đảm bảo *thứ tự đó công bằng*.

#### Encrypted mempool chống thao túng thế nào

**Mempool thường: sequencer đọc được TOÀN BỘ nội dung plaintext** (msg gì, số tiền bao nhiêu, contract nào) *trước khi* chốt thứ tự — chính cái "thấy trước" này bật đèn xanh cho front-run/sandwich.

**Encrypted mempool bẻ gãy điều kiện "biết nội dung"**: user **mã hóa** tx trước khi gửi, đảo ngược luồng:

```
1. User mã hóa tx → gửi CIPHERTEXT   (sequencer KHÔNG đọc được nội dung)
2. Sequencer xếp thứ tự các ciphertext MÙ (không biết trong đó là gì)
3. Thứ tự được CHỐT (commit) trên DA
4. SAU KHI chốt mới giải mã → thực thi theo đúng thứ tự đã khóa
```

Vì phải chốt thứ tự **khi còn mù**, sequencer không thể chèn lệnh dựa trên nội dung nạn nhân → hết front-run. Các cách hiện thực "chỉ giải mã sau khi thứ tự đã cố định":

- **Threshold encryption** — một ủy ban giữ các **mảnh khóa**; chỉ giải mã được khi đủ ngưỡng, và chỉ giải **sau** khi thứ tự đã commit. Sequencer một mình **không** có khóa.
- **Time-lock / VDF** — tx khóa bằng câu đố cần thời gian giải; đến lúc giải xong thì thứ tự đã chốt.
- **Commit-reveal** — user gửi **hash** (commit) trước, **lộ** nội dung (reveal) sau khi thứ tự đã cố định.

Điểm chung của cả ba: **tách "chốt thứ tự" ra TRƯỚC "biết nội dung"** → sequencer mất khả năng thao túng theo nội dung.

**Ba lớp phòng thủ — chống được gì:**

| Cơ chế | Chống censor (skip)? | Chống MEV/reorder? | Đánh đổi |
|---|:---:|:---:|---|
| **Based sequencer** ([§3](#3-hai-chế-độ-sequencer)) | ✅ (tx từ DA) | ✅ (thứ tự = thứ tự DA, sequencer hết quyền xếp) | mất mempool UX, độ trễ cao (chờ epoch DA) |
| **Forced inclusion** ([§4](#4-forced-inclusion--chống-kiểm-duyệt-khi-vẫn-dùng-single-sequencer)) | ✅ (đảm bảo vào chain) | ❌ (không đụng tới thứ tự/nội dung) | tx vào chậm (grace period), vị trí vẫn do sequencer |
| **Encrypted mempool** (tương lai) | ❌ | ✅ (sequencer xếp mù, không MEV theo nội dung) | cần hạ tầng khóa (committee/VDF), độ trễ giải mã |

#### Các chain / hệ khác giải quyết MEV thế nào

Không có "viên đạn bạc". Thực tế chia làm hai triết lý: **loại bỏ** MEV (không ai front-run được) hay **thu hồi & tái phân phối** MEV (vẫn có, nhưng lợi nhuận về tay validator/user/giao thức thay vì searcher ẩn danh). Bốn nhóm cách tiếp cận chính:

**A. Encrypted / delayed mempool — xếp thứ tự khi còn "mù"** (cùng họ với lớp phòng thủ ở trên):
- **Shutter Network** (trên Gnosis Chain) — threshold encryption bằng nhóm "keypers"; tx mã hóa, chốt thứ tự xong keypers mới giải mã.
- **Radius** — shared sequencer cho rollup, dùng **PVDE** (practical verifiable delay encryption): không cần committee giữ khóa, dựa vào time-lock.
- **Penumbra** (Cosmos) — DEX chống MEV *by design*: mọi swap trong 1 block gộp thành **batch**, số lượng được **mã hóa (ZK)** và khớp ở **một giá chung** → không có vị trí nào để kẹp.

**B. Batch auction / một giá chung — xóa lợi thế thứ tự trong batch:**
- **CoW Protocol** (app-level trên Ethereum) — gom lệnh thành batch, solver cạnh tranh settle, **mọi lệnh cùng batch nhận cùng clearing price** → sandwich nội-batch vô nghĩa; tận dụng "Coincidence of Wants".
- Cùng ý tưởng "frequent batch auction" từng được **Sei** thử ở giai đoạn đầu (gộp lệnh trong block, khớp một giá) để giảm front-run.

**C. PBS + order-flow auction — tách người xây block, đấu giá MEV rồi tái phân phối:**
- **Ethereum: PBS + MEV-Boost** — tách **builder** (xây block, gồm cả MEV) khỏi **proposer** (chỉ chọn block trả giá cao nhất). MEV **không mất** nhưng chảy về validator qua đấu giá thay vì vài searcher; đang tiến tới **enshrined PBS** và đề xuất **MEV-burn**.
- **Flashbots** — **private order-flow** (gửi tx thẳng cho builder, bỏ qua public mempool → kẻ front-run không thấy); **MEV-Share** trả lại một phần MEV cho user; **SUAVE** hướng tới mempool/sequencing phi tập trung.
- **Jito** (Solana) — hệ đấu giá + "bundle" kiểu Flashbots cho Solana.
- **Cosmos: Skip Block SDK / POB** — "MEV lane" đấu giá top-of-block, thu MEV cho chain/staker; nhiều Cosmos chain dùng.
- **Osmosis** — module **ProtoRev** tự bắt **backrun arbitrage** đưa lợi nhuận về giao thức/cộng đồng thay vì searcher ngoài.

**D. Fair-ordering — ép thứ tự theo thời điểm nhận, bỏ quyền tùy ý:**
- **Arbitrum** — sequencer xếp **FCFS** (first-come-first-served theo thời điểm đến), triệt tiêu đấu giá gas kiểu front-run; gần đây thêm **Timeboost** (đấu giá "express lane" có kiểm soát).
- **Chainlink FSS**, **Aequitas/Themis** — giao thức đồng thuận *fair ordering*: nhiều node cùng quyết thứ tự theo thứ tự nhận tx, không để một bên tùy ý.

**Map về stack này:**

| Nhóm | Triết lý | Tương ứng lớp phòng thủ của ev-node |
|---|---|---|
| A. Encrypted/delayed | Loại bỏ (xếp mù) | **Encrypted mempool** (§2.5 ở trên) — cùng ý tưởng |
| B. Batch auction | Loại bỏ (một giá chung) | app-level: hợp đồng CosmWasm tự làm batch/uniform-price |
| C. PBS / order-flow auction | Tái phân phối | không sẵn — cần lớp builder/auction ngoài |
| D. Fair-ordering | Loại bỏ (theo thời gian) | **Based sequencer** gần nhất (thứ tự = thứ tự DA, không tùy ý) |

> Rút ra cho dApp trên cosmos-exec: nếu **liveness/chống MEV là ưu tiên tối đa** → **based sequencer** (nhóm D). Nếu vẫn muốn mempool UX → chờ **encrypted mempool** (nhóm A) hoặc **tự chống ở tầng hợp đồng** bằng batch auction / commit-reveal / uniform price (nhóm B) — cái này bạn hiện thực được ngay trong CosmWasm mà không cần đổi sequencer.

#### Đấu giá MEV (MEV auction) — chi tiết nhóm C

Triết lý nhóm C **khác hẳn** nhóm A/B/D: không cố *xóa* MEV (rất khó và tốn kém), mà **biến MEV thành một thứ đấu giá công khai** rồi **hướng lợi nhuận về đúng chỗ** (validator / user / kho bạc giao thức) thay vì để nó rò rỉ ngầm cho searcher ẩn danh qua front-run. Ý tưởng gốc: *"quyền sắp xếp có giá trị → bán đấu giá cái quyền đó minh bạch, thu tiền về."*

**Ai tham gia:**
- **Searcher** — bot phát hiện cơ hội MEV (arbitrage, thanh lý, backrun), đóng gói thành **bundle** (một chuỗi tx theo thứ tự cố định) kèm **một khoản bid**.
- **Builder / auctioneer** — nơi nhận bundle + bid, chọn người trả cao nhất.
- **Proposer / sequencer** — bên có quyền quyết nội dung block; nhận tiền bid.

**Ba kiểu đấu giá phổ biến:**

| Kiểu | Bán cái gì | Ai nhận tiền | Ví dụ |
|---|---|---|---|
| **Top-of-block (TOB)** | Vài slot ĐẦU block (cho arb/thanh lý) | proposer / kho bạc | Skip Block SDK "MEV lane", Osmosis ProtoRev (tự backrun) |
| **Whole-block (PBS)** | Toàn bộ nội dung block | proposer | Ethereum PBS + MEV-Boost |
| **Order-flow auction (OFA)** | Quyền thực thi/backrun MỘT tx của user | **trả lại user** một phần | Flashbots MEV-Share |

**Luồng một TOB auction (dễ hình dung nhất):**

```
1. Searcher thấy cơ hội (vd arb 2 pool lệch giá) → đóng bundle: [tx_arb...]  + bid = 8 (trả cho proposer)
2. Nhiều searcher cùng gửi bid cạnh tranh → 8, 10, 12...
3. Auctioneer chọn bid CAO NHẤT (12) → đặt bundle đó ở TOP block
4. Số 12 được phân phối: → validator/staker, HOẶC → kho bạc giao thức, HOẶC → chia lại user
5. Phần còn lại của block xếp như bình thường
```

**Vì sao "đấu giá" tốt hơn "để mặc":** nếu không đấu giá, MEV vẫn tồn tại nhưng bị **vài searcher tinh vi + sequencer** ăn ngầm qua front-run/sandwich (hại user, không minh bạch). Đấu giá **công khai hóa** cuộc cạnh tranh đó → (a) giá trị về tay người đáng nhận (staker/kho bạc/user) thay vì rò rỉ; (b) giảm "gas war" (searcher không còn spam nâng gas để giành thứ tự); (c) đo đếm được.

> **Lưu ý quan trọng — đấu giá TÁI PHÂN PHỐI chứ không LOẠI BỎ.** Nạn nhân của một cú arb/backrun vẫn có thể chịu tác động giá; điểm khác là lợi nhuận **không còn chảy ngầm**. Muốn *loại bỏ* thao túng theo nội dung thì vẫn phải dùng nhóm A (encrypted) hoặc B (batch/uniform price). Nhiều hệ **kết hợp**: encrypted mempool (chống front-run) + auction cho phần MEV "lành tính" như arbitrage.

**Áp vào rollup single-sequencer này.** Ở đây **sequencer chính là builder** (một mình dựng block), nên về lý thuyết nó **tự chạy được một TOB auction**: mở một "lane" nhận `MsgAuctionBid` (bundle + bid), chọn bid cao nhất đặt top-of-block, và — điểm hay — **hướng khoản bid về ví treasury** đúng bằng cơ chế [sweep phí cuối block](fee-economics.md#sweep-phi-cuoi-block) đã có. Hiện **stack chưa hiện thực** phần này (xem ô "C — không sẵn" ở bảng map trên); nó là hướng mở rộng, và **không đơn giản** vì:

- Cần định nghĩa message/lane đấu giá + luật chọn bid trong đường sản xuất block (đụng `block/` + executor), **không** chỉ là contract.
- Đấu giá do một sequencer tin cậy điều hành vẫn cần giả định nó **không tự ăn gian bid** (không bí mật chèn bundle của chính nó) — trớ trêu là quay lại đúng điểm tin mà ta muốn bỏ. Vì thế các hệ nghiêm túc thường ghép PBS/auction **với** một cơ chế cam kết/chống-gian (commit hoặc phi tập trung builder).

→ Cho một dApp cụ thể không muốn chờ hạ tầng đó, **nhóm B ở tầng contract vẫn là đường ngắn nhất** (0 thay đổi framework); đấu giá MEV kiểu nhóm C hợp lý hơn khi bạn vận hành nhiều dApp và muốn thu MEV về kho bạc chung.

### 2.6 Bảng tóm tắt "ai check gì, fail trả lỗi gì"

| Câu hỏi | Bằng chứng | Reject với lỗi |
|---|---|---|
| Header có đúng sequencer ký? | Verify chữ ký trên payload header | `ErrSignatureVerificationFailed` |
| Data có khớp header? | `DACommitment(data) == header.DataHash` | `header-data validation failed` |
| Có chèn lén / skip block? | `LastHeaderHash`, `Height == prev+1` | `invalid last header hash` / `invalid block height` |
| Sequencer cheat state? | Re-execute → so AppHash ở block kế | `invalid last app hash` |
| Sequencer censor? | Forced-inclusion audit qua epoch | `errMaliciousProposer` |
| Data còn tồn tại? | Header + data đều phải on Celestia | DA fetch fail → block không advance |

**Trust assumption duy nhất còn lại:** DA layer (Celestia) live và phục vụ data. Mất Celestia → không verify được nữa, nhưng kẻ tấn công cũng không lừa được state — chỉ là chain ngừng tiến.

### 2.7 Kẻ khác ghi CÙNG namespace trên Celestia thì sao?

Namespace Celestia **không permissioned** — bất kỳ ai cũng publish blob vào **bất kỳ** namespace, kể cả namespace chain bạn đang dùng. Câu hỏi đúng không phải "làm sao cấm họ ghi" (không cấm được) mà "full node **lọc** data thật khỏi rác/độc thế nào".

**Kết luận: không giả mạo/không làm loạn state được.** An ninh **không dựa vào namespace bí mật** mà dựa vào **chữ ký + proposer address trong genesis**. Blob của kẻ khác nhét vào namespace của bạn bị full node **drop** ngay khi retrieve.

#### Hai lớp lọc khi quét blob từ DA ([da_retriever.go](../../../../../block/internal/syncing/da_retriever.go))

Full node **không tin** blob nào; với mỗi blob đọc được ở một DA height:

**Header blob** (`tryDecodeHeader`):
1. Parse được thành `SignedHeader`? Không → bỏ (rác).
2. **Verify chữ ký envelope** bằng `header.Signer.PubKey` ([da_retriever.go:338](../../../../../block/internal/syncing/da_retriever.go#L338)) → sai → bỏ.
3. **`assertExpectedProposer`** — proposer phải khớp **`genesis.ProposerAddress`** ([assert.go:12](../../../../../block/internal/syncing/assert.go#L12)) → khác → bỏ.

**Data blob** (`tryDecodeData` → `assertValidSignedData`, [assert.go:20](../../../../../block/internal/syncing/assert.go#L20)):
1. Signer phải là proposer trong genesis **và** chữ ký hợp lệ trên payload data.
2. Data còn phải khớp `header.DataHash` (`DACommitment`) của một header **đã verify** — junk không khớp header nào → vô nghĩa.

Kẻ tấn công **không có private key của sequencer** → mọi header/data nó nhét vào đều fail bước 2/3 → **bị drop**. Không có chuyện hai chain trộn vào nhau, không forge được block.

> **Strict mode:** một khi full node thấy một envelope hợp lệ, nó chuyển sang strict và **từ chối luôn** mọi blob không đúng định dạng envelope ([da_retriever.go:346](../../../../../block/internal/syncing/da_retriever.go#L346)) — càng khó chèn rác.

#### Vậy kẻ đó làm được gì? — chỉ griefing/DoS, KHÔNG corrupt

| Tác động | Xảy ra? | Ghi chú |
|---|:---:|---|
| Giả mạo block / sửa state / fork | ❌ | thiếu khóa sequencer + proposer check |
| Nhận nhầm data giả | ❌ | signature + `DataHash` phải khớp header đã verify |
| **Spam blob → full node tốn băng thông/CPU để tải + verify rồi vứt** | ⚠️ | griefing thuần: nhiều blob rác hơn mỗi height |
| **Spam namespace forced-inclusion** | ⚠️ | namespace này vốn permissionless (cố ý); tx vẫn qua ante (chữ ký/fee) nên không corrupt, chỉ bắt sequencer scan/xử lý nhiều hơn |

→ Tệ nhất là **tốn tài nguyên**, không mất an toàn state.

#### Thực hành

- **Không** cần giữ namespace bí mật để an toàn — bảo mật là mật mã, lộ namespace vẫn an toàn về tính đúng.
- **Vẫn nên** chọn namespace riêng/ít đụng — không phải để bảo mật mà để **giảm nhiễu** (ít blob rác phải lọc → rẻ hơn). Đây là lý do mỗi app nên một namespace ([namespace.go](../namespace.go)).
- Chống spam forced-inclusion: **bật fee/ante** để tx rác vẫn phải trả phí → spam tốn tiền kẻ tấn công (xem [fee-economics.md](fee-economics.md)).

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

## 4b. Multi-sequencer HA (failover) — chống SPOF liveness

Single sequencer chết → chain **ngừng ra block** (không sai state — sovereign verification vẫn giữ, chỉ **treo**). Đây là rủi ro **liveness**, và **ev-node đã có sẵn cơ chế Raft HA** để giải quyết — bạn **không phải tự xây từ đầu**, chỉ cấu hình.

> **Lưu ý bản chất:** đây là **active-passive failover** (chịu lỗi *crash*), thường do **cùng một operator** chạy N node để không có SPOF hạ tầng. Nó **không** biến chain thành phi tập trung tin cậy (vẫn một bên điều hành cụm) và **không** tăng throughput — chỉ tăng **uptime**. Muốn bỏ điểm tin ordering thì vẫn là based sequencer/shared sequencer (cuối mục).

### Kiến trúc: Raft leader election

```
        Raft cluster (N node, cùng operator)
   ┌──────────┐   ┌──────────┐   ┌──────────┐
   │ node A    │   │ node B    │   │ node C    │
   │ LEADER    │   │ follower  │   │ follower  │
   │ =aggregator│  │ =sync mode│   │ =sync mode│
   │ produce   │   │ replicate │   │ replicate │
   └─────┬─────┘   └─────┬─────┘   └─────┬─────┘
         │ Raft log (RaftBlockState) replicate ↕        
         └───────────────┴───────────────┘
   Leader chết → Raft bầu lại → follower mới thành aggregator → produce tiếp
```

- **`DynamicLeaderElection`** ([pkg/raft/election.go](../../../../../pkg/raft/election.go)) — nghe sự kiện leadership của Raft: node **thành leader → chạy aggregator mode** (produce block); node **thành follower → chạy sync mode** (replicate + verify như full node). Cùng một binary, tự chuyển vai.
- **Failover** ([node/failover.go](../../../../../node/failover.go)) — `newAggregatorMode` / `newSyncMode` dựng lại đúng bộ component cho vai trò hiện tại khi chuyển.
- Leader chết → Raft bầu leader mới trong vài heartbeat → node đó chuyển sang aggregator và produce tiếp. Chain **tự hồi phục**, không cần can thiệp tay.

### Bốn điều kiện thiết kế BẮT BUỘC nắm

**1. Mọi instance phải ký bằng CÙNG một signer key.** Genesis pin **một** `ProposerAddress`, và full node reject header không khớp nó ([§2.7](#27-kẻ-khác-ghi-cùng-namespace-trên-celestia-thì-sao)). Nên leader nào lên cũng phải ký bằng **đúng khóa proposer đó** → header mới verify được. ⇒ Vận hành: **phân phối cùng private key** cho N node một cách an toàn (HSM / secret manager / KMS), đây là **bề mặt tấn công khóa lớn hơn** — đánh đổi của HA.

**2. Cần N lẻ ≥ 3 để có quorum thật.** Raft cần đa số `⌊N/2⌋+1`: N=3 chịu **1** node chết, N=5 chịu **2**. N=2 vô nghĩa (mất 1 là mất quorum). Trước **mỗi** block leader phải `HasQuorum()` — mất đa số thì **tự dừng** để chống **split-brain** (chi tiết ở [chain-flow.md §4.2](chain-flow.md), bước "CHECK RAFT QUORUM").

**3. Chống double-sign khi đổi leader.** Khi một node vừa được bầu, nó gọi `waitForMsgsLanded` để **áp dụng hết log Raft** trước khi produce ([election.go:98-99](../../../../../pkg/raft/election.go#L98)); nếu FSM còn lag → **abdicate** (nhường lại) thay vì ký block cũ → tránh hai leader cùng ký một height (double-sign = fork).

**4. Leader mới phải catch up trước khi produce.** `catchupEnabled` ([failover.go:163](../../../../../node/failover.go#L163)): node chạy syncer từ DA + P2P tới khi bằng head **rồi mới** bật block production, tránh produce chồng lên state cũ.

### Cấu hình (phác thảo)

Mỗi node bật Raft qua `raft.Config` ([pkg/raft/node.go](../../../../../pkg/raft/node.go)):

```go
raft.Config{
    NodeID:    "seq-a",                       // định danh trong cụm
    RaftAddr:  "10.0.0.1:9001",               // cổng Raft nội bộ
    RaftDir:   "/data/raft",                  // log + snapshot Raft
    Bootstrap: true,                          // CHỈ node khởi tạo cụm đặt true
    Peers:     []string{"seq-b@10.0.0.2:9001", "seq-c@10.0.0.3:9001"},
    // HeartbeatTimeout / LeaderLeaseTimeout: chỉnh độ nhạy failover
}
```

Cả N node cùng `Aggregator=true` (đều *có thể* làm leader) + cùng signer key + cùng genesis; Raft đảm bảo **tại một thời điểm chỉ một** node thực sự produce.

### Đánh đổi & các lựa chọn khác

| Cách | Chống được | Không giải quyết | Ghi chú |
|---|---|---|---|
| **Raft HA (mục này)** | SPOF liveness (crash 1 node) | điểm tin ordering (leader vẫn toàn quyền xếp → MEV còn); phi tập trung tin cậy | sẵn trong ev-node; cần shared key + N≥3 |
| **Based sequencer** ([§3](#3-hai-chế-độ-sequencer)) | cả liveness **và** điểm tin ordering | mempool UX, độ trễ (chờ epoch DA) | không có sequencer để chết |
| **Shared sequencer** (Astria, Espresso) | liveness + phi tập trung ordering | phụ thuộc mạng ngoài, tích hợp phức tạp | chưa tích hợp sẵn ở stack này |
| **Decentralized sequencer set (BFT)** | mọi thứ | = quay lại validator set + chi phí BFT | đi ngược mục tiêu "bỏ validator" của ev-node |

**Khuyến nghị theo nhu cầu:** cần uptime cao mà vẫn muốn mempool UX + tự vận hành → **Raft HA** (mục này). Cần chống-kiểm-duyệt/loại điểm tin ordering là chính → **based sequencer**. Cần phi tập trung ordering thật sự → hướng **shared sequencer** (roadmap).

### 4c. Thang phi tập trung — và "bậc 3/4 có phát minh lại CometBFT không?"

Phân biệt hai mục tiêu hay bị gộp: **tính sẵn sàng** (chain không treo khi 1 node chết) và **trung lập đáng tin cậy** (không phải tin một bên về *thứ tự*). Raft HA chỉ giải quyết cái đầu.

| Bậc | Cơ chế | Bỏ được gì | Dựng consensus BFT mới? | Sẵn trong ev-node? |
|:--:|---|---|:--:|:--:|
| 0 | Single sequencer | — | ❌ | ✅ |
| 1 | **Raft HA** (active-passive, 1 operator) | SPOF *tính sẵn sàng* (crash-fault) | ❌ (Raft = CFT, không BFT) | ✅ |
| 2 | **Based sequencer** (thứ tự = thứ tự DA) | *điểm tin ordering* | ❌ **mượn consensus của Celestia** | ✅ |
| 3 | **Multi-operator sequencer set + BFT** | *tập trung tin cậy* thật sự | ✅ (nhưng chỉ cho ordering) | ❌ future work |
| 4 | **Shared sequencer** (Astria/Espresso) | phi tập trung ordering liên-rollup | ♻️ dùng lại, externalize | ❌ tích hợp ngoài |

**Raft (bậc 1) ≠ decentralize.** Raft là **crash-fault-tolerant**: giả định các node **không ác ý** và thường **cùng một operator**. Nó bỏ SPOF *tính sẵn sàng* nhưng vẫn một bên điều hành → **chưa** trung lập. Phi tập trung *trust* thật sự cần **BFT** (node có thể ác ý), và đó là bước nhảy mô hình niềm tin, không phải "thêm node".

#### Bậc 3/4 có "phát minh lại bánh xe" (quay về CometBFT) không?

**Có dùng lại BFT, nhưng là BFT gọn hơn và đứng đúng chỗ — KHÔNG phải full circle về Cosmos chain truyền thống.** Ba điểm phân biệt:

1. **Chỉ đồng thuận THỨ TỰ (+ DA), không đồng thuận EXECUTION.** CometBFT trong một Cosmos chain thường: validator vừa order, vừa execute, vừa vote trên `app_hash` (state). Sequencer set (bậc 3) / shared sequencer (bậc 4) chỉ cần đồng thuận **thứ tự giao dịch**; tính đúng của state vẫn do **sovereign verification** ([§2](#2-full-node-verify-như-thế-nào--chi-tiết-kỹ-thuật)) — full node tự chạy lại từ DA. Đây là bài toán consensus **nhỏ hơn hẳn**: bỏ hẳn bước vote `app_hash`.

2. **Finality vẫn thuộc DA.** Vote của sequencer set chỉ cho **soft-ordering nhanh**; settlement/bất biến đến từ **Celestia**. Với CometBFT thường, vote của validator **chính là** finality. Ở đây consensus sequencer đứng **DƯỚI** DA, không phải điểm chốt cuối.

3. **Bậc 4 amortize + externalize.** Shared sequencer là **một mạng dùng chung nhiều rollup** — bạn không tự nuôi validator set mà "thuê" ordering. Về hiện thực, các mạng này *thực sự* chạy BFT: **Astria** là một chain **CometBFT** chỉ làm ordering; **Espresso** dùng **HotStuff** (HotShot). Nhưng chi phí "có một tập BFT" được **chia sẻ** và **tách khỏi rollup của bạn**.

→ Điểm **không đổi** qua mọi bậc: **execution luôn sovereign** (tách khỏi consensus). Cái "quay lại" chỉ là *đồng thuận-về-thứ-tự*, nó nhẹ và nằm dưới DA. Nên **không** đánh mất tính chất cốt lõi "bỏ validator-set-lo-execution" — vốn là điểm phân biệt của sovereign rollup với một Cosmos chain thường.

**Muốn decentralize mà KHÔNG dựng BFT mới → bậc 2 (based sequencer).** Based lấy thứ tự thẳng từ Celestia — mà Celestia bản thân đã là một chain CometBFT. Tức là bạn **tái dùng consensus có sẵn của lớp DA** cho ordering, **zero consensus mới**. Đây là cách tránh "phát minh lại bánh xe" triệt để nhất: không tự chạy BFT, mượn luôn của DA (đổi lại: mất mempool UX + độ trễ chờ epoch DA).

> **Tóm lại cho câu hỏi "có giống CometBFT không":** bậc 2 = *mượn* CometBFT của Celestia (không dựng mới). Bậc 3/4 = *dựng lại* một BFT **nhưng chỉ cho ordering, đứng dưới DA, và (bậc 4) chia sẻ nhiều rollup** — "rhymes with" CometBFT chứ không phải quay về chain Tendermint đầy đủ, vì execution vẫn sovereign.

### 4d. Nguyên lý & cách dùng từng bậc 2 / 3 / 4

Điểm chung của cả ba: **ai quyết "thứ tự giao dịch"** thay cho một single sequencer. Khác nhau ở *ai* và *tin cậy tới đâu*. Execution ở cả ba **vẫn sovereign** (full node tự verify state từ DA).

#### Bậc 2 — Based sequencer (thứ tự = thứ tự trên DA)

**Nguyên lý.** Bỏ hẳn mempool và quyền sắp xếp. "Sequencer" chỉ **đọc namespace trên Celestia theo đúng DA-height** và **suy ra block một cách tất định** — thứ tự giao dịch **chính là** thứ tự chúng lên DA. Vì Celestia đã sắp thứ tự blob bằng consensus của nó, rollup **thừa hưởng** thứ tự đó. Tên "based" = mượn ordering của lớp nền (giống "based rollup" bên Ethereum dùng L1 để sequencing).

```
User ──tx──► namespace trên Celestia ──(Celestia sắp thứ tự)──► DA height H
                                                                    │
     Mọi full node đọc H theo thứ tự → suy ra CÙNG một block (tất định)
```

**Cách dùng** (đã hỗ trợ sẵn):
- Bật `BasedSequencer = true` (yêu cầu `Aggregator = true`) trong `NodeConfig` ([§3](#3-hai-chế-độ-sequencer)).
- Không có `SubmitBatchTxs` (no mempool); `VerifyBatch` luôn `true` (tx từ DA đã verified); `GetNextBatch` kéo tx từ namespace DA.
- Người dùng **gửi tx thẳng lên DA** (đường forced-inclusion trở thành đường tx chính).

**Đánh đổi.** Ai cũng tái dựng được block từ DA → **không cần orderer tin cậy**. Nhưng: độ trễ cao (chờ DA + epoch), **mất mempool UX** (không soft-confirm tức thì, không ưu tiên theo phí), và **mỗi tx là một blob DA** → user gánh chi phí DA. Hợp với app **giá trị cao / tần suất thấp / cần trung lập tối đa**.

#### Bậc 3 — Multi-operator sequencer set + BFT (future work)

**Nguyên lý.** **Nhiều operator độc lập** cùng chạy sequencer, chạy một **đồng thuận BFT chỉ trên THỨ TỰ** (không trên execution): một leader (luân phiên) đề xuất batch đã sắp thứ tự → **≥ 2/3** đồng ý → batch được ký (ngưỡng) và publish lên DA. Chịu lỗi Byzantine: tối đa `f` node ác ý trong `3f+1`. State đúng vẫn do sovereign verification.

```
op1 ┐
op2 ├─ BFT trên THỨ TỰ (2/3+) → batch đã ký ngưỡng ──► DA ──► full node verify chữ ký của SET
op3 ┘        (leader luân phiên)
```

**Cách dùng / cần xây** (chưa có trong ev-node):
- Một lớp consensus BFT cho ordering giữa các sequencer (Tendermint/HotStuff…).
- **Bỏ khóa ký dùng chung** (điểm yếu của Raft HA) → **threshold signature / DKG**: không node nào giữ trọn khóa; block được ký bởi ngưỡng của set.
- **Genesis/verify phải nhận diện "set key"** thay vì một `ProposerAddress` đơn — tức `assertExpectedProposer` phải chấp nhận **pubkey ngưỡng của set** ([§2.7](#27-kẻ-khác-ghi-cùng-namespace-trên-celestia-thì-sao)).
- Thêm **luân phiên leader + accountability/slashing** cho hành vi ký nước đôi (equivocation).

**Đánh đổi.** Phi tập trung *trust* thật sự cho ordering, soft-confirm nhanh hơn based. Nhưng **tái nhập độ phức tạp BFT** giữa các orderer + cần **nhiều operator độc lập** (điều phối, kinh tế). Đây là bước nhảy mô hình niềm tin, không phải "thêm node".

#### Bậc 4 — Shared sequencer (Astria / Espresso — tích hợp ngoài)

**Nguyên lý.** **Thuê** một **mạng sequencing phi tập trung dùng chung nhiều rollup**. Rollup gửi tx tới mạng này; mạng (BFT của riêng nó) sắp thứ tự và tạo một dòng giao dịch có thứ tự (thường kèm/đẩy lên DA); rollup **chỉ đọc phần của mình rồi execute**. Việc phi tập trung/liveness do mạng chung lo; rollup tập trung vào execution. Ví dụ: **Astria** = một chain **CometBFT** chỉ làm ordering rồi đẩy lên Celestia; **Espresso** dùng **HotStuff (HotShot)**.

```
nhiều rollup ──tx──► Shared Sequencer Network (BFT dùng chung) ──► dòng đã sắp thứ tự ──► DA
                                                                     │
                            rollup của bạn đọc slice của mình → execute (sovereign)
```

**Cách dùng / cần tích hợp** (chưa có trong ev-node):
- Đường sản xuất block **đọc batch đã sắp thứ tự từ API/stream của shared sequencer** thay cho mempool nội bộ (adapter cho `Sequencer` interface kéo từ mạng ngoài).
- Tuân theo cơ chế commit/DA của mạng đó.

**Đánh đổi.** Không tự chạy consensus; có thể được **atomic composability liên-rollup**. Nhưng **phụ thuộc liveness/tin cậy của mạng ngoài** + độ phức tạp tích hợp + kinh tế/độ trễ theo mạng đó.

#### So sánh nhanh 3 bậc

| | Ai sắp thứ tự | Đường gửi tx | Ai tái dựng được block | Trạng thái ở ev-node |
|---|---|---|---|---|
| **2 Based** | lớp DA (Celestia) | tx → thẳng DA | mọi full node (tất định từ DA) | ✅ có (`BasedSequencer`) |
| **3 BFT set** | tập sequencer nhiều bên | tx → set (rồi lên DA) | full node verify chữ ký ngưỡng của set | ❌ future work |
| **4 Shared** | mạng ordering ngoài | tx → shared network | rollup đọc slice → execute | ❌ tích hợp ngoài |

> **Lộ trình thực dụng:** muốn có ngay & rẻ về công sức → **bậc 2** (chỉ bật cờ, đổi UX). Muốn tự chủ phi tập trung → **bậc 3** (nhiều việc: BFT ordering + threshold key + đổi genesis/verify). Muốn dựa hạ tầng có sẵn + composability → **bậc 4** (tích hợp mạng ngoài). Cả 3 giữ nguyên **execution sovereign**, nên không đụng tới `CosmosExecutor`/lớp thực thi — chỉ đổi **nguồn thứ tự**.

## 5. Vậy "không có validator" mất gì?

| Rủi ro | Ảnh hưởng thực tế | Giảm nhẹ trong ev-node |
|--------|-------------------|------------------------|
| **Liveness / SPOF** | Single sequencer chết → chain **ngừng ra block** (không sai state, chỉ treo) | **based sequencer**, hoặc **Raft HA multi-sequencer** ([§4b](#4b-multi-sequencer-ha-failover--chống-spof-liveness)) |
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
