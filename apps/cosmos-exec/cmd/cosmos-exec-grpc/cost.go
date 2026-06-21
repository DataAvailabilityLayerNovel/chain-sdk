package main

import (
	"math/big"
	"os"
	"strings"
	"sync"
)

// CostBreakdown is the simulated cost of a tx — what the user *would* pay if
// fees were enforced. The chain currently accepts 0-fee txs (see
// NewPermissionlessAnteHandler); this is policy math overlaid on the real
// gas/bytes measurements, so test wallets don't get drained but operators
// can see what production economics would look like.
//
// Amounts are decimal strings to avoid float precision issues over JSON.
// EstDAAmount is fractional units of EstDADenom (e.g. "TIA"); EstGasAmount
// is fractional units of EstGasDenom (e.g. "ustake").
//
// VI: bảng chi phí MÔ PHỎNG của một tx — số tiền user *sẽ* trả NẾU bật thu phí.
// Mặc định dev chain nhận tx phí 0; đây chỉ là phép tính chính sách áp lên số
// đo thật (gas/bytes) để operator thấy "kinh tế production" sẽ ra sao mà không
// rút cạn ví test. Số tiền để dạng chuỗi thập phân để tránh sai số float qua JSON.
type CostBreakdown struct {
	Bytes        uint64 `json:"bytes"`
	Gas          uint64 `json:"gas"`
	EstDAAmount  string `json:"est_da_amount"`
	EstDADenom   string `json:"est_da_denom"`
	EstGasAmount string `json:"est_gas_amount"`
	EstGasDenom  string `json:"est_gas_denom"`
	// Policy constants used for this estimate — exposed so dashboards can
	// show why numbers changed when operators retune.
	DAPricePerByte string `json:"da_price_per_byte"`
	MinGasPrice    string `json:"min_gas_price"`
}

// costPolicy bundles the pricing constants used to convert bytes/gas into
// fractional fee amounts. Loaded once from env at startup.
//
// VI: gói các hằng giá để quy đổi bytes→phí DA và gas→phí thực thi. Nạp một
// lần từ ENV lúc khởi động (xem getCostPolicy + sync.Once).
type costPolicy struct {
	tiaPerByte  *big.Float
	minGasPrice *big.Float
	daDenom     string
	gasDenom    string
	// Pretty-print of the input strings, kept verbatim so the API exposes
	// what the operator configured rather than our re-formatting.
	tiaPerByteStr  string
	minGasPriceStr string
}

var (
	costPolicyOnce sync.Once
	costPolicyVal  costPolicy
)

// getCostPolicy returns the singleton policy loaded from env vars. The default
// TIA-per-byte is the EFFECTIVE rate measured on-chain (total PFB fee.amount /
// total blob bytes over 69 PayForBlobs submissions): 29481 utia / 172663 B =
// 0.1707 utia/byte = 1.71e-7 TIA/byte. NOTE this folds in the large per-PFB
// fixed cost (~301 utia) amortised across bytes; the marginal cost of an extra
// byte is only ~0.05 utia/byte (5.0e-8 TIA/byte). See scripts/measure_da_fees.mjs.
//
// VI: TIA/byte mặc định là rate THỰC ĐO trên chuỗi (tổng fee PFB / tổng byte,
// N=69): 0,1707 utia/byte = 1,71e-7 TIA/byte. Đây là rate "hiệu dụng" đã gộp chi
// phí cố định ~301 utia/PFB; chi phí biên mỗi byte tăng thêm chỉ ~0,05 utia/byte.
// Operator chỉnh qua ENV.
func getCostPolicy() costPolicy {
	costPolicyOnce.Do(func() {
		tiaStr := envOr("COSMOS_EXEC_TIA_PER_BYTE", "0.000000171")
		gasStr := envOr("COSMOS_EXEC_MIN_GAS_PRICE", "0.000001")
		costPolicyVal = costPolicy{
			tiaPerByte:     mustBigFloat(tiaStr),
			minGasPrice:    mustBigFloat(gasStr),
			daDenom:        envOr("COSMOS_EXEC_DA_DENOM", "TIA"),
			gasDenom:       envOr("COSMOS_EXEC_GAS_DENOM", "ustake"),
			tiaPerByteStr:  tiaStr,
			minGasPriceStr: gasStr,
		}
	})
	return costPolicyVal
}

// estimate converts (bytes, gas) into a populated CostBreakdown using p's
// pricing constants. Both products use big.Float math so very small per-byte
// prices don't lose precision against millions of bytes.
//
// VI: quy đổi (bytes, gas) thành CostBreakdown. Dùng big.Float (số thực độ
// chính xác cao) để giá/byte rất nhỏ nhân với hàng triệu byte không mất số lẻ.
func (p costPolicy) estimate(bytes, gas uint64) CostBreakdown {
	// daAmt = bytes × tiaPerByte (hoá đơn DA); gasAmt = gas × minGasPrice (phí thực thi).
	daAmt := new(big.Float).Mul(new(big.Float).SetUint64(bytes), p.tiaPerByte)
	gasAmt := new(big.Float).Mul(new(big.Float).SetUint64(gas), p.minGasPrice)
	return CostBreakdown{
		Bytes:          bytes,
		Gas:            gas,
		EstDAAmount:    trimZeros(daAmt.Text('f', 12)),
		EstDADenom:     p.daDenom,
		EstGasAmount:   trimZeros(gasAmt.Text('f', 6)),
		EstGasDenom:    p.gasDenom,
		DAPricePerByte: p.tiaPerByteStr,
		MinGasPrice:    p.minGasPriceStr,
	}
}

// envOr đọc ENV key; rỗng/không có thì trả giá trị mặc định def.
func envOr(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

// mustBigFloat parse chuỗi số thành big.Float (80 bit mantissa). Sai cú pháp →
// trả 0 thay vì panic: operator cấu hình sai giá thì số 0 trên API đủ "ồn" để
// nhận ra mà không làm sập server.
func mustBigFloat(s string) *big.Float {
	f, _, err := big.ParseFloat(s, 10, 80, big.ToNearestEven)
	if err != nil {
		// Fall back to zero — operator misconfigured a price; surfacing
		// zero in the API is loud enough to notice without crashing.
		return new(big.Float)
	}
	return f
}

// trimZeros strips trailing zeros (and a trailing dot) from a fixed-point
// decimal so "0.000020000000" → "0.00002". Kept simple — assumes input has
// at most one '.' which big.Float.Text('f', _) guarantees.
//
// VI: cắt số 0 thừa (và dấu chấm cuối) của số thập phân cố định, "0.0000200" →
// "0.00002". Giả định input có tối đa một dấu '.', đúng với big.Float.Text('f').
func trimZeros(s string) string {
	if !strings.ContainsRune(s, '.') {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
