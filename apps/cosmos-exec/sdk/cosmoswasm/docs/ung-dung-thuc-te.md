# Ứng dụng thực tế của đồ án

Tài liệu này trả lời câu hỏi **"dựng bộ công cụ này ra để làm gì trong đời thực"** — nhìn ở góc *ai dùng, dùng vào việc gì, và vì sao đồ án khiến việc đó trở nên khả thi*. Gồm: các ứng dụng chạy được ngay trên code (mục 2), năng lực blob-first cho dev chủ động chọn nơi cất data lớn (mục 3), hướng phát triển tương lai (mục 4) và lý do chọn hệ Cosmos (mục 5).

> Bối cảnh nhanh: đồ án là **bộ công cụ dựng sovereign rollup chạy hợp đồng CosmWasm** trên ev-node + Celestia. Nó gói toàn bộ độ phức tạp thành: một **máy chủ `cosmos-exec`** (25 endpoint HTTP), một **SDK Go**, và một **web app** kèm ví Keplr. Nhờ vậy một nhóm nhỏ có thể có **chuỗi riêng của ứng dụng mình** trong vài lệnh, thay vì tự ghép cả một hệ blockchain.

---

## 1. Vì sao "chuỗi riêng cho từng ứng dụng" lại là nhu cầu thật

Hôm nay muốn ghi dữ liệu bất biến (giao dịch, sự kiện, chứng nhận…) lên blockchain, người ta có ba lựa chọn, đều vướng:

- **Thuê chỗ trên chuỗi công cộng (Ethereum, BNB…):** phí biến động, tắc nghẽn khi mạng đông, và ứng dụng của bạn phải "chen chỗ" với hàng nghìn app khác. Một app ghi nhiều dữ liệu là phí đội lên ngay.
- **Tự dựng blockchain nguyên khối:** mỗi nút phải tự làm cả ba việc — chạy giao dịch, đồng thuận, lưu trữ vĩnh viễn — nên vận hành nặng, cần đội ngũ chuyên gia, và vẫn phình trạng thái khi dữ liệu lớn.
- **Dùng cơ sở dữ liệu thường:** rẻ và nhanh, nhưng **không có tính bất biến và không ai kiểm chứng được** — đúng thứ khiến blockchain có giá trị.

Đồ án nhắm vào **khoảng trống ở giữa**: cho một nhóm nhỏ (studio game, doanh nghiệp SME, cộng đồng, nhóm sinh viên) **chuỗi độc lập của riêng ứng dụng** — phí thấp, không tranh chỗ với ai, tự chủ luật lệ (*sovereign*) — mà **không cần là chuyên gia blockchain**. Đó là "app-chain hoá" đang là xu hướng của ngành.

Ba đặc điểm của sản phẩm quyết định các ứng dụng bên dưới trở nên khả thi:

| Đặc điểm sản phẩm | Có sẵn trong code | Mở ra ứng dụng gì |
|---|---|---|
| **Chuỗi riêng, phí do mình định** (`app/ante.go` — min gas price cấu hình được, có thể ~0) | ✅ | App ghi nhiều dữ liệu mà không sợ phí thị trường |
| **Hợp đồng CosmWasm** (viết bằng Rust, chạy sandbox) | ✅ | Logic nghiệp vụ tuỳ biến: điểm thưởng, vật phẩm, quy trình duyệt… |
| **HTTP/JSON API 25 endpoint** (`cmd/cosmos-exec-grpc`) | ✅ | Mọi ngôn ngữ / web / backend cắm vào, **không bắt buộc viết Go** |
| **Web app + ví Keplr + explorer realtime** | ✅ | Người dùng cuối thao tác không cần biết CLI |
| **Auto-create account** (ví mới ký được ngay) | ✅ | Onboarding không ma sát — người dùng web tạo ví là xài liền |
| **Forced inclusion** (gửi thẳng tx lên Celestia) | ✅ | Chống kiểm duyệt — hợp use case cần "không ai chặn được giao dịch" |
| **Lưu dữ liệu lớn ngoài chuỗi, chỉ neo cam kết** | ✅ | App dữ liệu nặng (media, log, telemetry) vẫn rẻ |

