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

// getCostPolicy returns the singleton policy loaded from env vars. Defaults
// are sized for a Celestia-as-DA + Cosmos-SDK stake-token setup, but they're
// intentionally low so that test runs surface a non-zero number without
// distorting whether the tx is "expensive". Operators tune via env.
func getCostPolicy() costPolicy {
	costPolicyOnce.Do(func() {
		tiaStr := envOr("COSMOS_EXEC_TIA_PER_BYTE", "0.0000001")
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
func (p costPolicy) estimate(bytes, gas uint64) CostBreakdown {
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

func envOr(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

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
