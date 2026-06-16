package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/executor"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

// faucetConfig is the treasury + faucet setup (approaches A and B from
// docs/fee-economics.md). It is enabled only when a treasury key is provided;
// otherwise the chain keeps its zero-balance, faucet-less default behavior.
//
// VI: cấu hình kho bạc (treasury) + faucet. CHỈ bật khi có khoá treasury
// (COSMOS_EXEC_TREASURY_PRIVKEY_HEX); không có → chain giữ mặc định không
// faucet, mọi account số dư 0. Cách A: nạp tiền cho treasury lúc genesis.
// Cách B: mỗi request /faucet treasury gửi một ít token cho người xin.
type faucetConfig struct {
	privHex    string    // treasury secp256k1 key (hex), holds the supply
	treasury   string    // bech32 address derived from privHex
	genesisAmt sdk.Coins // funded to treasury at genesis (A)
	payout     sdk.Coins // sent per /faucet request (B)
	gas        uint64    // gas limit for the faucet's MsgSend tx
	cooldown   time.Duration

	// mu bảo vệ mọi field dưới đây: nhiều request /faucet có thể chạy song song,
	// phải tuần tự hoá việc cấp sequence để không hai tx dùng trùng sequence.
	mu       sync.Mutex
	lastSeen map[string]time.Time // recipient → last funded time (chống spam theo cooldown)
	haveSeq  bool
	nextSeq  uint64 // expected treasury sequence for the next tx
}

// loadFaucetConfig reads faucet env vars. Returns (nil, nil) when
// COSMOS_EXEC_TREASURY_PRIVKEY_HEX is unset — feature disabled, no error.
//
// VI: đọc cấu hình faucet từ ENV. Trả (nil, nil) khi chưa set khoá treasury —
// nghĩa là TẮT faucet (không phải lỗi). Caller dùng nil này để quyết định có
// đăng ký route /faucet hay không.
func loadFaucetConfig() (*faucetConfig, error) {
	priv := strings.TrimSpace(os.Getenv("COSMOS_EXEC_TREASURY_PRIVKEY_HEX"))
	if priv == "" {
		return nil, nil
	}

	// Suy ra địa chỉ bech32 của treasury từ khoá riêng (để tra account/sequence).
	treasury, err := cosmoswasm.TreasuryAddressFromHex(priv)
	if err != nil {
		return nil, fmt.Errorf("COSMOS_EXEC_TREASURY_PRIVKEY_HEX: %w", err)
	}

	// ParseCoinsNormalized: parse chuỗi kiểu "1000000ustake" thành sdk.Coins.

	genesisAmt, err := sdk.ParseCoinsNormalized(envOr("COSMOS_EXEC_TREASURY_AMOUNT", "1000000000000ustake"))
	if err != nil {
		return nil, fmt.Errorf("COSMOS_EXEC_TREASURY_AMOUNT: %w", err)
	}
	payout, err := sdk.ParseCoinsNormalized(envOr("COSMOS_EXEC_FAUCET_AMOUNT", "1000000ustake"))
	if err != nil {
		return nil, fmt.Errorf("COSMOS_EXEC_FAUCET_AMOUNT: %w", err)
	}
	if payout.IsZero() {
		return nil, fmt.Errorf("COSMOS_EXEC_FAUCET_AMOUNT must be > 0")
	}

	// Gas limit cho tx MsgSend của faucet (mặc định 200k), override qua ENV.
	gas := uint64(200000)
	if v := strings.TrimSpace(os.Getenv("COSMOS_EXEC_FAUCET_GAS")); v != "" {
		g, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("COSMOS_EXEC_FAUCET_GAS: %w", err)
		}
		gas = g
	}

	// Cooldown: mỗi địa chỉ chỉ được xin lại sau khoảng này (mặc định 1 giờ).
	cooldown := time.Hour
	if v := strings.TrimSpace(os.Getenv("COSMOS_EXEC_FAUCET_COOLDOWN_SECONDS")); v != "" {
		s, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("COSMOS_EXEC_FAUCET_COOLDOWN_SECONDS: %w", err)
		}
		cooldown = time.Duration(s) * time.Second
	}

	return &faucetConfig{
		privHex:    priv,
		treasury:   treasury,
		genesisAmt: genesisAmt,
		payout:     payout,
		gas:        gas,
		cooldown:   cooldown,
		lastSeen:   make(map[string]time.Time),
	}, nil
}

// genesisOption returns the executor option that funds the treasury at chain
// init (approach A). Without this the treasury has zero balance and every
// faucet tx fails "insufficient funds" once fees are enforced.
//
// VI: trả về option nạp số dư cho treasury ngay tại genesis (cách A). Thiếu
// bước này thì treasury số dư 0, mọi tx faucet sẽ rớt "insufficient funds"
// khi đã bật thu phí.
func (fc *faucetConfig) genesisOption(application *app.App) (executor.Option, error) {
	genesis, err := application.GenesisWithBalances([]banktypes.Balance{
		{Address: fc.treasury, Coins: fc.genesisAmt},
	})
	if err != nil {
		return nil, fmt.Errorf("build genesis with treasury balance: %w", err)
	}
	return executor.WithGenesis(genesis), nil
}

