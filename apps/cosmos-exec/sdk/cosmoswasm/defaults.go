// package cosmoswasm: các hằng "gợi ý mặc định" để caller dùng khi không
// muốn tự nghĩ con số phù hợp.
package cosmoswasm

import "time" // type Duration cho khoảng thời gian.

const (
	// DefaultPollInterval is the suggested WaitTxResult polling cadence.
	// VI: chu kỳ poll mặc định khi đợi tx đóng block — 1s là cân bằng giữa
	// "thấy nhanh" và "không spam server".
	DefaultPollInterval = 1 * time.Second

	// DefaultTxTimeout is the suggested context timeout for a single tx lifecycle
	// (submit + wait for result).
	// VI: timeout cho 1 vòng đời tx (submit + đợi kết quả) = 60s — đủ cho
	// đa số trường hợp; tx WASM phức tạp có thể cần dài hơn.
	DefaultTxTimeout = 60 * time.Second
)
