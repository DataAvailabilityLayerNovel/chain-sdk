// package executor: chứa logic "thực thi" — nhận tx, chạy state transition,
// và (file này) lo việc LƯU/ĐỌC state xuống/từ ổ đĩa để sống sót qua restart.
package executor

import (
	"encoding/json"  // mã hoá/giải mã JSON (lưu metadata, tx, block).
	"fmt"            // tạo lỗi có ngữ cảnh (fmt.Errorf), quét chuỗi (Sscanf).
	"os"             // thao tác file: mở, ghi, đọc, rename, fsync.
	"path/filepath"  // ghép đường dẫn an toàn theo OS (filepath.Join).
	"sync"           // sync.Mutex — khoá chống ghi đồng thời (race).
)

// PersistStore provides disk-backed persistence for executor state:
//   - metadata.json: chain identity and latest checkpoint (initialized, chainID, stateRoot, heights)
//   - tx_results.jsonl: append-only tx execution results
//   - blocks.jsonl: append-only block info
//
// On startup, all files are replayed into memory. During operation, new data
// is appended. Metadata is overwritten (not appended) on every state change.
//
// VI: struct lưu state executor ra đĩa. metadata.json ghi đè mỗi lần đổi
// state; 2 file .jsonl chỉ ghi nối thêm (append). Khởi động sẽ đọc lại hết
// vào RAM.
type PersistStore struct {
	dir string     // thư mục chứa các file lưu trữ.
	mu  sync.Mutex // khoá: mỗi thao tác file phải Lock trước (an toàn goroutine).

	txFile    *os.File // handle file tx_results.jsonl (mở sẵn để append).
	blockFile *os.File // handle file blocks.jsonl.
}

// ChainMetadata holds the executor's critical state that must survive restarts.
//
// VI: state cốt lõi cần sống sót qua restart. Các thẻ `json:"..."` quy định
// tên field khi serialize ra JSON.
type ChainMetadata struct {
	Initialized     bool   `json:"initialized"`      // chain đã init chưa.
	ChainID         string `json:"chain_id"`         // định danh chain.
	StateRoot       string `json:"state_root"`       // hex-encoded — gốc state hiện tại.
	LastHeight      uint64 `json:"last_height"`      // block cao nhất đã thực thi.
	FinalizedHeight uint64 `json:"finalized_height"` // block cao nhất đã finalize.
}

// persistedTxResult: bao ngoài 1 bản ghi tx khi lưu (gắn thêm "type").
type persistedTxResult struct {
	Type string            `json:"type"` // luôn = "tx" (đánh dấu loại bản ghi).
	Data TxExecutionResult `json:"data"` // dữ liệu kết quả thực thi tx.
}

// persistedBlock: bao ngoài 1 bản ghi block khi lưu.
type persistedBlock struct {
	Type string    `json:"type"` // luôn = "block".
	Data BlockInfo `json:"data"` // thông tin block.
}

