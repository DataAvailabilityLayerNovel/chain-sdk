package main

import (
	"encoding/json"
	"net/http"
)

func swaggerUIHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(swaggerUIHTML)) //nolint:errcheck
	}
}

func swaggerJSONHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(swaggerSpec()) //nolint:errcheck
	}
}

func swaggerSpec() map[string]any {
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Cosmos-Exec gRPC API",
			"description": "HTTP API for the Cosmos WASM executor — transactions, smart contract queries, blob storage, and cost estimation.",
			"version":     "1.0.0",
		},
		"servers": []map[string]any{
			{"url": "http://127.0.0.1:50051", "description": "Local executor"},
		},
		"tags": []map[string]any{
			{"name": "node", "description": "Node status and block info"},
			{"name": "tx", "description": "Transaction submission and result polling"},
			{"name": "wasm", "description": "CosmWasm smart contract queries"},
			{"name": "blob", "description": "Blob-first data storage (off-chain data, on-chain commitment)"},
		},
		"paths": map[string]any{
			// ── NODE ────────────────────────────────────────────────
			"/status": map[string]any{
				"get": map[string]any{
					"tags":        []string{"node"},
					"summary":     "Get executor status",
					"description": "Returns whether the node is initialized, healthy, synced, and current block heights.",
					"responses": map[string]any{
						"200": resp("Node status", ref("StatusResponse")),
					},
				},
			},
			"/blocks/latest": map[string]any{
				"get": map[string]any{
					"tags":        []string{"node"},
					"summary":     "Get latest block",
					"description": "Returns the most recently executed block info (height, time, app_hash, num_txs).",
					"responses": map[string]any{
						"200": resp("Latest block info", ref("BlockInfo")),
					},
				},
			},
			"/blocks/{height}": map[string]any{
				"get": map[string]any{
					"tags":    []string{"node"},
					"summary": "Get block by height",
					"parameters": []map[string]any{
						pathParam("height", "integer", "Block height"),
					},
					"responses": map[string]any{
						"200": resp("Block info", ref("BlockInfo")),
						"400": resp("Invalid height", ref("ErrorResponse")),
						"404": resp("Block not found", ref("ErrorResponse")),
					},
				},
			},

			// ── TX ──────────────────────────────────────────────────
			"/tx/pending": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tx"},
					"summary":     "Get pending transaction count",
					"description": "Returns the number of transactions currently waiting in the mempool.",
					"responses": map[string]any{
						"200": resp("Pending tx count", ref("TxPendingResponse")),
					},
				},
			},
			"/tx/{hash}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"tx"},
					"summary":     "Get transaction by hash",
					"description": "Returns full transaction info with status (pending/success/failed), height, code, log, and events.",
					"parameters": []map[string]any{
						pathParam("hash", "string", "Transaction hash (hex, no 0x prefix)"),
					},
					"responses": map[string]any{
						"200": resp("Transaction info", ref("TxDetailResponse")),
						"400": resp("Invalid hash", ref("ErrorResponse")),
					},
				},
			},
			"/tx/submit": map[string]any{
				"post": map[string]any{
					"tags":        []string{"tx"},
					"summary":     "Submit a signed transaction",
					"description": "Injects a Cosmos SDK transaction into the executor mempool. The tx is included in the next block.",
					"requestBody": reqBody("application/json", ref("SubmitTxRequest")),
					"responses": map[string]any{
						"200": resp("Transaction accepted", ref("SubmitTxResponse")),
						"400": resp("Invalid request", ref("ErrorResponse")),
					},
				},
			},
			"/tx/result": map[string]any{
				"get": map[string]any{
					"tags":    []string{"tx"},
					"summary": "Get transaction result by hash",
					"parameters": []map[string]any{
						queryParam("hash", "string", true, "Transaction hash (hex, no 0x prefix)"),
					},
					"responses": map[string]any{
						"200": resp("Transaction result (found or not found)", ref("TxResultResponse")),
						"400": resp("Missing hash parameter", ref("ErrorResponse")),
					},
				},
			},
			"/tx/estimate": map[string]any{
				"post": map[string]any{
					"tags":        []string{"tx"},
					"summary":     "Simulate per-tx cost (TIA + gas)",
					"description": "Returns what the tx *would* cost given current pricing policy, without charging anything. Accepts one of {tx_base64|tx_hex} + gas hint, {hash} of an executed tx, or raw {bytes, gas}.",
					"requestBody": reqBody("application/json", ref("EstimateTxRequest")),
					"responses": map[string]any{
						"200": resp("Cost breakdown", ref("TxCostBreakdown")),
						"400": resp("Invalid request", ref("ErrorResponse")),
						"404": resp("Hash not found", ref("ErrorResponse")),
					},
				},
			},

			// ── WASM ────────────────────────────────────────────────
			"/wasm/query-smart": map[string]any{
				"post": map[string]any{
					"tags":        []string{"wasm"},
					"summary":     "Query a CosmWasm smart contract",
					"description": "Executes a read-only smart query against the contract's current state.",
					"requestBody": reqBody("application/json", ref("QuerySmartRequest")),
					"responses": map[string]any{
						"200": resp("Query result", ref("QuerySmartResponse")),
						"400": resp("Invalid request or contract error", ref("ErrorResponse")),
					},
				},
			},

			// NOTE: blob-first data (large off-chain data on Celestia DA) is NOT
			// served by cosmos-exec-grpc. The SDK's BlobClient talks to a Celestia
			// bridge directly. Only the on-chain commitment is recorded here, via a
			// normal /tx/submit (CosmWasm execute with a "record_blob" message).
		},
		"components": map[string]any{
			"schemas": schemas(),
		},
	}
}

