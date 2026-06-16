// package cosmoswasm: file định nghĩa các TYPE dùng chung của SDK —
// request/response gửi qua HTTP, struct event/result. Chỉ chứa khai báo,
// không có logic.
package cosmoswasm

// SubmitTxResponse: phản hồi của POST /tx/submit. Server chỉ trả hash tx vừa
// nhận vào mempool — chưa nói gì về thành công/thất bại lúc thực thi (cần
// poll thêm /tx/result để biết).
type SubmitTxResponse struct {
	Hash string `json:"hash"`
}

// GetTxResultResponse: phản hồi của GET /tx/result. Found=false nghĩa là tx
// chưa thấy (có thể chưa được include vào block, hoặc hash sai).
type GetTxResultResponse struct {
	Found  bool               `json:"found"`            // server đã thấy tx chưa.
	Result *TxExecutionResult `json:"result,omitempty"` // omitempty: bỏ field khi nil.
}

// TxEventAttribute: 1 cặp key-value trong event của tx (vd: "amount":"100ustake").
type TxEventAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// TxEvent: 1 event do msg phát ra (vd "transfer", "wasm-execute"). Mỗi event
// có Type (loại) + list Attributes (chi tiết).
type TxEvent struct {
	Type       string             `json:"type"`
	Attributes []TxEventAttribute `json:"attributes"`
}

// TxExecutionResult: kết quả CHI TIẾT của 1 tx sau khi đã thực thi.
type TxExecutionResult struct {
	Hash   string    `json:"hash"`             // hash tx (định danh).
	Height uint64    `json:"height"`           // block chứa tx.
	Code   uint32    `json:"code"`             // 0 = OK, khác 0 = lỗi (mã của Cosmos).
	Log    string    `json:"log"`              // mô tả lỗi (nếu có).
	Events []TxEvent `json:"events,omitempty"` // events phát ra; rỗng → bỏ field.
}

// NodeStatus: phản hồi của GET /status — mirror struct StatusInfo của executor
// (apps/cosmos-exec/executor/executor.go). Dùng để phân biệt mức finality:
//   - LatestHeight    : block cao nhất executor đã sinh (soft tip).
//   - FinalizedHeight : block cao nhất đã DA-finalized (set qua SetFinal).
// Một tx ở Height=h là soft khi h ≤ LatestHeight, DA-final khi h ≤ FinalizedHeight.
type NodeStatus struct {
	Initialized     bool   `json:"initialized"`
	ChainID         string `json:"chain_id"`
	LatestHeight    uint64 `json:"latest_height"`
	FinalizedHeight uint64 `json:"finalized_height"`
	Healthy         bool   `json:"healthy"`
	Synced          bool   `json:"synced"`
}

// FinalityLevel: mức chung kết của 1 tx, theo mô hình finality nhiều giai đoạn
// (xem docs thesis 07 §7.4). Tăng dần: Unknown < Soft < DA.
type FinalityLevel int

const (
	// FinalityUnknown: chưa thấy tx (chưa vào block nào, hoặc hash sai).
	FinalityUnknown FinalityLevel = iota
	// FinalitySoft: tx đã vào 1 block do sequencer commit + gossip P2P, nhưng
	// block đó CHƯA được DA xác nhận. Rủi ro: sequencer có thể equivocate.
	FinalitySoft
	// FinalityDA: block chứa tx đã DA-finalized — data vĩnh viễn khả dụng + có
	// thứ tự. Bất khả đảo ngược (giả định DA an toàn).
	FinalityDA
)

// String: tên đọc được của mức finality (dùng cho log/UI).
func (f FinalityLevel) String() string {
	switch f {
	case FinalitySoft:
		return "soft"
	case FinalityDA:
		return "da-finalized"
	default:
		return "unknown"
	}
}

// QuerySmartResponse: response của /wasm/query-smart. 1 trong 2 field có giá
// trị: Data (parsed JSON) hoặc DataRaw (chuỗi raw không phải JSON).
type QuerySmartResponse struct {
	Data    any    `json:"data,omitempty"`     // any: kiểu động (map/slice/số/chuỗi).
	DataRaw string `json:"data_raw,omitempty"` // chuỗi thô khi không parse được JSON.
}

// InstantiateTxRequest: input cho BuildInstantiateTx — đủ field để dựng
// MsgInstantiateContract.
type InstantiateTxRequest struct {
	Sender string // người gửi (rỗng → DefaultSender / signer.Address()).
	CodeID uint64 // mã code đã upload qua MsgStoreCode.
	Msg    any    // JSON init message (map/struct/[]byte).
	Label  string // label cho instance (rỗng → "wasm-via-sdk").
	Admin  string // admin có quyền migrate; rỗng → không có admin.
}

// ExecuteTxRequest: input cho BuildExecuteTx — gọi method của contract đã tồn tại.
type ExecuteTxRequest struct {
	Sender   string // người gửi.
	Contract string // địa chỉ bech32 của contract đích.
	Msg      any    // JSON execute message (map/struct).
}

