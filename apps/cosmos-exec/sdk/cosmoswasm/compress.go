// package cosmoswasm: lớp public mỏng cho tiện ích NÉN (gzip), bọc
// internal/compress. Mục đích trong blob-first: nén data TRƯỚC khi đẩy lên
// Celestia → ít byte hơn → DA_cost rẻ hơn (DA_cost = bytes × giá/byte) và
// submit nhanh hơn. Cặp dùng chính: CompressIfBeneficial (ghi) ↔ MaybeDecompress
// (đọc). Cả hai an toàn: không bao giờ làm phình data, và tự nhận diện gzip.
package cosmoswasm

import (
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/compress"
)

// CompressGzip nén data bằng gzip (mức BestSpeed — ưu tiên tốc độ).
//
// VI: nén thô. Thường nên dùng CompressIfBeneficial thay vì gọi trực tiếp, để
// tránh trường hợp data không nén được bị phình.
func CompressGzip(data []byte) ([]byte, error) {
	return compress.Gzip(data)
}

// DecompressGzip giải nén data gzip về dạng gốc.
func DecompressGzip(data []byte) ([]byte, error) {
	return compress.Gunzip(data)
}

// IsGzipCompressed trả true nếu data bắt đầu bằng magic byte của gzip (0x1f 0x8b).
//
// VI: dùng để biết một blob lấy về có phải dạng nén không (không cần lưu cờ riêng).
func IsGzipCompressed(data []byte) bool {
	return compress.IsGzipped(data)
}

// CompressIfBeneficial nén data và CHỈ trả về dạng nén khi nó NHỎ HƠN bản gốc.
// Nếu không (data ngẫu nhiên, ảnh JPEG, đã nén...) → trả về (data gốc, false).
//
// VI: an toàn để gọi trước mọi SubmitBlob — không bao giờ làm data to ra. Bool
// thứ hai cho biết có nén thật hay không (chủ yếu để log/metric).
func CompressIfBeneficial(data []byte) ([]byte, bool) {
	return compress.IfBeneficial(data)
}

// MaybeDecompress giải nén nếu data là gzip, ngược lại trả nguyên không đổi.
//
// VI: cặp đôi của CompressIfBeneficial ở chiều đọc. Tự nhận diện qua magic byte
// nên không cần biết trước blob có nén hay không.
func MaybeDecompress(data []byte) ([]byte, error) {
	return compress.MaybeDecompress(data)
}