---

## 2. Các ứng dụng thực tế xây được **ngay trên code hiện tại**

Những use case dưới đây chỉ cần *viết một hợp đồng CosmWasm + gọi API sẵn có* — không phải sửa lõi. Cái đầu tiên đã có ví dụ chạy được trong repo.

### 2.1. Game có tài sản & lịch sử trận đấu on-chain (đã có demo)

**Bài toán:** một studio game nhỏ muốn vật phẩm, điểm số, kết quả trận đấu là **của người chơi** và **không thể sửa** — nhưng không đủ sức trả phí chuỗi công cộng cho mỗi hành động trong game.

**Làm được gì với đồ án:** chuỗi riêng phí ~0 cho game; hợp đồng CosmWasm quản economy (đúc/chuyển vật phẩm, bảng xếp hạng); dữ liệu nặng như *replay cả trận* đẩy ra ngoài chuỗi, chỉ ghi "vân tay" lên chuỗi để ai cũng kiểm chứng replay là thật.

**Bằng chứng trong repo:** ví dụ [`examples/game-telemetry`](../examples/game-telemetry/main.go) đã chạy trọn vòng: `register → create match → start → record → finish`, ghi telemetry/replay và đọc lại có xác minh.

### 2.2. Truy xuất nguồn gốc & chuỗi cung ứng

**Bài toán:** một hợp tác xã nông sản / hãng dược muốn chứng minh lô hàng đi qua đâu, ai xác nhận, không bị làm giả nhãn.

**Làm được gì:** mỗi lô hàng là một "vòng đời" trong hợp đồng CosmWasm; mỗi mốc (thu hoạch → đóng gói → kiểm định → phân phối) là một giao dịch có chữ ký của bên chịu trách nhiệm. Ảnh chứng từ / phiếu kiểm định nặng thì để ngoài chuỗi, neo cam kết lên chuỗi. Người tiêu dùng quét QR → web app đọc trực tiếp lịch sử bất biến.

**Vì sao hợp:** chuỗi riêng → mỗi hợp tác xã một chuỗi, không lẫn dữ liệu; phí thấp nên ghi được *nhiều mốc* cho *nhiều lô*.

### 2.3. Điểm thưởng / thẻ thành viên cho doanh nghiệp SME

**Bài toán:** chuỗi cà phê, phòng gym muốn hệ thống tích điểm **minh bạch, liên thông nhiều chi nhánh**, khách không sợ "bị xoá điểm".

**Làm được gì:** một hợp đồng token CosmWasm làm điểm thưởng; auto-create account để khách mới **quét là có ví ngay**, không bắt cài app phức tạp; explorer để khách tự tra lịch sử tích/tiêu điểm.

### 2.4. Cấp & xác thực chứng chỉ (giáo dục, nghề)

**Bài toán:** trường/trung tâm cấp bằng, chứng chỉ; nhà tuyển dụng cần **xác minh thật/giả trong vài giây** mà không gọi điện về trường.

**Làm được gì:** hợp đồng "registry" lưu hash của mỗi văn bằng + thông tin người được cấp; nhà tuyển dụng nhập mã → web app kiểm chứng khớp on-chain. Bản scan gốc để ngoài chuỗi, neo cam kết → riêng tư mà vẫn xác minh được.

### 2.5. Sổ ghi sự kiện / nhật ký kiểm toán bất biến (audit log)

**Bài toán:** một hệ thống IoT, một sàn nội bộ, một quy trình phê duyệt cần **nhật ký không ai sửa được về sau** để phục vụ thanh tra/compliance.

**Làm được gì:** mỗi sự kiện ghi thành giao dịch; khi cần "không ai được chặn dòng ghi" (ví dụ tố cáo, log an ninh) thì dùng **forced inclusion** — gửi thẳng lên Celestia, sequencer buộc phải đưa vào block. Đây là điểm mà DB thường không làm nổi.

### 2.6. Bỏ phiếu / quản trị cộng đồng, DAO nhỏ