// BlobSubmitResponse is returned by Client.SubmitBlob.
//
// VI: response của /blob/submit — upload "blob" lên DA layer riêng (không
// on-chain). Caller nhận về commitment 32 byte (hex) — chỉ ghi commitment
// này on-chain để tiết kiệm gas.
type BlobSubmitResponse struct {
	// Commitment is the hex-encoded Celestia blob commitment (NMT subtree root)
	// of the submitted data. Record this on-chain (e.g. in a WASM contract) to
	// keep gas costs minimal.
	// VI: commitment Celestia (NMT subtree root) dạng hex. Ghi on-chain để tiết
	// kiệm gas. Đây là "vé" để retrieve/verify sau — nhưng PHẢI kèm Height.
	Commitment string `json:"commitment"`
	// Height is the Celestia DA block height the blob landed in.
	// REQUIRED to retrieve the blob later: Blob.Get needs height + namespace +
	// commitment. Commitment alone is NOT enough.
	// VI: DA height nơi blob được đưa vào. BẮT BUỘC để lấy lại blob sau này —
	// commitment một mình KHÔNG đủ.
	Height uint64 `json:"height"`
	// Namespace is the hex-encoded DA namespace the blob was submitted to.
	// VI: namespace DA (hex) mà blob được gửi vào.
	Namespace string `json:"namespace"`
	// Size is the number of bytes stored.
	Size int `json:"size"`
}

// BlobRetrieveResponse is returned by Client.RetrieveBlob.
//
// VI: response của GET /blob/{commitment} — lấy lại data đã upload (base64).
type BlobRetrieveResponse struct {
	Commitment string `json:"commitment"`
	// DataBase64 is the stored data encoded as standard base64.
	// VI: data thật, base64 (vì JSON không chứa binary trực tiếp).
	DataBase64 string `json:"data_base64"`
	Size       int    `json:"size"`
}

// BlobCommitTxRequest is used with BuildBlobCommitTx to record a single blob
// commitment inside a CosmWasm contract.
//
// VI: input cho hàm build tx ghi commitment vào WASM contract.
type BlobCommitTxRequest struct {
	// Sender is the message sender (optional, uses DefaultSender if empty).
	Sender string
	// Contract is the bech32 address of the target WASM contract.
	Contract string
	// Commitment is the hex-encoded Celestia commitment returned by SubmitBlob.
	// VI: commitment lấy từ SubmitBlob — phải đúng để retrieve/verify được.
	Commitment string
	// Height is the Celestia DA height returned by SubmitBlob. Recorded on-chain
	// alongside the commitment so anyone reading the contract can retrieve the
	// blob via Blob.Get(height, namespace, commitment).
	// VI: DA height từ SubmitBlob — ghi kèm commitment để người đọc contract
	// retrieve lại blob được (Blob.Get cần cả height).
	Height uint64
	// Namespace is the hex DA namespace returned by SubmitBlob (optional but
	// recommended — needed for retrieval if the app uses multiple namespaces).
	// VI: namespace DA (hex) — nên ghi để retrieve khi app dùng nhiều namespace.
	Namespace string
	// Tag is an optional application-level label (e.g. "snapshot", "event-log").
	// VI: nhãn ứng dụng (vd "snapshot") — contract dùng để phân loại blob.
	Tag string
	// Extra holds any additional fields to merge into the contract message.
	// VI: field tuỳ ý gắn thêm vào msg gửi contract (vd timestamp, version).
	Extra map[string]any
}

// BlobBatchResponse is returned by Client.SubmitBatch and POST /blob/batch.
//
// VI: response của /blob/batch — upload nhiều blob cùng lúc. Server build
// cây Merkle, trả 1 root đại diện. CHỈ root này cần ghi on-chain.
type BlobBatchResponse struct {
	// Root is the Merkle root of the batch (hex SHA-256 tree of commitments).
	// Commit this on-chain — it is the only on-chain cost for N blobs.
	// VI: root cây Merkle — đại diện cho N blob, ghi 1 lần = chi phí on-chain
	// duy nhất.
	Root string `json:"root"`
	// Commitments are the per-blob Celestia commitments, in submission order.
	// VI: commitment của từng blob, theo đúng thứ tự gửi (giữ index để verify cây).
	Commitments []string `json:"commitments"`
	// Count is len(Commitments).
	Count int `json:"count"`
	// Height is the Celestia DA height the whole batch landed in (one Submit
	// call → one height for all blobs). REQUIRED to retrieve any blob in the batch.
	// VI: DA height của cả batch (1 lần Submit → 1 height cho mọi blob). BẮT BUỘC
	// để retrieve bất kỳ blob nào trong batch.
	Height uint64 `json:"height"`
}

// BatchRootTxRequest is used with BuildBatchRootTx to record a Merkle batch
// root in a CosmWasm contract.
//
// VI: input cho hàm build tx ghi MerkleRoot (đại diện 1 batch) vào contract.
type BatchRootTxRequest struct {
	// Sender is optional; uses DefaultSender when empty.
	Sender string
	// Contract is the bech32 address of the target WASM contract.
	Contract string
	// Root is the Merkle root returned by SubmitBatch.
	Root string
	// Height is the Celestia DA height of the batch (from SubmitBatch). Recorded
	// on-chain so blobs in the batch can be retrieved later.
	// VI: DA height của batch (từ SubmitBatch) — ghi on-chain để retrieve blob sau.
	Height uint64
	// Namespace is the hex DA namespace of the batch (optional but recommended).
	// VI: namespace DA (hex) của batch — nên ghi để retrieve.
	Namespace string
	// Count is the number of blobs in the batch.
	// VI: số blob trong batch — contract dùng để biết chiều cao/leaves của cây.
	Count int
	// Tag is an optional application-level label (e.g. "game-events").
	Tag string
	// Extra holds any additional fields to merge into the contract message.
	Extra map[string]any
}
