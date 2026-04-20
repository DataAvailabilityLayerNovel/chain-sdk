package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PersistStore provides disk-backed persistence for executor state:
//   - metadata.json: chain identity and latest checkpoint (initialized, chainID, stateRoot, heights)
//   - tx_results.jsonl: append-only tx execution results
//   - blocks.jsonl: append-only block info
//   - blobs.jsonl: append-only blob data
//
// On startup, all files are replayed into memory. During operation, new data
// is appended. Metadata is overwritten (not appended) on every state change.
type PersistStore struct {
	dir string
	mu  sync.Mutex

	txFile    *os.File
	blockFile *os.File
	blobFile  *os.File
}

// ChainMetadata holds the executor's critical state that must survive restarts.
type ChainMetadata struct {
	Initialized     bool   `json:"initialized"`
	ChainID         string `json:"chain_id"`
	StateRoot       string `json:"state_root"`       // hex-encoded
	LastHeight      uint64 `json:"last_height"`
	FinalizedHeight uint64 `json:"finalized_height"`
}

type persistedTxResult struct {
	Type string            `json:"type"`
	Data TxExecutionResult `json:"data"`
}

type persistedBlock struct {
	Type string    `json:"type"`
	Data BlockInfo `json:"data"`
}

type persistedBlob struct {
	Type       string `json:"type"`
	Commitment string `json:"commitment"`
	DataHex    string `json:"data_hex"`
}

// NewPersistStore opens (or creates) the persistence directory and files.
func NewPersistStore(dir string) (*PersistStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create persist dir: %w", err)
	}

	txFile, err := os.OpenFile(filepath.Join(dir, "tx_results.jsonl"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open tx_results.jsonl: %w", err)
	}

	blockFile, err := os.OpenFile(filepath.Join(dir, "blocks.jsonl"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		txFile.Close()
		return nil, fmt.Errorf("open blocks.jsonl: %w", err)
	}

	blobFile, err := os.OpenFile(filepath.Join(dir, "blobs.jsonl"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		txFile.Close()
		blockFile.Close()
		return nil, fmt.Errorf("open blobs.jsonl: %w", err)
	}

	return &PersistStore{
		dir:       dir,
		txFile:    txFile,
		blockFile: blockFile,
		blobFile:  blobFile,
	}, nil
}

// Close flushes and closes all files.
func (p *PersistStore) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var firstErr error
	for _, f := range []*os.File{p.txFile, p.blockFile, p.blobFile} {
		if f != nil {
			if err := f.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// AppendTxResult appends a tx result to disk.
func (p *PersistStore) AppendTxResult(result TxExecutionResult) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	line, err := json.Marshal(persistedTxResult{Type: "tx", Data: result})
	if err != nil {
		return err
	}
	_, err = p.txFile.Write(append(line, '\n'))
	return err
}

// AppendBlock appends a block info to disk.
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

// AppendBlob appends a blob commitment+data to disk.
func (p *PersistStore) AppendBlob(commitment string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	line, err := json.Marshal(persistedBlob{
		Type:       "blob",
		Commitment: commitment,
		DataHex:    fmt.Sprintf("%x", data),
	})
	if err != nil {
		return err
	}
	_, err = p.blobFile.Write(append(line, '\n'))
	return err
}

// SaveMetadata writes the chain metadata to disk (overwrite, not append).
// Called after InitChain, ExecuteTxs, and SetFinal.
func (p *PersistStore) SaveMetadata(meta ChainMetadata) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	path := filepath.Join(p.dir, "metadata.json")
	tmpPath := path + ".tmp"

	// Atomic write: write to temp, then rename.
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write metadata.json.tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename metadata.json: %w", err)
	}
	return nil
}

// LoadMetadata reads the chain metadata from disk.
// Returns a zero-value ChainMetadata and nil error if the file does not exist.
func (p *PersistStore) LoadMetadata() (ChainMetadata, error) {
	data, err := os.ReadFile(filepath.Join(p.dir, "metadata.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return ChainMetadata{}, nil
		}
		return ChainMetadata{}, fmt.Errorf("read metadata.json: %w", err)
	}

	var meta ChainMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return ChainMetadata{}, fmt.Errorf("parse metadata.json: %w", err)
	}
	return meta, nil
}

// LoadTxResults reads all persisted tx results.
// Returns an error for I/O failures; corrupt lines are skipped with a count.
func (p *PersistStore) LoadTxResults() (map[string]TxExecutionResult, int, error) {
	results := make(map[string]TxExecutionResult)
	data, err := os.ReadFile(filepath.Join(p.dir, "tx_results.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return results, 0, nil
		}
		return nil, 0, fmt.Errorf("read tx_results.jsonl: %w", err)
	}

	skipped := 0
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec persistedTxResult
		if err := json.Unmarshal(line, &rec); err != nil {
			skipped++
			continue
		}
		results[rec.Data.Hash] = rec.Data
	}
	return results, skipped, nil
}

// LoadBlocks reads all persisted block infos.
// Returns an error for I/O failures; corrupt lines are skipped with a count.
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

// LoadBlobs reads all persisted blobs into the given BlobStore.
// Returns an error for I/O failures; corrupt lines are skipped with a count.
func (p *PersistStore) LoadBlobs(store *BlobStore) (loaded int, skipped int, err error) {
	data, err := os.ReadFile(filepath.Join(p.dir, "blobs.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("read blobs.jsonl: %w", err)
	}

	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec persistedBlob
		if err := json.Unmarshal(line, &rec); err != nil {
			skipped++
			continue
		}
		blobData, err := hexDecode(rec.DataHex)
		if err != nil {
			skipped++
			continue
		}
		if _, err := store.Put(blobData); err != nil {
			skipped++
			continue
		}
		loaded++
	}
	return loaded, skipped, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd hex length")
	}
	result := make([]byte, len(s)/2)
	for i := 0; i < len(result); i++ {
		var v byte
		_, err := fmt.Sscanf(s[i*2:i*2+2], "%02x", &v)
		if err != nil {
			return nil, err
		}
		result[i] = v
	}
	return result, nil
}