**Bài toán:** một cộng đồng, câu lạc bộ, quỹ muốn bỏ phiếu **công khai, kiểm đếm được, chống gian lận**.

**Làm được gì:** hợp đồng governance CosmWasm (đề xuất, bỏ phiếu theo trọng số token); forced inclusion đảm bảo phiếu không bị sequencer kiểm duyệt; web app cho thành viên bỏ phiếu bằng Keplr.

> **Điểm chung của cả 6 use case:** tất cả đều là *"ghi dữ liệu có logic nghiệp vụ + cần bất biến + cần ai cũng kiểm chứng"* — và đều **chạy được trên sản phẩm hiện tại chỉ bằng cách viết hợp đồng CosmWasm rồi gọi HTTP API**. Đây chính là giá trị "một nhóm nhỏ tự dựng chuỗi riêng" mà đồ án hướng tới.

---

## 3. Blob-first: cho lập trình viên **chủ động chọn nơi cất data lớn**

Hầu hết các use case ở mục 2 đều có một phần dữ liệu **nặng** đi kèm: replay trận đấu, ảnh chứng từ, bản scan văn bằng, log thiết bị… Câu hỏi thực tế của dev không phải "ghi lên blockchain hay không", mà là **"cất phần nặng đó ở đâu"**. Đồ án không ép một cách duy nhất — nó đưa ra **ba lựa chọn** và để dev tự quyết theo bài toán:

| Cách cất data lớn | Ai làm | Ưu | Nhược |
|---|---|---|---|
| **① Nhét vào state hợp đồng** (IAVL) | Dev ghi thẳng vào CosmWasm state | Query on-chain tức thì, không phải fetch đi đâu | **Mọi nút giữ vĩnh viễn** → phình state, phí cao, càng ngày càng nặng |
| **② Để rollup tự đẩy qua DA** | Tự động — data giao dịch của block do ev-node submit lên Celestia (namespace `rollup`) | Không phải làm gì thêm | Là data *của cả block*, dev **không kiểm soát riêng** phần nặng; vẫn nằm trong tx nên vẫn tốn gas theo byte |
| **③ Blob-first: dev chủ động up thẳng lên Celestia** (SDK `BlobClient`) | Dev gọi `SubmitBlob`/`SubmitBatch` — **một lời gọi JSON-RPC thẳng tới Celestia bridge, NGOÀI luồng tx** | Data nặng **không vào tx, không vào state**; on-chain chỉ neo *cam kết* nhẹ | Data không query trực tiếp trên chuỗi — **khi cần phải kéo về** (đánh đổi có chủ đích) |

**Blob-first chính là cách ③** — và đây là đóng góp của SDK: biến "đẩy data ra DA" thành một lựa chọn *sạch, rẻ, kiểm soát được* cho dev, thay vì buộc nhét mọi thứ vào state.

### So sánh thẳng ② (rollup tự đẩy DA) vs ③ (blob-first của đồ án)

> **Nói thật trước để không bị hớ:** xét *riêng giá lưu mỗi byte trên Celestia*, ② và ③ **gần như bằng nhau** — Celestia rẻ ở cả hai đường (giá đo thực ≈ `0.1707 utia/byte`, xem [`cost.go`](../cmd/cosmos-exec-grpc/cost.go)). Nên đồ án **không** thắng ở "giá lưu 1 byte". Nó thắng ở **bốn chỗ khác** mà cách ② không giải quyết được. Đó mới là điểm cần nhấn.

Cùng một ví dụ: ghi **một replay trận đấu 300 KB** lên chuỗi.