// NewPersistStore opens (or creates) the persistence directory and files.
//
// VI: constructor — tạo thư mục + mở 2 file append. Tham số dir.
// Trả về (*PersistStore, error).
func NewPersistStore(dir string) (*PersistStore, error) {
	// MkdirAll: tạo thư mục (kể cả thư mục cha) nếu chưa có; 0o755 = quyền rwxr-xr-x.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create persist dir: %w", err) // %w: bọc lỗi gốc.
	}

	// Mở file tx: O_CREATE (tạo nếu chưa có) | O_APPEND (ghi nối) | O_RDWR (đọc+ghi).
	txFile, err := os.OpenFile(filepath.Join(dir, "tx_results.jsonl"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open tx_results.jsonl: %w", err)
	}

	// Mở file block tương tự.
	blockFile, err := os.OpenFile(filepath.Join(dir, "blocks.jsonl"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		txFile.Close() // mở block lỗi → đóng file tx đã mở để không rò rỉ handle.
		return nil, fmt.Errorf("open blocks.jsonl: %w", err)
	}

	// Trả về con trỏ struct đã điền field.
	return &PersistStore{
		dir:       dir,
		txFile:    txFile,
		blockFile: blockFile,
	}, nil
}

// Close flushes and closes all files.
//
// VI: đóng mọi file. Trả về lỗi ĐẦU TIÊN gặp phải (nếu có).
func (p *PersistStore) Close() error {
	p.mu.Lock()         // giành khoá.
	defer p.mu.Unlock() // defer: tự mở khoá khi hàm kết thúc.

	var firstErr error
	// Lặp qua slice 2 file để đóng.
	for _, f := range []*os.File{p.txFile, p.blockFile} {
		if f != nil {
			// Chỉ lưu lỗi đầu tiên, vẫn tiếp tục đóng file còn lại.
			if err := f.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// AppendTxResult appends a tx result to disk.
//
// VI: ghi nối 1 kết quả tx xuống đĩa (1 dòng JSON).
func (p *PersistStore) AppendTxResult(result TxExecutionResult) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Marshal bản ghi (gắn type="tx") thành JSON bytes.
	line, err := json.Marshal(persistedTxResult{Type: "tx", Data: result})
	if err != nil {
		return err
	}
	// append(line, '\n'): nối ký tự xuống dòng → định dạng JSONL (1 JSON/dòng).
	_, err = p.txFile.Write(append(line, '\n'))
	return err
}

// AppendBlock appends a block info to disk.
//
// VI: ghi nối 1 block xuống đĩa (tương tự AppendTxResult).
func (p *PersistStore) AppendBlock(block BlockInfo) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	line, err := json.Marshal(persistedBlock{Type: "block", Data: block})
	if err != nil {
		return err
	}
	_, err = p.blockFile.Write(append(line, '\n'))
	return err
}

// SaveMetadata writes the chain metadata to disk (overwrite, not append).
// Called after InitChain, ExecuteTxs, and SetFinal.
//
// VI: ghi ĐÈ metadata.json (không append). Gọi sau init/thực thi/finalize.
func (p *PersistStore) SaveMetadata(meta ChainMetadata) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := json.Marshal(meta) // metadata -> JSON bytes.
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	path := filepath.Join(p.dir, "metadata.json")
	// Ghi an toàn (atomic + fsync) để không hỏng file khi mất điện.
	if err := writeFileAtomicSync(path, data); err != nil {
		return fmt.Errorf("persist metadata.json: %w", err)
	}
	return nil
}

// writeFileAtomicSync writes data to path durably: write to a temp file,
// fsync it, rename over the target, then fsync the parent directory so the
// rename itself survives a crash. Without the fsyncs a power loss can leave
// metadata.json truncated or the rename unobserved, corrupting chain state.
//
// VI: ghi "bền vững": ghi ra file .tmp → fsync → đổi tên đè file thật →
// fsync luôn thư mục cha (để thao tác rename cũng bền). Nếu thiếu fsync,
// mất điện có thể làm metadata.json hỏng/cụt.
func writeFileAtomicSync(path string, data []byte) error {
	tmpPath := path + ".tmp" // file tạm: <path>.tmp
	// Mở file tạm: WRONLY (chỉ ghi) | CREATE | TRUNC (xoá nội dung cũ).
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmpPath, err)
	}
	if _, err := f.Write(data); err != nil { // ghi dữ liệu vào file tạm.
		f.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := f.Sync(); err != nil { // Sync = fsync: ép dữ liệu xuống đĩa thật.
		f.Close()
		return fmt.Errorf("fsync %s: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	// Rename: thay thế nguyên tử file thật bằng file tạm.
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s: %w", tmpPath, err)
	}
	// fsync the directory so the rename is durable, not just the file data.
	// VI: mở thư mục cha rồi Sync để bản thân thao tác rename cũng bền.
	dir, err := os.Open(filepath.Dir(path)) // filepath.Dir: lấy thư mục chứa path.
	if err != nil {
		return fmt.Errorf("open dir for fsync: %w", err)
	}
	defer dir.Close() // đảm bảo đóng handle thư mục khi hàm xong.
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("fsync dir: %w", err)
	}
	return nil
}

