// package cosmoswasm: lớp public mỏng cho tiện ích CHUNK (cắt nhỏ blob), bọc
// internal/chunk. Mục đích trong blob-first: data lớn hơn 1 blob (MaxBlobSize)
// được cắt thành nhiều mảnh để đẩy qua SubmitBatch, rồi ghép lại + kiểm tra
// toàn vẹn khi đọc.
package cosmoswasm

import (
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/chunk"
)

// DefaultChunkSize is the default per-chunk byte limit when ChunkBlob is called
// with maxSize <= 0 (512 KiB).
const DefaultChunkSize = chunk.DefaultMaxSize

// ChunkMeta describes how an oversized blob was split, so it can be reassembled
// and verified later. Keep it alongside the chunks (e.g. record on-chain or in
// app metadata) — ReassembleChunks needs OriginalHash to check integrity.
//
// VI: "sổ tay" mô tả cách 1 blob lớn bị cắt. PHẢI giữ lại để ghép đúng và verify.
type ChunkMeta struct {
	// OriginalHash is the hex SHA-256 of the original (pre-split) data.
	OriginalHash string `json:"original_hash"`
	// TotalChunks is the number of pieces.
	TotalChunks int `json:"total_chunks"`
	// ChunkCommitments are the hex SHA-256 of each piece, in order.
	ChunkCommitments []string `json:"chunk_commitments"`
}

// ChunkBlob splits data into pieces of at most maxSize bytes (maxSize <= 0 uses
// DefaultChunkSize). If data already fits in one piece, it returns a single
// chunk and a nil ChunkMeta (no reassembly needed).
//
// VI: cắt data thành mảnh ≤ maxSize. Vừa 1 mảnh → trả [data] + meta nil.
func ChunkBlob(data []byte, maxSize int) ([][]byte, *ChunkMeta) {
	chunks, meta := chunk.Split(data, maxSize)
	if meta == nil {
		return chunks, nil
	}
	return chunks, &ChunkMeta{
		OriginalHash:     meta.OriginalHash,
		TotalChunks:      meta.TotalChunks,
		ChunkCommitments: meta.ChunkCommitments,
	}
}

// ReassembleChunks concatenates ordered chunks back into the original data. If
// meta is non-nil, it verifies the result hashes to meta.OriginalHash and
// returns an error on mismatch (missing/corrupt/reordered chunk).
//
// VI: ghép mảnh theo thứ tự. Có meta → verify OriginalHash (bắt lỗi thiếu/hỏng/lệch).
func ReassembleChunks(chunks [][]byte, meta *ChunkMeta) ([]byte, error) {
	var im *chunk.Meta
	if meta != nil {
		im = &chunk.Meta{
			OriginalHash:     meta.OriginalHash,
			TotalChunks:      meta.TotalChunks,
			ChunkCommitments: meta.ChunkCommitments,
		}
	}
	return chunk.Reassemble(chunks, im)
}