| Tiêu chí | ② Nhét data vào tx, để rollup đẩy DA | ③ Blob-first (đồ án) |
|---|---|---|
| **Nén trước khi lên DA** | Không — block data đi **byte thô** | **Có** (`CompressIfBeneficial`): telemetry/JSON/log nén ~3–4× → **ít byte DA hơn hẳn** |
| **Byte lên DA (300 KB)** | ~307 KB thô → DA bill ≈ **0,052 TIA** | sau nén ~≈ **0,013 TIA** (khớp số đo luận án); JPEG/đã nén thì bằng ②, *không bao giờ tệ hơn* |
| **Cái gì nằm trong tx người dùng ký** | **Cả 300 KB** nằm trong tx | Chỉ **cam kết ~40 byte** |
| **Gas ví người dùng phải trả** | **Lớn** — ký & xử lý tx nặng 300 KB | **Tí xíu** — chỉ một tx commit nhỏ |
| **Phình state (nếu ghi vào state)** | **+300 KB / mỗi nút, vĩnh viễn** | ~40–80 byte |
| **Data lớn cỡ vài MB** | Vướng **trần kích thước tx/block** → có thể **bất khả thi** | `ChunkBlob` + `SubmitBatch` → **lớn tuỳ ý**, 1 Merkle root cho cả lô |
| **Lấy lại & xác minh độc lập** | Lẫn trong block-data namespace, **không thiết kế cho app đọc lẻ** | Namespace **riêng của app** → `RetrieveBlob` + đối chiếu hash, đọc/verify độc lập |

**Đọc bảng theo một câu:** ② không "đắt tiền DA", nhưng nó **đẩy chi phí sang những chỗ tệ hơn** — bắt *ví người dùng* trả gas cho 300 KB, bắt *mọi nút* giữ 300 KB mãi mãi (nếu chạm state), gửi **byte thô không nén**, và **đụng trần kích thước** khi data lớn. Blob-first **cô lập** cục dữ liệu nặng: nén lại, cắt mảnh, để ở namespace riêng, và **chỉ để một dấu 40 byte chạm vào chuỗi**. Với data không nén được thì ③ vẫn hoà ② về byte DA, nhưng **vẫn thắng ở 4 dòng còn lại** (gas ví, phình state, giới hạn kích thước, đọc/verify độc lập).

> Chốt để nhấn mạnh đóng góp: cái đồ án làm **không phải** "tìm chỗ lưu rẻ hơn Celestia" — mà là **một pipeline (nén → chunk → batch → neo cam kết) biến việc đẩy data ra DA thành lựa chọn có kiểm soát**, gỡ được đúng bốn nút thắt mà cách "để rollup tự đẩy" bỏ ngỏ.

### Vì sao cách ③ rẻ hơn "bình thường": nén + chunk trước khi lên DA

Phí DA của Celestia tính theo **số byte** (`DA_cost = bytes × giá/byte`). SDK xử lý data qua một pipeline ngắn trước khi submit để **ít byte hơn mức thô**:

- **Nén (gzip) — [`compress.go`](../compress.go):** `CompressIfBeneficial` nén data và **chỉ giữ bản nén khi nó thật sự nhỏ hơn** (data ngẫu nhiên/ảnh JPEG/đã nén thì giữ nguyên — *không bao giờ làm phình*). Ít byte → phí DA rẻ hơn + submit nhanh hơn. Chiều đọc có cặp `MaybeDecompress` tự nhận diện gzip qua magic byte, không cần lưu cờ.
- **Chunk — [`chunk.go`](../chunk.go):** data lớn hơn một blob (`MaxBlobSize` = 2 MiB) được `ChunkBlob` cắt thành nhiều mảnh (mặc định ≤ 512 KiB) rồi gửi cả lô bằng **`SubmitBatch`** (tổng ≤ 8 MiB) — chỉ tốn **một DA height + một Merkle root** cho cả lô thay vì nhiều lần submit lẻ.

Kết quả: cùng một khối dữ liệu, đường blob-first tốn **ít byte DA hơn** so với để nguyên trong tx (cách ②) và **rẻ hơn nhiều lần** so với nhét vào state vĩnh viễn (cách ①).

### Đã lên DA rồi thì lấy lại thế nào — và vì sao đây là *lựa chọn có đánh đổi*

