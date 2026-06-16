// package cosmoswasm: SDK Go phía CLIENT để dApp giao tiếp với chain
// cosmos-exec. File này định nghĩa *Client — bọc HTTP client, lo việc
// gửi tx, query, lấy account, đợi tx được đóng block...
package cosmoswasm

import (
	"bytes"          // bytes.NewReader: biến []byte thành io.Reader cho request body.
	"context"        // truyền tín hiệu huỷ/timeout.
	"encoding/base64"// mã hoá tx []byte -> base64 (định dạng JSON-safe).
	"encoding/json"  // mã hoá/giải mã JSON request/response.
	"errors"         // tạo lỗi đơn giản.
	"fmt"            // định dạng & bọc lỗi.
	"io"             // io.Reader / ReadAll cho body HTTP.
	"net/http"       // client HTTP chuẩn của Go.
	"net/url"        // url.Values: build query string an toàn.
	"strings"        // xử lý chuỗi (TrimSpace, Contains...).
	"time"           // timeout HTTP, polling interval.

	// txcodec (internal): tiện ích chuẩn hoá JSON message (msg query/execute).
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/txcodec"
)

// submitTxRequest: thân request gửi tới /tx/submit. omitempty: bỏ field nếu rỗng.
type submitTxRequest struct {
	TxBase64 string `json:"tx_base64,omitempty"` // tx mã hoá base64 (cách 1).
	TxHex    string `json:"tx_hex,omitempty"`    // tx mã hoá hex (cách 2).
}

// querySmartRequest: thân request gửi tới /wasm/query-smart.
type querySmartRequest struct {
	Contract string          `json:"contract"` // địa chỉ bech32 của hợp đồng.
	Msg      json.RawMessage `json:"msg"`      // RawMessage: giữ JSON thô (không re-encode).
}

// apiError: định dạng error trả về từ server (JSON {"error": "..."}).
type apiError struct {
	Error string `json:"error"`
}

// NewClient: tạo Client trỏ tới baseURL. Trim dấu / cuối + khoảng trắng;
// nếu rỗng thì dùng URL mặc định DefaultExecAPIURL.
func NewClient(baseURL string) *Client {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		trimmed = DefaultExecAPIURL
	}

	return &Client{
		baseURL: trimmed,
		// http.Client mặc định: timeout 20s cho mọi request (chống treo).
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

// WithHTTPClient: trả về Client mới với http.Client tuỳ chỉnh.
// VI: receiver `c *Client` nhưng *KHÔNG sửa c* — clone ra rồi sửa bản clone
// (immutable builder). Nhờ vậy có thể dùng song song mà không race.
func (c *Client) WithHTTPClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		return c // nil → trả nguyên client cũ (no-op).
	}

	cloned := *c // dereference + gán = SAO chép struct (shallow copy).
	cloned.httpClient = httpClient
	return &cloned
}

// SubmitTxBase64: gửi tx (đã base64) tới /tx/submit. Trả response chứa hash
// hoặc lỗi nếu chain từ chối.
func (c *Client) SubmitTxBase64(ctx context.Context, txBase64 string) (*SubmitTxResponse, error) {
	txBase64 = strings.TrimSpace(txBase64)
	if txBase64 == "" {
		return nil, errors.New("tx base64 is required")
	}

	res := SubmitTxResponse{} // struct rỗng để doJSON ghi response vào.
	// doJSON: marshal request body → POST → decode response vào &res.
	err := c.doJSON(
		ctx,
		http.MethodPost,
		txSubmitPath,
		submitTxRequest{TxBase64: txBase64},
		&res, // truyền CON TRỎ để doJSON ghi vào.
	)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(res.Hash) == "" {
		// Chain trả 200 nhưng thiếu hash → coi như lỗi (response không hợp lệ).
		return nil, errors.New("submit response missing tx hash")
	}

	return &res, nil
}

