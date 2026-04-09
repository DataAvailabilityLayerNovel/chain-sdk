package cosmoswasm

import "net/http"

const (
	defaultExecAPIURL = "http://127.0.0.1:50051"
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
type Client struct {
	baseURL    string
	httpClient *http.Client
}