Điểm cần nói thẳng (và cũng là lý do vì sao đây là quyền chọn của dev, không phải mặc định): **DA không phải nơi để query trực tiếp**. Data nằm trên Celestia, muốn dùng thì **phải kéo về node/app** bằng `RetrieveBlob(height, commitment)` — cần *cả* DA height (đã neo on-chain) *lẫn* commitment, rồi `MaybeDecompress` + ghép chunk và **đối chiếu `OriginalHash`** để chắc chắn toàn vẹn.

Nghĩa là dev cân nhắc:

- Data **hay đọc, cần nhanh, nhỏ** → để **state** (cách ①).
- Data **nặng, ít đọc, chỉ cần chứng minh "có tồn tại & không bị sửa"** (replay, ảnh chứng từ, log) → **blob-first** (cách ③): rẻ, không phình chuỗi, chấp nhận "khi cần mới fetch về".

Gói gọn một dòng gọi: **[`StoreBlobAndRecord`](../blob_record.go)** làm trọn *upload lên Celestia → để dev tự dựng execute message ghi cam kết → ký & chờ vào block*; nếu bước on-chain lỗi vẫn trả về blob đã lên DA để **retry mà không phải upload lại**.

> **Ý nghĩa cho các ứng dụng mục 2:** chính cách ③ khiến game ghi được *cả trận replay*, chuỗi cung ứng đính được *ảnh kiểm định*, hệ chứng chỉ neo được *bản scan* — những thứ mà nếu nhét vào state sẽ khiến chuỗi phình và đắt đến mức không dùng được. Blob-first là thứ biến các use case "dữ liệu nặng" từ *bất khả thi về chi phí* thành *khả thi*.

---

## 4. Tiềm năng phát triển — **Future work** biến đồ án thành sản phẩm thật

Đây là hướng mở rộng có cơ sở kỹ thuật (nhiều thứ đã có mầm trong repo), cho thấy đồ án không dừng ở minh hoạ.

### 4.1. "Rollup-as-a-Service" — dựng chuỗi bằng vài cú click

Hiện đã có script một-lệnh dựng node ([`scripts/run-cosmos-wasm-nodes.go`](../../../../../scripts/run-cosmos-wasm-nodes.go)) và cấu hình theo profile dev/test/prod. Bước tiếp: một **bảng điều khiển web** để người không rành kỹ thuật chọn tên chuỗi, denom, phí, rồi bấm "Deploy" → hệ thống tự dựng chuỗi + cấp URL API + web app. Biến toàn bộ đồ án thành **nền tảng cho thuê chuỗi** (giống Vercel nhưng cho app-chain).

### 4.2. Chợ mẫu hợp đồng (contract template marketplace)

Đóng gói sẵn các hợp đồng cho 6 use case ở mục 2 (điểm thưởng, truy xuất nguồn gốc, chứng chỉ, governance…) thành **template bấm-là-chạy**. Người dùng chỉ điền tham số, không cần viết Rust. Đây là bước đưa sản phẩm tới đối tượng *hoàn toàn không phải lập trình viên blockchain*.

### 4.3. Liên thông hệ Cosmos qua IBC

Repo đã có tài liệu nền [`ibc-integration.md`](ibc-integration.md). Phát triển tiếp cho phép chuỗi của app **chuyển token/dữ liệu qua lại với Osmosis, dYdX, các Cosmos chain khác** — ví dụ điểm thưởng của SME đổi được ra tài sản có thanh khoản, hay chứng chỉ dùng lại được ở chuỗi khác. Đây là lợi thế "sinh ra đã liên thông" của hệ Cosmos mà đồ án thừa hưởng.

### 4.4. Cầu sang hệ EVM

ev-node đã có sẵn một **executor cho EVM** song song với executor CosmWasm của đồ án (xem [`cosmos-vs-evnode.md`](cosmos-vs-evnode.md) mục so sánh). Tiềm năng: cùng một khung, cho developer **chọn viết hợp đồng bằng Solidity hoặc CosmWasm**, mở rộng tệp người dùng sang cộng đồng Ethereum.

### 4.5. Dịch vụ lưu trữ / chứng thực dữ liệu có xác minh

