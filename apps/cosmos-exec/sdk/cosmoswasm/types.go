package cosmoswasm

type SubmitTxResponse struct {
	Hash string `json:"hash"`
}

type GetTxResultResponse struct {
	Found  bool               `json:"found"`
	Result *TxExecutionResult `json:"result,omitempty"`
}

type TxEventAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type TxEvent struct {
	Type       string             `json:"type"`
	Attributes []TxEventAttribute `json:"attributes"`
}

type TxExecutionResult struct {
	Hash   string    `json:"hash"`
	Height uint64    `json:"height"`
	Code   uint32    `json:"code"`
	Log    string    `json:"log"`
	Events []TxEvent `json:"events,omitempty"`
}

type QuerySmartResponse struct {
	Data    any    `json:"data,omitempty"`
	DataRaw string `json:"data_raw,omitempty"`
}

type InstantiateTxRequest struct {
	Sender string
	CodeID uint64
	Msg    any
	Label  string
	Admin  string
}

type ExecuteTxRequest struct {
	Sender   string
	Contract string
	Msg      any
}