// LoadMetadata reads the chain metadata from disk.
// Returns a zero-value ChainMetadata and nil error if the file does not exist.
//
// VI: đọc metadata.json. Nếu file CHƯA tồn tại → trả struct rỗng + nil
// (coi như chain mới, không phải lỗi).
func (p *PersistStore) LoadMetadata() (ChainMetadata, error) {
	data, err := os.ReadFile(filepath.Join(p.dir, "metadata.json"))
	if err != nil {
		if os.IsNotExist(err) { // phân biệt "không có file" với lỗi I/O thật.
			return ChainMetadata{}, nil
		}
		return ChainMetadata{}, fmt.Errorf("read metadata.json: %w", err)
	}

	var meta ChainMetadata
	// Unmarshal: JSON bytes -> struct (truyền con trỏ &meta để ghi vào).
	if err := json.Unmarshal(data, &meta); err != nil {
		return ChainMetadata{}, fmt.Errorf("parse metadata.json: %w", err)
	}
	return meta, nil
}

// LoadTxResults reads all persisted tx results.
// Returns an error for I/O failures; corrupt lines are skipped with a count.
//
// VI: đọc toàn bộ tx đã lưu thành map[hash]kết_quả. Trả thêm số dòng
// hỏng bị bỏ qua (skipped). Lỗi I/O thật mới trả error.
func (p *PersistStore) LoadTxResults() (map[string]TxExecutionResult, int, error) {
	results := make(map[string]TxExecutionResult) // map kết quả, khởi tạo rỗng.
	data, err := os.ReadFile(filepath.Join(p.dir, "tx_results.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return results, 0, nil // chưa có file → map rỗng, 0 dòng hỏng.
		}
		return nil, 0, fmt.Errorf("read tx_results.jsonl: %w", err)
	}

	skipped := 0
	// splitLines: tách dữ liệu thành từng dòng ([]byte).
	for _, line := range splitLines(data) {
		if len(line) == 0 { // bỏ qua dòng trống.
			continue
		}
		var rec persistedTxResult
		if err := json.Unmarshal(line, &rec); err != nil {
			skipped++ // dòng JSON hỏng → đếm và bỏ qua (không làm hỏng cả file).
			continue
		}
		results[rec.Data.Hash] = rec.Data // key = hash tx; dòng sau ghi đè dòng trước.
	}
	return results, skipped, nil
}

// LoadBlocks reads all persisted block infos.
// Returns an error for I/O failures; corrupt lines are skipped with a count.
//
// VI: như LoadTxResults nhưng cho block, key = chiều cao block (Height).
func (p *PersistStore) LoadBlocks() (map[uint64]BlockInfo, int, error) {
	blocks := make(map[uint64]BlockInfo)
	data, err := os.ReadFile(filepath.Join(p.dir, "blocks.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return blocks, 0, nil
		}
		return nil, 0, fmt.Errorf("read blocks.jsonl: %w", err)
	}

	skipped := 0
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec persistedBlock
		if err := json.Unmarshal(line, &rec); err != nil {
			skipped++
			continue
		}
		blocks[rec.Data.Height] = rec.Data
	}
	return blocks, skipped, nil
}

// splitLines: tách []byte thành danh sách dòng (cắt tại '\n'), không giữ '\n'.
func splitLines(data []byte) [][]byte {
	var lines [][]byte // slice các dòng, ban đầu nil.
	start := 0         // vị trí bắt đầu dòng hiện tại.
	// for i, b := range data — i: chỉ số, b: byte tại i.
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i]) // lát [start:i) = 1 dòng.
			start = i + 1                        // dòng kế bắt đầu sau '\n'.
		}
	}
	if start < len(data) { // phần còn lại không kết thúc bằng '\n'.
		lines = append(lines, data[start:])
	}
	return lines
}

// hexDecode: tự giải mã chuỗi hex thành []byte (vd "ff00" -> [255,0]).
func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 { // hex hợp lệ phải có số ký tự chẵn (2 ký tự/byte).
		return nil, fmt.Errorf("odd hex length")
	}
	result := make([]byte, len(s)/2) // cấp slice kết quả đúng kích thước.
	for i := 0; i < len(result); i++ {
		var v byte
		// Sscanf với "%02x": đọc 2 ký tự hex thành 1 byte v.
		_, err := fmt.Sscanf(s[i*2:i*2+2], "%02x", &v)
		if err != nil {
			return nil, err
		}
		result[i] = v
	}
	return result, nil
}