Từ khả năng "để dữ liệu lớn ngoài chuỗi, neo cam kết trên chuỗi", có thể xây một **dịch vụ notarization**: khách nộp tài liệu/ảnh/video → nhận về một chứng nhận thời điểm tồn tại (proof-of-existence) bất biến, dùng cho bản quyền, hồ sơ pháp lý, xác thực nội dung chống deepfake. Đây là hướng gần với các dự án "kho lưu trữ có chủ quyền" trên thị trường.

### 4.6. SDK đa ngôn ngữ

Hiện có SDK Go + web (TypeScript) qua HTTP. Vì API là HTTP/JSON thuần, chỉ cần **sinh thêm SDK cho Python / JS / Rust** là mở rộng được cho backend, mobile, IoT — không đụng tới lõi.

---

## 5. Vì sao chọn hệ Cosmos (CosmWasm + Cosmos SDK) — và vì sao **không** chọn cái khác

Đây không phải lựa chọn mặc định hay theo thói quen. Bài toán của đồ án ràng buộc rất rõ: **một nhóm nhỏ, không phải chuyên gia blockchain, cần một tầng thực thi vừa an toàn vừa dễ nhúng vào ev-node**. Khi đặt các phương án lên bàn cân với đúng ràng buộc đó, hệ Cosmos là lựa chọn có lý nhất — dưới đây là lập luận theo kiểu *loại trừ*.

> Chốt trước cho khỏi hiểu nhầm: đồ án chọn **CosmWasm (máy chạy hợp đồng)** + **BaseApp của Cosmos SDK (khung ứng dụng)**, nhưng **cố tình bỏ CometBFT** và cắm thẳng vào ev-node. Nên "chọn Cosmos" ở đây là chọn *tầng hợp đồng + tầng ứng dụng*, còn tầng đồng thuận đồ án tự chọn cái nhẹ hơn. Chính khả năng **tháo rời được** này cũng là một điểm cộng của Cosmos — sẽ nói ở lý do 5.

### Đã cân nhắc gì và vì sao loại

| Phương án | Vì sao hấp dẫn | Vì sao **không** chọn cho đồ án này |
|---|---|---|
| **Tự viết máy ảo hợp đồng** | Toàn quyền kiểm soát | Vô lý về công sức: một máy ảo an toàn, tất định, có gas-metering là cả một luận án riêng. Nhóm nhỏ không thể và không nên |
| **EVM + Solidity** | Hệ sinh thái lớn nhất | ev-node muốn nhúng EVM phải đi qua **Engine API + geth** (hai tiến trình, JWT, giao thức nặng). Solidity cũng nhiều bẫy an toàn cho người mới. Nặng hơn nhu cầu |
| **Máy WASM tự chọn (không Cosmos)** | Nhẹ, WASM hiện đại | Có runtime nhưng **thiếu tầng ứng dụng**: vẫn phải tự viết ví, số dư, chữ ký, quản state có phiên bản, chuẩn địa chỉ… — làm lại đúng thứ Cosmos SDK đã cho sẵn |
| **CosmWasm trên Cosmos SDK (đã chọn)** | WASM an toàn **+** tầng ứng dụng có sẵn **+** tháo rời được khỏi consensus | Khớp trọn ba ràng buộc của đồ án |

Nói cách khác: các phương án khác hoặc **bắt tự xây lại quá nhiều** (viết VM, viết ví/số dư), hoặc **kéo theo quá nhiều thứ thừa** (cả một geth), trong khi CosmWasm + Cosmos SDK cho *vừa đủ và đúng chỗ*.

### Năm lý do khiến Cosmos thắng — mỗi lý do gắn với một cái lợi cụ thể

1. **Không phải phát minh lại tầng ứng dụng.** BaseApp của Cosmos SDK đã đóng gói sẵn ví, số dư (`bank`), tài khoản/chữ ký (`auth`), và module `wasm`. Đồ án chỉ việc *nhúng và cấu hình lại* thay vì viết từ số không — đây là thứ giúp một nhóm nhỏ ra được sản phẩm chạy đầu-cuối. Bằng chứng: cả tầng thực thi chỉ gói trong hai gói `app` + `executor`, phần còn lại là dùng lại.

