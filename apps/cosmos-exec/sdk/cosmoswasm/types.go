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
	// Commitment is a hex-encoded SHA-256 of the stored data.
	// Store this on-chain (e.g. in a WASM contract) to keep gas costs minimal.
	// VI: hex SHA-256 của data đã lưu. Đây là "vé" để retrieve hoặc verify sau.
	Commitment string `json:"commitment"`
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
	// Commitment is the hex-encoded SHA-256 returned by SubmitBlob.
	// VI: commitment lấy từ SubmitBlob — phải đúng để contract verify được.
	Commitment string
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
	// Commitments are the per-blob SHA-256 hashes, in submission order.
	// VI: hash của từng blob, theo đúng thứ tự gửi (giữ index để verify cây).
	Commitments []string `json:"commitments"`
	// Count is len(Commitments).
	Count int `json:"count"`
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
	// Root is the Merkle root returned by SubmitBatch / CommitRoot.
	Root string
	// Count is the number of blobs in the batch.
	// VI: số blob trong batch — contract dùng để biết chiều cao/leaves của cây.
	Count int
	// Tag is an optional application-level label (e.g. "game-events").
	Tag string
	// Extra holds any additional fields to merge into the contract message.
	Extra map[string]any
}
