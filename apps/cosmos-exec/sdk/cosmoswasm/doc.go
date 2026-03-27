package cosmoswasm

import "net/http"

const (
	defaultExecAPIURL = "http://127.0.0.1:50051"
	txSubmitPath      = "/tx/submit"
	txResultPath      = "/tx/result"
	querySmartPath    = "/wasm/query-smart"
)

// Client wraps the public HTTP endpoints exposed by cosmos-exec-grpc:
// - POST /tx/submit
// - GET  /tx/result
// - POST /wasm/query-smart
type Client struct {
	baseURL    string
	httpClient *http.Client
}