2. **CosmWasm an toàn theo thiết kế cho người viết hợp đồng.** Hợp đồng chạy trong **sandbox WASM, tất định, gas-metered**: hợp đồng lỗi hay vòng lặp vô hạn thì bị chặn bởi gas, **không kéo sập cả chuỗi**, và mọi nút cho ra cùng kết quả. Với các use case ở mục 2 (điểm thưởng, truy xuất nguồn gốc, bỏ phiếu) — nơi lỗi hợp đồng là mất tiền/mất niềm tin — đây là bảo hiểm quan trọng. Viết bằng Rust cũng chặn cả một lớp lỗi bộ nhớ mà Solidity hay dính.

3. **State có phiên bản (IAVL) → khôi phục sau sự cố gần như miễn phí.** Cây trạng thái IAVL lưu lịch sử theo từng version, nên khi chuỗi cần **rollback** (ví dụ tầng thực thi chạy vượt tầng đồng thuận sau khi crash), đồ án chỉ gọi `LoadVersion(height)` là quay về đúng mốc — không phải dựng lại cơ chế lùi trạng thái thủ công. Đây là thứ một app-chain sản xuất bắt buộc phải có, và Cosmos cho sẵn.

4. **Tương thích ngay với ví Keplr và hệ liên thông IBC.** Vì theo chuẩn Cosmos, đồ án chỉ cần **giả lập vài endpoint LCD** là ví **Keplr** (hàng triệu người đang dùng) đọc được số dư và ký giao dịch — người dùng cuối không phải cài ví lạ. Xa hơn, chuỗi *sinh ra đã nói được IBC*, mở đường liên thông với Osmosis, dYdX… (future work 4.3). Một chuỗi tự chế sẽ bị cô lập hoàn toàn khỏi những mạng lưới này.

5. **Kiến trúc tách bạch (ABCI) cho phép đồ án "chỉ lấy phần cần".** Chính vì Cosmos tách sạch **ứng dụng** khỏi **đồng thuận** qua ABCI, đồ án mới **rút được CometBFT ra và cắm thẳng BaseApp vào ev-node**. Kết quả: binary nhẹ hơn, đường đi giao dịch ngắn hơn, và đồ án kiểm soát trọn tầng thực thi để nhét các tính năng riêng (forced inclusion, auto-account) vào đúng chỗ. Một nền tảng đóng kín, không tháo rời được, sẽ không cho làm điều này.

### Chốt lại

> Chọn Cosmos **không phải** vì nó là blockchain "xịn nhất", mà vì nó là bộ khung **đúng độ trừu tượng** cho bài toán: cho sẵn những viên gạch khó và nguy hiểm nhất (máy chạy hợp đồng an toàn, ví, số dư, state có phiên bản, chuẩn liên thông) để nhóm nhỏ khỏi tự xây, đồng thời **đủ mô-đun để tháo rời** — giữ CosmWasm + BaseApp, bỏ CometBFT, thay bằng ev-node nhẹ hơn. Không phương án nào khác cho cả hai điều đó cùng lúc: EVM thì nặng và đóng hơn, WASM trần thì thiếu tầng ứng dụng, tự xây thì bất khả thi với một đồ án.

---

## Tham chiếu chéo

- Ví dụ chạy thật: [`examples/game-telemetry`](../examples/game-telemetry/main.go), [`examples/forced-inclusion`](../examples/forced-inclusion/main.go), [`examples/my-counter`](../examples/my-counter/main.go)
- Use case chi tiết theo thao tác người dùng: [thesis/thesis-usecases.md](thesis/thesis-usecases.md), [frontend-usecases.md](frontend-usecases.md)
- So sánh phần đồ án tự xây vs ev-node cung cấp: [cosmos-vs-evnode.md](cosmos-vs-evnode.md)
- Liên thông IBC: [ibc-integration.md](ibc-integration.md)
- Chống kiểm duyệt (forced inclusion): [sequencer-security.md](sequencer-security.md)
</content>
</invoke>