// feeForGas mirrors app.feePolicy: ceil(gas * COSMOS_EXEC_MIN_GAS_PRICE) in
// COSMOS_EXEC_GAS_DENOM (default ustake). Returns nil (0-fee) when the price
// is unset/invalid/non-positive, matching the ante's "policy off" behavior so
// the faucet keeps working in the permissionless dev default.
//
// VI: tính phí ĐÚNG y công thức feePolicy trong app/ante.go — phải khớp, nếu
// không tx faucet sẽ bị chính ante từ chối (ErrInsufficientFee). Trả nil
// (phí 0) khi giá chưa set/sai/không dương → trùng hành vi "tắt phí" của ante.
func feeForGas(gas uint64) sdk.Coins {
	v := strings.TrimSpace(os.Getenv("COSMOS_EXEC_MIN_GAS_PRICE"))
	if v == "" {
		return nil
	}
	price, err := math.LegacyNewDecFromStr(v)
	if err != nil || !price.IsPositive() {
		return nil
	}
	denom := strings.TrimSpace(os.Getenv("COSMOS_EXEC_GAS_DENOM"))
	if denom == "" {
		denom = "ustake"
	}
	amt := price.MulInt(math.NewIntFromUint64(gas)).Ceil().RoundInt()
	if !amt.IsPositive() {
		return nil
	}
	return sdk.NewCoins(sdk.NewCoin(denom, amt))
}

type faucetResponse struct {
	TxHash    string `json:"tx_hash"`
	Recipient string `json:"recipient"`
	Amount    string `json:"amount"`
	Treasury  string `json:"treasury"`
}

// faucetHandler funds a recipient address from the treasury key by signing a
// bank MsgSend and pushing it through the normal tx path. Treasury sequence is
// serialized under fc.mu so concurrent requests don't reuse a sequence.
//
// VI: handler /faucet — nạp token cho địa chỉ người xin. Tự ký một MsgSend
// bằng khoá treasury rồi đẩy vào mempool qua InjectTx (đi đúng đường tx bình
// thường). Toàn bộ thân hàm chạy dưới fc.mu để tuần tự hoá sequence treasury.
func faucetHandler(exec *executor.CosmosExecutor, fc *faucetConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		// Lấy địa chỉ nhận từ query ?addr=, kiểm tra hợp lệ bech32.
		addr := strings.TrimSpace(r.URL.Query().Get("addr"))
		if addr == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "addr query param is required"})
			return
		}
		if _, err := sdk.AccAddressFromBech32(addr); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bech32 addr: " + err.Error()})
			return
		}
		// Từ chối tự nạp cho chính treasury (vô nghĩa + lệch kế toán sequence).
		if addr == fc.treasury {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refusing to fund the treasury itself"})
			return
		}

		// Khoá: từ đây tới hết hàm là vùng tới hạn (xử lý sequence + cooldown).
		fc.mu.Lock()
		defer fc.mu.Unlock()

		// Chống spam: nếu địa chỉ vừa được nạp trong khoảng cooldown → 429.
		if last, ok := fc.lastSeen[addr]; ok && time.Since(last) < fc.cooldown {
			retry := fc.cooldown - time.Since(last)
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": fmt.Sprintf("address funded recently, retry in %s", retry.Round(time.Second)),
			})
			return
		}

		// Tra account treasury trên state để lấy account_number + sequence.
		info, err := exec.GetAccountInfo(r.Context(), fc.treasury)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "treasury account lookup: " + err.Error()})
			return
		}

		// Use the higher of on-chain sequence and our locally tracked next
		// sequence: mempool txs aren't reflected in state until block-executed,
		// so back-to-back requests must not reuse the same sequence.
		//
		// VI: lấy MAX(sequence on-chain, nextSeq tự đếm). Lý do: tx trong mempool
		// chưa vào state cho tới khi block được thực thi, nên hai request liên
		// tiếp mà chỉ đọc sequence on-chain sẽ trùng → một tx bị loại.
		seq := info.Sequence
		if fc.haveSeq && fc.nextSeq > seq {
			seq = fc.nextSeq
		}

		// Lấy chain_id (cần để ký đúng signDoc).
		status, err := exec.GetStatus(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "status: " + err.Error()})
			return
		}

		// Fee must satisfy the ante feePolicy (when COSMOS_EXEC_MIN_GAS_PRICE
		// is set) — the treasury's own tx is enforced just like any other.
		_, txBytes, err := cosmoswasm.BuildSignedBankSendWithFee(
			fc.privHex, status.ChainID, addr, fc.payout, feeForGas(fc.gas), info.AccountNumber, seq, fc.gas,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "sign: " + err.Error()})
			return
		}

		// Đẩy tx faucet vào mempool (cùng đường với tx của user).
		hash, err := exec.InjectTx(r.Context(), txBytes)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "submit: " + err.Error()})
			return
		}

		// Cập nhật bộ đếm: sequence kế tiếp + mốc thời gian (cho cooldown).
		fc.nextSeq = seq + 1
		fc.haveSeq = true
		fc.lastSeen[addr] = time.Now()

		writeJSON(w, http.StatusOK, faucetResponse{
			TxHash:    hash,
			Recipient: addr,
			Amount:    fc.payout.String(),
			Treasury:  fc.treasury,
		})
	}
}
