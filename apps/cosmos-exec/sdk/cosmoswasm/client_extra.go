package cosmoswasm

// File này bổ sung các method bọc thêm endpoint dev-facing của cosmos-exec mà
// Client chưa phủ: /tx/simulate, /tx/estimate, /blocks/latest, /blocks/{height},
// /tx/pending. Tách riêng khỏi client.go để nhóm "đọc/ước lượng" gọn một chỗ.

import (
	"context"         // truyền timeout/cancel.
	"encoding/base64" // tx []byte -> base64 cho /tx/simulate.
	"errors"          // lỗi đơn giản.
	"net/http"        // method GET/POST.
	"strconv"         // height uint64 -> chuỗi cho path /blocks/{height}.
)

// Coin phản chiếu một sdk.Coin khi serialize JSON ({denom, amount}). Dùng để
// đọc trường "fee" trả về từ /tx/simulate mà không phải import cosmos-sdk.
type Coin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// SimulateResponse là kết quả của Client.SimulateTx (POST /tx/simulate).
//
// VI: gas_used là gas THẬT tx tiêu khi chạy thử (không commit); gas_limit đã
// nhân hệ số đệm (COSMOS_EXEC_GAS_ADJUSTMENT) ở phía server; fee là phí tương
// ứng gas_limit theo đúng chính sách ante enforce — gắn thẳng vào tx trước khi ký.
type SimulateResponse struct {
	GasUsed   uint64 `json:"gas_used"`
	GasWanted uint64 `json:"gas_wanted"`
	GasLimit  uint64 `json:"gas_limit"`
	Fee       []Coin `json:"fee"`
	FeeDenom  string `json:"fee_denom"`
	FeeAmount string `json:"fee_amount"`
}

// SimulateTx chạy thử tx qua ante + msg handler (KHÔNG commit) và trả gas thật
// + gas_limit (đã đệm) + fee gợi ý. Gọi trước khi ký để phí khớp tx thực, thay
// cho việc đặt một gas_limit cố định.
//
// VI: POST /tx/simulate. txBytes là TxRaw đã ký (hoặc ký giả 0-fee) — server chỉ
// cần để chạy mô phỏng, không phát sinh giao dịch.
func (c *Client) SimulateTx(ctx context.Context, txBytes []byte) (*SimulateResponse, error) {
	if len(txBytes) == 0 {
		return nil, errors.New("tx bytes cannot be empty")
	}
	res := SimulateResponse{}
	err := c.doJSON(
		ctx,
		http.MethodPost,
		txSimulatePath,
		submitTxRequest{TxBase64: base64.StdEncoding.EncodeToString(txBytes)},
		&res,
	)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// CostBreakdown là kết quả của Client.EstimateCost (POST /tx/estimate). Phơi cả
// hằng chính sách (da_price_per_byte, min_gas_price) để dashboard biết vì sao số
// thay đổi khi operator chỉnh giá.
type CostBreakdown struct {
	Bytes          uint64 `json:"bytes"`
	Gas            uint64 `json:"gas"`
	EstDAAmount    string `json:"est_da_amount"`
	EstDADenom     string `json:"est_da_denom"`
	EstGasAmount   string `json:"est_gas_amount"`
	EstGasDenom    string `json:"est_gas_denom"`
	DAPricePerByte string `json:"da_price_per_byte"`
	MinGasPrice    string `json:"min_gas_price"`
}

// EstimateRequest là input cho Client.EstimateCost. Cung cấp đúng MỘT trong:
//   - TxBase64 / TxHex (+ Gas): raw tx — server biết bytes, gas do caller cấp.
//   - Hash: tx đã chạy — server lấy bytes + gas THẬT từ kết quả.
//   - Bytes + Gas: truyền trực tiếp.
type EstimateRequest struct {
	TxBase64 string `json:"tx_base64,omitempty"`
	TxHex    string `json:"tx_hex,omitempty"`
	Hash     string `json:"hash,omitempty"`
	Bytes    uint64 `json:"bytes,omitempty"`
	Gas      uint64 `json:"gas,omitempty"`
}

// EstimateCost ước lượng tổng chi phí (DA + gas) cho một tx mà KHÔNG cần chạy nó
// (trừ dạng {hash}). Dùng cho dashboard/định giá; muốn gas chính xác để ký thì
// dùng SimulateTx.
//
// VI: POST /tx/estimate.
func (c *Client) EstimateCost(ctx context.Context, req EstimateRequest) (*CostBreakdown, error) {
	res := CostBreakdown{}
	if err := c.doJSON(ctx, http.MethodPost, txEstimatePath, req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// BlockInfo phản chiếu block view của executor (/blocks/*).
type BlockInfo struct {
	Height   uint64   `json:"height"`
	Time     string   `json:"time"`
	AppHash  string   `json:"app_hash"`
	NumTxs   int      `json:"num_txs"`
	TxHashes []string `json:"tx_hashes,omitempty"`
}

// GetLatestBlock trả block mới nhất. found=false khi chain chưa có block nào
// (server trả {"found": false}). Lỗi mạng/HTTP trả qua error.
//
// VI: GET /blocks/latest.
func (c *Client) GetLatestBlock(ctx context.Context) (*BlockInfo, bool, error) {
	// Server trả hoặc block JSON, hoặc {"found": false} — gộp cả hai vào 1 struct.
	var raw struct {
		BlockInfo
		Found *bool `json:"found"`
	}
	if err := c.doJSON(ctx, http.MethodGet, blocksLatestPath, nil, &raw); err != nil {
		return nil, false, err
	}
	if (raw.Found != nil && !*raw.Found) || raw.Height == 0 {
		return nil, false, nil
	}
	b := raw.BlockInfo
	return &b, true, nil
}

// GetBlockByHeight trả block tại một chiều cao. Height không tồn tại → server
// trả 404, ở đây thành error.
//
// VI: GET /blocks/{height}.
func (c *Client) GetBlockByHeight(ctx context.Context, height uint64) (*BlockInfo, error) {
	if height == 0 {
		return nil, errors.New("height must be > 0")
	}
	res := BlockInfo{}
	if err := c.doJSON(ctx, http.MethodGet, blocksPathPrefix+strconv.FormatUint(height, 10), nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetPendingTxCount trả số giao dịch đang chờ trong mempool của executor.
//
// VI: GET /tx/pending.
func (c *Client) GetPendingTxCount(ctx context.Context) (int, error) {
	var res struct {
		PendingCount int `json:"pending_count"`
	}
	if err := c.doJSON(ctx, http.MethodGet, txPendingPath, nil, &res); err != nil {
		return 0, err
	}
	return res.PendingCount, nil
}