func schemas() map[string]any {
	return map[string]any{
		"ErrorResponse": obj(
			prop("error", "string", "Error message"),
		),

		// ── NODE ────────────────────────────────────────────────
		"StatusResponse": obj(
			prop("initialized", "boolean", "Whether the executor has been initialized"),
			prop("chain_id", "string", "Chain ID"),
			prop("latest_height", "integer", "Latest executed block height"),
			prop("finalized_height", "integer", "Latest finalized block height"),
			prop("healthy", "boolean", "Whether the node is healthy"),
			prop("synced", "boolean", "Whether finalized height has caught up to latest height"),
		),
		"BlockInfo": obj(
			prop("height", "integer", "Block height"),
			prop("time", "string", "Block timestamp (RFC3339)"),
			prop("app_hash", "string", "Application state hash after this block (hex)"),
			prop("num_txs", "integer", "Number of transactions in this block"),
		),
		"TxPendingResponse": obj(
			prop("pending_count", "integer", "Number of transactions in the mempool"),
		),
		"TxDetailResponse": map[string]any{
			"type": "object",
			"properties": mergeProps(
				prop("hash", "string", "Transaction hash"),
				prop("status", "string", "Transaction status: pending, success, or failed"),
				prop("found", "boolean", "Whether the tx result was found"),
				prop("height", "integer", "Block height (present when found)"),
				prop("code", "integer", "Result code, 0 = success (present when found)"),
				prop("log", "string", "Execution log (present when found)"),
				prop("gas_used", "integer", "Gas consumed by the tx"),
				prop("gas_wanted", "integer", "Gas requested by the tx"),
				prop("bytes", "integer", "On-wire size of the encoded tx"),
				map[string]any{"events": map[string]any{
					"type": "array", "description": "Execution events (present when found)",
					"items": map[string]any{"type": "object"},
				}},
				map[string]any{"cost": map[string]any{
					"$ref":        "#/components/schemas/TxCostBreakdown",
					"description": "Simulated cost using current pricing policy",
				}},
			),
		},

		// ── TX ──────────────────────────────────────────────────
		"SubmitTxRequest": obj(
			prop("tx_base64", "string", "Transaction bytes encoded as base64"),
			prop("tx_hex", "string", "Transaction bytes encoded as hex (alternative to tx_base64)"),
		),
		"SubmitTxResponse": obj(
			prop("hash", "string", "Transaction hash (hex SHA-256)"),
		),
		"TxResultResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"found": map[string]any{"type": "boolean", "description": "Whether the tx was found"},
				"result": map[string]any{
					"type":        "object",
					"description": "Execution result (present only when found=true)",
					"properties": mergeProps(
						prop("hash", "string", "Transaction hash"),
						prop("height", "integer", "Block height"),
						prop("code", "integer", "Result code (0 = success)"),
						prop("log", "string", "Execution log"),
						prop("gas_used", "integer", "Gas consumed by the tx"),
						prop("gas_wanted", "integer", "Gas requested by the tx"),
						prop("bytes", "integer", "On-wire size of the encoded tx"),
						map[string]any{"events": map[string]any{
							"type": "array", "description": "Execution events",
							"items": map[string]any{"type": "object"},
						}},
					),
				},
				"cost": map[string]any{
					"$ref":        "#/components/schemas/TxCostBreakdown",
					"description": "Simulated cost using current pricing policy (present when found)",
				},
			},
		},
		"EstimateTxRequest": obj(
			prop("tx_base64", "string", "Signed tx bytes (base64)"),
			prop("tx_hex", "string", "Signed tx bytes (hex) — alternative to tx_base64"),
			prop("hash", "string", "Hash of an already-executed tx (looks up stored bytes/gas)"),
			prop("bytes", "integer", "Raw byte count when neither tx nor hash is supplied"),
			prop("gas", "integer", "Gas to price against (required with tx_base64/tx_hex/bytes; ignored for hash)"),
		),
		"TxCostBreakdown": obj(
			prop("bytes", "integer", "Tx byte count used for the DA cost"),
			prop("gas", "integer", "Gas units used for the execution cost"),
			prop("est_da_amount", "string", "Estimated DA cost as a fractional decimal (e.g. \"0.000123\")"),
			prop("est_da_denom", "string", "Denomination of est_da_amount (default: TIA)"),
			prop("est_gas_amount", "string", "Estimated gas cost as a fractional decimal"),
			prop("est_gas_denom", "string", "Denomination of est_gas_amount (default: ustake)"),
			prop("da_price_per_byte", "string", "Policy constant: TIA per byte"),
			prop("min_gas_price", "string", "Policy constant: gas-denom per gas unit"),
		),

		// ── WASM ────────────────────────────────────────────────
		"QuerySmartRequest": obj(
			prop("contract", "string", "Contract bech32 address"),
			prop("msg", "object", "Query message (JSON object)"),
		),
		"QuerySmartResponse": map[string]any{
			"type": "object",
			"properties": mergeProps(
				prop("data", "object", "Parsed query result"),
				prop("data_raw", "string", "Raw query result (when JSON parsing fails)"),
			),
		},

		// ── BLOB ────────────────────────────────────────────────
		"BlobSubmitRequest": obj(
			prop("data_base64", "string", "Raw data encoded as base64"),
		),
		"BlobSubmitResponse": obj(
			prop("commitment", "string", "SHA-256 commitment (hex) — store this on-chain"),
			prop("size", "integer", "Stored data size in bytes"),
		),
		"BlobRetrieveResponse": obj(
			prop("commitment", "string", "SHA-256 commitment (hex)"),
			prop("data_base64", "string", "Blob data encoded as base64"),
			prop("size", "integer", "Data size in bytes"),
		),
		"BlobBatchRequest": map[string]any{
			"type": "object",
			"required": []string{"blobs_base64"},
			"properties": map[string]any{
				"blobs_base64": map[string]any{
					"type":        "array",
					"description": "Array of base64-encoded blobs",
					"items":       map[string]any{"type": "string"},
				},
			},
		},
		"BlobBatchResponse": map[string]any{
			"type": "object",
			"properties": mergeProps(
				prop("root", "string", "Merkle root of the batch (hex) — commit this on-chain"),
				prop("count", "integer", "Number of blobs in the batch"),
				map[string]any{"commitments": map[string]any{
					"type":        "array",
					"description": "Per-blob SHA-256 commitments",
					"items":       map[string]any{"type": "string"},
				}},
			),
		},
	}
}

// ── helpers to keep the spec DRY ────────────────────────────────────────────

func ref(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func reqBody(contentType string, schema map[string]any) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			contentType: map[string]any{"schema": schema},
		},
	}
}

func resp(desc string, schema map[string]any) map[string]any {
	return map[string]any{
		"description": desc,
		"content": map[string]any{
			"application/json": map[string]any{"schema": schema},
		},
	}
}

func queryParam(name, typ string, required bool, desc string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    required,
		"description": desc,
		"schema":      map[string]any{"type": typ},
	}
}

func pathParam(name, typ, desc string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"required":    true,
		"description": desc,
		"schema":      map[string]any{"type": typ},
	}
}

func obj(props ...map[string]any) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": mergeProps(props...),
	}
}

func prop(name, typ, desc string) map[string]any {
	return map[string]any{
		name: map[string]any{"type": typ, "description": desc},
	}
}

func mergeProps(maps ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Cosmos-Exec API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>body{margin:0} .topbar{display:none}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: '/swagger.json',
      dom_id: '#swagger-ui',
      deepLinking: true,
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
      layout: 'BaseLayout'
    });
  </script>
</body>
</html>`