// SubmitTxBytes: tiện ích — nhận []byte, tự base64 rồi gọi SubmitTxBase64.
func (c *Client) SubmitTxBytes(ctx context.Context, txBytes []byte) (*SubmitTxResponse, error) {
	if len(txBytes) == 0 {
		return nil, errors.New("tx bytes cannot be empty")
	}

	return c.SubmitTxBase64(ctx, base64.StdEncoding.EncodeToString(txBytes))
}

// AccountInfo mirrors the executor's auth account view.
//
// VI: struct tương ứng response của /auth/account/{addr} — đủ field để
// build SignDoc.
type AccountInfo struct {
	Address       string `json:"address"`        // địa chỉ bech32.
	AccountNumber uint64 `json:"account_number"` // do chain cấp khi tạo account.
	Sequence      uint64 `json:"sequence"`       // tăng dần mỗi tx (chống replay).
	Exists        bool   `json:"exists"`         // false → chain "đoán trước" số sẽ cấp.
}

// FetchAccount queries the chain's auth state for the given bech32 address.
// Used by signed tx builders to obtain the account_number + sequence required
// to construct a SignDoc.
//
// VI: gọi GET /auth/account/{addr} để lấy thông tin tài khoản. Builder tx
// dùng kết quả này để điền AccountNumber + Sequence vào SignDoc trước khi ký.
func (c *Client) FetchAccount(ctx context.Context, bech32Address string) (*AccountInfo, error) {
	bech32Address = strings.TrimSpace(bech32Address)
	if bech32Address == "" {
		return nil, errors.New("address is required")
	}
	res := AccountInfo{}
	// GET không có body → tham số reqBody = nil. Path ghép thẳng địa chỉ.
	if err := c.doJSON(ctx, http.MethodGet, "/auth/account/"+bech32Address, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetTxResult: tra kết quả 1 tx theo hash (GET /tx/result?hash=...).
func (c *Client) GetTxResult(ctx context.Context, txHash string) (*GetTxResultResponse, error) {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return nil, errors.New("tx hash is required")
	}

	// url.Values: map[string][]string với method Encode() xử lý url-escape an toàn.
	query := url.Values{}
	query.Set("hash", txHash)

	res := GetTxResultResponse{}
	// Ghép path + "?" + chuỗi query đã encode. Vd: /tx/result?hash=abc.
	err := c.doJSON(ctx, http.MethodGet, txResultPath+"?"+query.Encode(), nil, &res)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

// WaitTxResult: POLL liên tục /tx/result cho tới khi tx được thực thi xong
// (Found=true) hoặc ctx bị huỷ. Tham số pollInterval: khoảng cách giữa 2 lần
// tra (mặc định 1s nếu <= 0).
func (c *Client) WaitTxResult(ctx context.Context, txHash string, pollInterval time.Duration) (*TxExecutionResult, error) {
	if pollInterval <= 0 {
		pollInterval = 1 * time.Second
	}

	// Ticker: phát tín hiệu định kỳ qua channel ticker.C.
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop() // dọn ticker khi hàm kết thúc (tránh leak goroutine).

	// for { ... } : vòng lặp vô hạn — chỉ thoát qua return hoặc ctx huỷ.
	for {
		res, err := c.GetTxResult(ctx, txHash)
		if err != nil {
			return nil, err
		}

		if res.Found {
			if res.Result == nil {
				return nil, errors.New("tx is found but result payload is empty")
			}
			return res.Result, nil
		}

		// select: chờ HOẶC ctx huỷ HOẶC tick tiếp theo (lấy cái xảy ra trước).
		select {
		case <-ctx.Done(): // ctx bị huỷ → trả lỗi của ctx (Canceled/DeadlineExceeded).
			return nil, ctx.Err()
		case <-ticker.C: // tới tick → tiếp tục vòng lặp.
		}
	}
}

// Status: GET /status — trả NodeStatus của node (latest_height +
// finalized_height...). Đây là building block để phân biệt soft vs DA-final:
// caller (hoặc GetTxFinality/WaitTxFinality) so block height của tx với 2 mốc này.
func (c *Client) Status(ctx context.Context) (*NodeStatus, error) {
	res := NodeStatus{}
	if err := c.doJSON(ctx, http.MethodGet, statusPath, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetTxFinality: trả mức finality hiện tại của 1 tx kèm kết quả thực thi.
//   - FinalityUnknown : chưa thấy tx (Found=false) → result = nil.
//   - FinalitySoft    : tx đã vào block nhưng block chưa DA-final.
//   - FinalityDA      : block chứa tx đã DA-finalized.
// Tốn 2 request: /tx/result rồi /status. Finality là snapshot tại thời điểm gọi
// — muốn theo dõi chuyển soft→DA thì gọi lại hoặc dùng WaitTxFinality.
func (c *Client) GetTxFinality(ctx context.Context, txHash string) (FinalityLevel, *TxExecutionResult, error) {
	res, err := c.GetTxResult(ctx, txHash)
	if err != nil {
		return FinalityUnknown, nil, err
	}
	if !res.Found || res.Result == nil {
		return FinalityUnknown, nil, nil
	}

	status, err := c.Status(ctx)
	if err != nil {
		return FinalityUnknown, res.Result, err
	}

	if res.Result.Height <= status.FinalizedHeight {
		return FinalityDA, res.Result, nil
	}
	return FinalitySoft, res.Result, nil
}

// WaitTxFinality: POLL cho tới khi tx đạt mức finality >= want (hoặc ctx huỷ).
// Vd: WaitTxFinality(ctx, hash, FinalityDA, time.Second) chặn tới khi block chứa
// tx được DA xác nhận — dùng cho luồng giá trị cao (rút tiền, bridge). want =
// FinalitySoft tương đương WaitTxResult nhưng trả thêm thông tin mức finality.
func (c *Client) WaitTxFinality(ctx context.Context, txHash string, want FinalityLevel, pollInterval time.Duration) (*TxExecutionResult, error) {
	if want <= FinalityUnknown {
		want = FinalitySoft // chờ "thấy tx" là tối thiểu có ý nghĩa.
	}
	if pollInterval <= 0 {
		pollInterval = 1 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		level, result, err := c.GetTxFinality(ctx, txHash)
		if err != nil {
			return nil, err
		}
		if level >= want {
			return result, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// QuerySmartRaw: gọi /wasm/query-smart và trả response thô (data + data_raw).
// msg `any`: chấp nhận map, struct hay []byte JSON đã marshal — NormalizeJSONMsg
// chuẩn hoá về json.RawMessage.
func (c *Client) QuerySmartRaw(ctx context.Context, contract string, msg any) (*QuerySmartResponse, error) {
	contract = strings.TrimSpace(contract)
	if contract == "" {
		return nil, errors.New("contract is required")
	}

	msgJSON, err := txcodec.NormalizeJSONMsg(msg) // -> json.RawMessage hợp lệ.
	if err != nil {
		return nil, fmt.Errorf("invalid query msg: %w", err)
	}

	res := QuerySmartResponse{}
	err = c.doJSON(
		ctx,
		http.MethodPost,
		querySmartPath,
		querySmartRequest{Contract: contract, Msg: msgJSON},
		&res,
	)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

// QuerySmart: phiên bản "tiện hơn" — trả map[string]any thay vì struct thô.
// Có 3 trường hợp xử lý: server trả Data (đã parse JSON), DataRaw (chuỗi),
// hoặc cả hai đều rỗng.
func (c *Client) QuerySmart(ctx context.Context, contract string, msg any) (map[string]any, error) {
	res, err := c.QuerySmartRaw(ctx, contract, msg)
	if err != nil {
		return nil, err
	}

	if res.Data != nil {
		// Type assertion: Data là any → kiểm tra có phải map không.
		// `obj, ok := x.(T)`: ok=false nếu sai kiểu (không panic).
		obj, ok := res.Data.(map[string]any)
		if ok {
			return obj, nil
		}

		// Không phải object JSON (vd: là số/chuỗi) → bọc vào map với key "value".
		return map[string]any{"value": res.Data}, nil
	}

	if strings.TrimSpace(res.DataRaw) == "" {
		return map[string]any{}, nil // rỗng cả hai → trả map rỗng.
	}

	// Thử parse DataRaw thành map.
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(res.DataRaw), &decoded); err != nil {
		// Không phải JSON map → trả luôn chuỗi raw dưới key "raw".
		return map[string]any{"raw": res.DataRaw}, nil
	}

	return decoded, nil
}

// doJSON: hàm phụ — wrap mọi gọi HTTP có RETRY khi lỗi tạm thời.
// VI: marshal body (nếu có) → thử attempts lần. Mỗi lỗi không phải transient
// thì return ngay; transient (connection refused / deadline) thì delay rồi retry.
func (c *Client) doJSON(ctx context.Context, method, path string, reqBody any, out any) error {
	var payload []byte // bytes của body JSON; nil = không có body.
	if reqBody != nil {
		var err error
		payload, err = json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
	}

	var lastErr error
	attempts := 1 + c.retryMax // ít nhất 1 lần thử + số retry cấu hình.
	if attempts < 1 {
		attempts = 1 // an toàn: tránh attempts = 0.
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 { // attempt > 0 = đang retry, cần chờ.
			select {
			case <-ctx.Done():
				return ctx.Err() // ctx huỷ trong lúc chờ retry → thoát.
			case <-time.After(c.retryDelay):
				// time.After: trả channel "kêu" sau retryDelay.
			}
		}

		// Mỗi attempt cần Reader mới (Reader đã đọc xong không "tua lại" được).
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}

		lastErr = c.doJSONOnce(ctx, method, path, body, out)
		if lastErr == nil {
			return nil // thành công → thoát luôn.
		}

		// Only retry on transient errors (connection refused, timeout).
		// VI: lỗi business (4xx/5xx có message) → KHÔNG retry, trả ngay.
		msg := lastErr.Error()
		if !strings.Contains(msg, "connection refused") && !strings.Contains(msg, "deadline exceeded") {
			return lastErr
		}
	}

	return lastErr // hết attempts vẫn lỗi → trả lỗi cuối.
}

// doJSONOnce: thực hiện 1 lần request HTTP (không retry). Lỗi mã >=400
// được biến thành error có nội dung message từ server.
func (c *Client) doJSONOnce(ctx context.Context, method, path string, body io.Reader, out any) error {
	// NewRequestWithContext: gắn ctx → request tự huỷ khi ctx Done.
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		// Bearer token: chuẩn auth của API (tương đương với HTTP middleware).
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close() // PHẢI đóng Body để connection được tái sử dụng.

	respPayload, err := io.ReadAll(response.Body) // đọc toàn bộ body.
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if response.StatusCode >= http.StatusBadRequest { // 400+ = lỗi.
		apiErr := apiError{}
		// Thử parse body thành {error: "..."} chuẩn của server.
		if err := json.Unmarshal(respPayload, &apiErr); err == nil && strings.TrimSpace(apiErr.Error) != "" {
			return fmt.Errorf("api error (%d): %s", response.StatusCode, apiErr.Error)
		}
		// Không phải JSON chuẩn → trả nguyên body làm message.
		return fmt.Errorf("api error (%d): %s", response.StatusCode, strings.TrimSpace(string(respPayload)))
	}

	if out == nil {
		return nil // caller không cần decode body.
	}

	if err := json.Unmarshal(respPayload, out); err != nil {
		return fmt.Errorf("decode response json: %w", err)
	}

	return nil
}
