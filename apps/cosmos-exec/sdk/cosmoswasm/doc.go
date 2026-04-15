package cosmoswasm

import (
	"net/http"
	"time"
)

const (
	// DefaultExecAPIURL is the fallback URL used by NewClient when no URL is provided.
	// For production use, prefer NewClientFromConfig with an explicit ExecURL.
	DefaultExecAPIURL = "http://127.0.0.1:50051"
	txSubmitPath      = "/tx/submit"
	txResultPath      = "/tx/result"
	querySmartPath    = "/wasm/query-smart"
	blobSubmitPath    = "/blob/submit"
	blobRetrievePath  = "/blob/retrieve"
	blobBatchPath     = "/blob/batch"
)

// Client wraps the public HTTP endpoints exposed by cosmos-exec-grpc:
//   - POST /tx/submit
//   - GET  /tx/result
//   - POST /wasm/query-smart
//   - POST /blob/submit       (blob-first data storage)
//   - GET  /blob/retrieve     (fetch blob by commitment)
//
// Create via NewClient(url) for quick use, or NewClientFromConfig(SDKConfig{})
// for full control over auth, retry, timeouts.
type Client struct {
	baseURL    string
	httpClient *http.Client
	authToken  string
	retryMax   int
	retryDelay time.Duration
	chainID    string
}
