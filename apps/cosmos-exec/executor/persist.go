package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PersistStore provides optional disk-backed persistence for tx results,
// block info, and blob metadata. Each type is stored as a JSON-lines file
// that is replayed on startup and appended to during operation.

// PersistStore wraps file-based append-only storage.
type PersistStore struct {
	dir string
	mu  sync.Mutex

	txFile    *os.File
	blockFile *os.File
	blobFile  *os.File
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

// LoadTxResults reads all persisted tx results.
func (p *PersistStore) LoadTxResults() (map[string]TxExecutionResult, error) {
	results := make(map[string]TxExecutionResult)
	data, err := os.ReadFile(filepath.Join(p.dir, "tx_results.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return results, nil
		}
		return nil, err
	}

	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec persistedTxResult
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		results[rec.Data.Hash] = rec.Data
	}
	return results, nil
}

// LoadBlocks reads all persisted block infos.
func (p *PersistStore) LoadBlocks() (map[uint64]BlockInfo, error) {
	blocks := make(map[uint64]BlockInfo)
	data, err := os.ReadFile(filepath.Join(p.dir, "blocks.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return blocks, nil
		}
		return nil, err
	}

	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec persistedBlock
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		blocks[rec.Data.Height] = rec.Data
	}
	return blocks, nil
}

// LoadBlobs reads all persisted blobs into the given BlobStore.
func (p *PersistStore) LoadBlobs(store *BlobStore) (int, error) {
	data, err := os.ReadFile(filepath.Join(p.dir, "blobs.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	count := 0
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec persistedBlob
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		blobData, err := hexDecode(rec.DataHex)
		if err != nil {
			continue
		}
		if _, err := store.Put(blobData); err == nil {
			count++
		}
	}
	return count, nil
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
