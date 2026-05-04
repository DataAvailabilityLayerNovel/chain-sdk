package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	db "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/libs/log"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	"github.com/CosmWasm/wasmd/x/wasm/keeper/testdata"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/executor"
)

// TestIntegration_SubmitExecuteQuery tests the full lifecycle:
// 1. InitChain
// 2. Submit a store WASM tx via HTTP
// 3. Execute it via ExecuteTxs
// 4. Query tx result via HTTP — should be found with code=0
// 5. Verify block is recorded
func TestIntegration_SubmitExecuteQuery(t *testing.T) {
	ctx := context.Background()
	exec := executor.New(app.New(log.NewNopLogger(), db.NewMemDB()))

	sender := sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20))

	// 1. Init chain.
	stateRoot, err := exec.InitChain(ctx, time.Now(), 1, "integration-test")
	if err != nil {
		t.Fatalf("init chain: %v", err)
	}

	// 2. Build and submit a store WASM tx via HTTP handler.
	storeTx, err := buildTestTx(&wasmtypes.MsgStoreCode{
		Sender:       sender.String(),
		WASMByteCode: testdata.ReflectContractWasm(),
	})
	if err != nil {
		t.Fatalf("build store tx: %v", err)
	}

	submitBody, _ := json.Marshal(submitTxRequest{
		TxHex: bytesToHex(storeTx),
	})
	submitReq := httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(submitBody))
	submitW := httptest.NewRecorder()
	submitTxHandler(exec)(submitW, submitReq)

	if submitW.Code != http.StatusOK {
		t.Fatalf("submit: expected 200, got %d: %s", submitW.Code, submitW.Body.String())
	}

	var submitResp submitTxResponse
	json.Unmarshal(submitW.Body.Bytes(), &submitResp)
	if submitResp.Hash == "" {
		t.Fatal("expected tx hash in submit response")
	}
	txHash := submitResp.Hash

	// Verify tx is pending.
	pendingReq := httptest.NewRequest(http.MethodGet, "/tx/pending", nil)
	pendingW := httptest.NewRecorder()
	txPendingHandler(exec)(pendingW, pendingReq)

	var pendingResp map[string]any
	json.Unmarshal(pendingW.Body.Bytes(), &pendingResp)
	if pendingResp["pending_count"] != float64(1) {
		t.Fatalf("expected 1 pending tx, got %v", pendingResp["pending_count"])
	}

	// 3. Pull txs from mempool and execute.
	txs, err := exec.GetTxs(ctx)
	if err != nil {
		t.Fatalf("get txs: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 tx in mempool, got %d", len(txs))
	}

	stateRoot2, err := exec.ExecuteTxs(ctx, txs, 1, time.Now(), stateRoot)
	if err != nil {
		t.Fatalf("execute txs: %v", err)
	}
	if len(stateRoot2) == 0 {
		t.Fatal("state root is empty after execute")
	}

	// 4. Query tx result via HTTP — should be found.
	// Use the /tx/result endpoint.
	resultReq := httptest.NewRequest(http.MethodGet, "/tx/result?hash="+txHash, nil)
	resultW := httptest.NewRecorder()
	txResultHandler(exec)(resultW, resultReq)

	if resultW.Code != http.StatusOK {
		t.Fatalf("tx result: expected 200, got %d: %s", resultW.Code, resultW.Body.String())
	}

	var resultResp map[string]any
	json.Unmarshal(resultW.Body.Bytes(), &resultResp)
	if resultResp["found"] != true {
		t.Fatalf("expected found=true, got %v", resultResp["found"])
	}

	resultData, ok := resultResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result to be object, got %T", resultResp["result"])
	}
	if resultData["code"] != float64(0) {
		t.Fatalf("expected code=0, got %v (log=%v)", resultData["code"], resultData["log"])
	}

	// 5. Verify block is recorded.
	blockReq := httptest.NewRequest(http.MethodGet, "/blocks/latest", nil)
	blockW := httptest.NewRecorder()
	blocksLatestHandler(exec)(blockW, blockReq)

	if blockW.Code != http.StatusOK {
		t.Fatalf("blocks latest: expected 200, got %d", blockW.Code)
	}

	var blockResp map[string]any
	json.Unmarshal(blockW.Body.Bytes(), &blockResp)
	if blockResp["height"] != float64(1) {
		t.Fatalf("expected block height 1, got %v", blockResp["height"])
	}
	if blockResp["num_txs"] != float64(1) {
		t.Fatalf("expected 1 tx in block, got %v", blockResp["num_txs"])
	}

	// 6. Verify status is correct.
	statusReq := httptest.NewRequest(http.MethodGet, "/status", nil)
	statusW := httptest.NewRecorder()
	statusHandler(exec)(statusW, statusReq)

	var statusResp map[string]any
	json.Unmarshal(statusW.Body.Bytes(), &statusResp)
	if statusResp["latest_height"] != float64(1) {
		t.Fatalf("expected latest_height=1, got %v", statusResp["latest_height"])
	}
}

func buildTestTx(msgs ...sdk.Msg) ([]byte, error) {
	packedMsgs := make([]*codectypes.Any, 0, len(msgs))
	for _, msg := range msgs {
		anyMsg, err := codectypes.NewAnyWithValue(msg)
		if err != nil {
			return nil, err
		}
		packedMsgs = append(packedMsgs, anyMsg)
	}

	bodyBytes, err := proto.Marshal(&txv1beta1.TxBody{Messages: packedMsgs})
	if err != nil {
		return nil, err
	}

	authInfoBytes, err := proto.Marshal(&txv1beta1.AuthInfo{})
	if err != nil {
		return nil, err
	}

	return proto.Marshal(&txv1beta1.TxRaw{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
	})
}

func bytesToHex(b []byte) string {
	const hextable = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, v := range b {
		dst[i*2] = hextable[v>>4]
		dst[i*2+1] = hextable[v&0x0f]
	}
	return string(dst)
}
