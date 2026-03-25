package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	bolt "github.com/boltdb/bolt"
	pb "github.com/evstack/ev-node/types/pb/evnode/v1"

	rpcclient "github.com/evstack/ev-node/pkg/rpc/client"
)

const (
	defaultAddr       = ":8090"
	defaultDBPath     = ".data/cosmos-explorer/index.db"
	defaultNodeRPCURL = "http://127.0.0.1:38331"

	bucketBlocks = "blocks"
	bucketTxs    = "txs"
	bucketMeta   = "meta"

	metaLatestIndexedHeight = "latest_indexed_height"
)

type explorerServer struct {
	rpcURL string
	client *rpcclient.Client
	db     *bolt.DB

	autoIndexer *autoIndexerRuntime
}

type autoIndexerRuntime struct {
	enabled          bool
	interval         time.Duration
	maxBlocksPerTick int

	mu                 sync.RWMutex
	running            bool
	lastRunAt          time.Time
	lastError          string
	lastIndexedHeight  uint64
	lastChainHeight    uint64
	lastIndexedInBatch uint64
}

type indexedBlock struct {
	Height         uint64   `json:"height"`
	Time           string   `json:"time"`
	TimeUnixNano   uint64   `json:"time_unix_nano"`
	LastHeaderHash string   `json:"last_header_hash"`
	DataHash       string   `json:"data_hash"`
	AppHash        string   `json:"app_hash"`
	NumTxs         int      `json:"num_txs"`
	TxHashes       []string `json:"tx_hashes"`
	HeaderDAHeight uint64   `json:"header_da_height"`
	DataDAHeight   uint64   `json:"data_da_height"`
}

type indexedTx struct {
	Hash           string `json:"hash"`
	Height         uint64 `json:"height"`
	Index          int    `json:"index"`
	Size           int    `json:"size"`
	RawHex         string `json:"raw_hex"`
	HeaderDAHeight uint64 `json:"header_da_height"`
	DataDAHeight   uint64 `json:"data_da_height"`
	Time           string `json:"time"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "reindex":
		if err := runReindex(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "help", "-h", "--help":
		printUsage()
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Cosmos Explorer (reuse evnode RPC)

Usage:
  go run ./tools/cosmos-explorer serve [flags]
  go run ./tools/cosmos-explorer reindex [flags]

Commands:
  serve
    Start Explorer API (P1) and reuse DA endpoints via proxy.

  reindex
    Build/update local index DB by block height range (P2).

Common flags:
  --rpc-url   evnode RPC URL (default: env EVNODE_RPC_URL/WASM_RPC_URL/NODE or http://127.0.0.1:38331)
  --db-path   index DB path (default: .data/cosmos-explorer/index.db)

serve flags:
  --addr      HTTP listen address (default: :8090)
	--auto-index          run background indexer while serving (default: true)
	--sync-interval       auto-index interval (default: 3s)
	--max-blocks-per-tick max blocks to index per tick (default: 50)

reindex flags:
  --from      start height (default: latest_indexed_height+1)
  --to        end height (default: latest chain height)
`)
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "HTTP listen address")
	rpcURL := fs.String("rpc-url", getRPCURLFromEnv(), "evnode RPC URL")
	dbPath := fs.String("db-path", defaultDBPath, "index DB path")
	autoIndex := fs.Bool("auto-index", true, "run background indexer while serving")
	syncInterval := fs.Duration("sync-interval", 3*time.Second, "auto-index interval")
	maxBlocksPerTick := fs.Int("max-blocks-per-tick", 50, "max blocks to index per tick")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *syncInterval <= 0 {
		return fmt.Errorf("--sync-interval must be > 0")
	}
	if *maxBlocksPerTick <= 0 {
		return fmt.Errorf("--max-blocks-per-tick must be > 0")
	}

	db, err := openIndexDB(*dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	s := &explorerServer{
		rpcURL: strings.TrimRight(*rpcURL, "/"),
		client: rpcclient.NewClient(*rpcURL),
		db:     db,
		autoIndexer: &autoIndexerRuntime{
			enabled:          *autoIndex,
			interval:         *syncInterval,
			maxBlocksPerTick: *maxBlocksPerTick,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/blocks/latest", s.handleLatestBlock)
	mux.HandleFunc("/api/v1/blocks/", s.handleBlockByHeight)
	mux.HandleFunc("/api/v1/txs/", s.handleTxByHash)
	mux.HandleFunc("/api/v1/search", s.handleSearch)
	mux.HandleFunc("/api/v1/indexer/state", s.handleIndexerState)

	mux.HandleFunc("/api/v1/da", s.handleDAProxy)
	mux.HandleFunc("/api/v1/da/", s.handleDAProxy)

	log.Printf("explorer api listening on %s", *addr)
	log.Printf("node rpc: %s", *rpcURL)
	log.Printf("index db: %s", *dbPath)
	if *autoIndex {
		log.Printf("auto-index enabled: interval=%s max_blocks_per_tick=%d", syncInterval.String(), *maxBlocksPerTick)
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if *autoIndex {
		go s.runAutoIndexer(serveCtx)
	}

	server := &http.Server{Addr: *addr, Handler: mux}
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runReindex(args []string) error {
	fs := flag.NewFlagSet("reindex", flag.ContinueOnError)
	rpcURL := fs.String("rpc-url", getRPCURLFromEnv(), "evnode RPC URL")
	dbPath := fs.String("db-path", defaultDBPath, "index DB path")
	from := fs.Uint64("from", 0, "start height (0 = latest indexed + 1)")
	to := fs.Uint64("to", 0, "end height (0 = latest chain height)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := openIndexDB(*dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	client := rpcclient.NewClient(*rpcURL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	state, err := client.GetState(ctx)
	if err != nil {
		return fmt.Errorf("get chain state: %w", err)
	}
	latestChainHeight := state.GetLastBlockHeight()
	if latestChainHeight == 0 {
		log.Println("chain has no blocks, nothing to index")
		return nil
	}

	resolvedTo := *to
	if resolvedTo == 0 {
		resolvedTo = latestChainHeight
	}
	if resolvedTo > latestChainHeight {
		resolvedTo = latestChainHeight
	}

	resolvedFrom := *from
	if resolvedFrom == 0 {
		last, err := getLatestIndexedHeight(db)
		if err != nil {
			return err
		}
		resolvedFrom = last + 1
	}

	if resolvedFrom == 0 {
		resolvedFrom = 1
	}
	if resolvedFrom > resolvedTo {
		log.Printf("nothing to index (from=%d to=%d)", resolvedFrom, resolvedTo)
		return nil
	}

	log.Printf("reindex start: from=%d to=%d rpc=%s", resolvedFrom, resolvedTo, *rpcURL)
	startTime := time.Now()

	for h := resolvedFrom; h <= resolvedTo; h++ {
		if err := indexHeight(client, db, h); err != nil {
			return fmt.Errorf("index height %d: %w", h, err)
		}
		if h%50 == 0 || h == resolvedTo {
			log.Printf("indexed up to height=%d", h)
		}
	}

	log.Printf("reindex done: heights=%d duration=%s", resolvedTo-resolvedFrom+1, time.Since(startTime).Round(time.Millisecond))
	return nil
}

func (s *explorerServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().UTC().Format(time.RFC3339)})
}

func (s *explorerServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	state, err := s.client.GetState(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	lastBlockTime := ""
	if state.GetLastBlockTime() != nil {
		lastBlockTime = state.GetLastBlockTime().AsTime().UTC().Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"chain_id":          state.GetChainId(),
		"initial_height":    state.GetInitialHeight(),
		"last_block_height": state.GetLastBlockHeight(),
		"last_block_time":   lastBlockTime,
		"da_height":         state.GetDaHeight(),
		"app_hash":          hex.EncodeToString(state.GetAppHash()),
		"last_header_hash":  hex.EncodeToString(state.GetLastHeaderHash()),
	})
}

func (s *explorerServer) handleLatestBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	resp, err := s.client.GetBlockByHeight(ctx, 0)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	blk, err := blockFromResponse(resp)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, blk)
}

func (s *explorerServer) handleBlockByHeight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	heightStr := strings.TrimPrefix(r.URL.Path, "/api/v1/blocks/")
	height, err := strconv.ParseUint(heightStr, 10, 64)
	if err != nil || height == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid block height"})
		return
	}

	if blk, ok, err := getIndexedBlock(s.db, height); err == nil && ok {
		writeJSON(w, http.StatusOK, blk)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.client.GetBlockByHeight(ctx, height)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	blk, err := blockFromResponse(resp)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, blk)
}

func (s *explorerServer) handleTxByHash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	hash := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/v1/txs/")), "0x"))
	if hash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tx hash is required"})
		return
	}

	if tx, ok, err := getIndexedTx(s.db, hash); err == nil && ok {
		writeJSON(w, http.StatusOK, tx)
		return
	}

	scanDepth := uint64(300)
	if val := strings.TrimSpace(r.URL.Query().Get("scan_depth")); val != "" {
		if parsed, err := strconv.ParseUint(val, 10, 64); err == nil && parsed > 0 {
			scanDepth = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	res, err := scanTxByHash(ctx, s.client, hash, scanDepth)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *explorerServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "q is required"})
		return
	}

	if h, err := strconv.ParseUint(q, 10, 64); err == nil && h > 0 {
		if blk, ok, err := getIndexedBlock(s.db, h); err == nil && ok {
			writeJSON(w, http.StatusOK, map[string]any{"type": "block", "result": blk})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		resp, err := s.client.GetBlockByHeight(ctx, h)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"type": "block", "error": err.Error()})
			return
		}
		blk, err := blockFromResponse(resp)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"type": "block", "result": blk})
		return
	}

	hash := strings.ToLower(strings.TrimPrefix(q, "0x"))
	if tx, ok, err := getIndexedTx(s.db, hash); err == nil && ok {
		writeJSON(w, http.StatusOK, map[string]any{"type": "tx", "result": tx})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	res, err := scanTxByHash(ctx, s.client, hash, 300)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if found, _ := res["found"].(bool); !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"type": "tx", "result": res})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"type": "tx", "result": res})
}

func (s *explorerServer) handleIndexerState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	latestIndexed, err := getLatestIndexedHeight(s.db)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	state, err := s.client.GetState(ctx)
	latestChain := uint64(0)
	lag := uint64(0)
	nodeError := ""
	if err != nil {
		nodeError = err.Error()
	} else {
		latestChain = state.GetLastBlockHeight()
		if latestChain > latestIndexed {
			lag = latestChain - latestIndexed
		}
	}

	autoState := map[string]any{"enabled": false}
	if s.autoIndexer != nil {
		s.autoIndexer.mu.RLock()
		autoState = map[string]any{
			"enabled":              s.autoIndexer.enabled,
			"running":              s.autoIndexer.running,
			"interval":             s.autoIndexer.interval.String(),
			"max_blocks_per_tick":  s.autoIndexer.maxBlocksPerTick,
			"last_run_at":          formatTimeOrEmpty(s.autoIndexer.lastRunAt),
			"last_error":           s.autoIndexer.lastError,
			"last_indexed_height":  s.autoIndexer.lastIndexedHeight,
			"last_chain_height":    s.autoIndexer.lastChainHeight,
			"last_indexed_in_batch": s.autoIndexer.lastIndexedInBatch,
		}
		s.autoIndexer.mu.RUnlock()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"latest_indexed_height": latestIndexed,
		"latest_chain_height":   latestChain,
		"lag":                   lag,
		"node_error":            nodeError,
		"auto_indexer":          autoState,
	})
}

func (s *explorerServer) runAutoIndexer(ctx context.Context) {
	if s.autoIndexer == nil || !s.autoIndexer.enabled {
		return
	}

	ticker := time.NewTicker(s.autoIndexer.interval)
	defer ticker.Stop()

	s.runAutoIndexerTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runAutoIndexerTick(ctx)
		}
	}
}

func (s *explorerServer) runAutoIndexerTick(ctx context.Context) {
	if s.autoIndexer == nil {
		return
	}

	s.autoIndexer.mu.Lock()
	if s.autoIndexer.running {
		s.autoIndexer.mu.Unlock()
		return
	}
	s.autoIndexer.running = true
	s.autoIndexer.mu.Unlock()

	defer func() {
		s.autoIndexer.mu.Lock()
		s.autoIndexer.running = false
		s.autoIndexer.lastRunAt = time.Now().UTC()
		s.autoIndexer.mu.Unlock()
	}()

	chainCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	state, err := s.client.GetState(chainCtx)
	if err != nil {
		s.autoIndexer.mu.Lock()
		s.autoIndexer.lastError = err.Error()
		s.autoIndexer.mu.Unlock()
		return
	}
	latestChain := state.GetLastBlockHeight()

	latestIndexed, err := getLatestIndexedHeight(s.db)
	if err != nil {
		s.autoIndexer.mu.Lock()
		s.autoIndexer.lastError = err.Error()
		s.autoIndexer.lastChainHeight = latestChain
		s.autoIndexer.mu.Unlock()
		return
	}

	if latestChain == 0 || latestIndexed >= latestChain {
		s.autoIndexer.mu.Lock()
		s.autoIndexer.lastError = ""
		s.autoIndexer.lastIndexedHeight = latestIndexed
		s.autoIndexer.lastChainHeight = latestChain
		s.autoIndexer.lastIndexedInBatch = 0
		s.autoIndexer.mu.Unlock()
		return
	}

	from := latestIndexed + 1
	to := from + uint64(s.autoIndexer.maxBlocksPerTick) - 1
	if to > latestChain {
		to = latestChain
	}

	indexed := uint64(0)
	for h := from; h <= to; h++ {
		if err := indexHeight(s.client, s.db, h); err != nil {
			s.autoIndexer.mu.Lock()
			s.autoIndexer.lastError = fmt.Sprintf("height %d: %v", h, err)
			s.autoIndexer.lastIndexedHeight = h - 1
			s.autoIndexer.lastChainHeight = latestChain
			s.autoIndexer.lastIndexedInBatch = indexed
			s.autoIndexer.mu.Unlock()
			return
		}
		indexed++
	}

	newLatest := to
	s.autoIndexer.mu.Lock()
	s.autoIndexer.lastError = ""
	s.autoIndexer.lastIndexedHeight = newLatest
	s.autoIndexer.lastChainHeight = latestChain
	s.autoIndexer.lastIndexedInBatch = indexed
	s.autoIndexer.mu.Unlock()
}

func (s *explorerServer) handleDAProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	relative := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if !strings.HasPrefix(relative, "/da") {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}

	target, err := url.Parse(s.rpcURL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "invalid rpc url"})
		return
	}
	target.Path = path.Join(target.Path, relative)
	target.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func openIndexDB(dbPath string) (*bolt.DB, error) {
	if err := os.MkdirAll(path.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketBlocks)); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketTxs)); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketMeta)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func getLatestIndexedHeight(db *bolt.DB) (uint64, error) {
	var out uint64
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketMeta))
		if b == nil {
			return errors.New("meta bucket missing")
		}
		v := b.Get([]byte(metaLatestIndexedHeight))
		if len(v) == 0 {
			out = 0
			return nil
		}
		parsed, err := strconv.ParseUint(string(v), 10, 64)
		if err != nil {
			return err
		}
		out = parsed
		return nil
	})
	return out, err
}

func indexHeight(client *rpcclient.Client, db *bolt.DB, height uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	resp, err := client.GetBlockByHeight(ctx, height)
	if err != nil {
		return err
	}

	blk, err := blockFromResponse(resp)
	if err != nil {
		return err
	}
	if blk.Height == 0 {
		return fmt.Errorf("block height 0 at requested height=%d", height)
	}

	txs, err := txsFromResponse(resp)
	if err != nil {
		return err
	}

	return db.Update(func(tx *bolt.Tx) error {
		blocks := tx.Bucket([]byte(bucketBlocks))
		txBucket := tx.Bucket([]byte(bucketTxs))
		meta := tx.Bucket([]byte(bucketMeta))
		if blocks == nil || txBucket == nil || meta == nil {
			return errors.New("index buckets missing")
		}

		blkRaw, err := json.Marshal(blk)
		if err != nil {
			return err
		}
		if err := blocks.Put(encodeHeightKey(blk.Height), blkRaw); err != nil {
			return err
		}

		for _, item := range txs {
			raw, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if err := txBucket.Put([]byte(strings.ToLower(item.Hash)), raw); err != nil {
				return err
			}
		}

		if err := meta.Put([]byte(metaLatestIndexedHeight), []byte(strconv.FormatUint(blk.Height, 10))); err != nil {
			return err
		}

		return nil
	})
}

func blockFromResponse(resp *pb.GetBlockResponse) (indexedBlock, error) {
	block := resp.GetBlock()
	if block == nil || block.GetHeader() == nil || block.GetHeader().GetHeader() == nil {
		return indexedBlock{}, errors.New("block/header not available")
	}
	h := block.GetHeader().GetHeader()
	txs := block.GetData().GetTxs()
	hashes := make([]string, 0, len(txs))
	for _, tx := range txs {
		hashes = append(hashes, txHashHex(tx))
	}

	return indexedBlock{
		Height:         h.GetHeight(),
		TimeUnixNano:   h.GetTime(),
		Time:           time.Unix(0, int64(h.GetTime())).UTC().Format(time.RFC3339),
		LastHeaderHash: hex.EncodeToString(h.GetLastHeaderHash()),
		DataHash:       hex.EncodeToString(h.GetDataHash()),
		AppHash:        hex.EncodeToString(h.GetAppHash()),
		NumTxs:         len(txs),
		TxHashes:       hashes,
		HeaderDAHeight: resp.GetHeaderDaHeight(),
		DataDAHeight:   resp.GetDataDaHeight(),
	}, nil
}

func txsFromResponse(resp *pb.GetBlockResponse) ([]indexedTx, error) {
	block := resp.GetBlock()
	if block == nil || block.GetHeader() == nil || block.GetHeader().GetHeader() == nil {
		return nil, errors.New("block/header not available")
	}
	height := block.GetHeader().GetHeader().GetHeight()
	blockTime := time.Unix(0, int64(block.GetHeader().GetHeader().GetTime())).UTC().Format(time.RFC3339)
	txs := block.GetData().GetTxs()

	items := make([]indexedTx, 0, len(txs))
	for i, tx := range txs {
		items = append(items, indexedTx{
			Hash:           txHashHex(tx),
			Height:         height,
			Index:          i,
			Size:           len(tx),
			RawHex:         hex.EncodeToString(tx),
			HeaderDAHeight: resp.GetHeaderDaHeight(),
			DataDAHeight:   resp.GetDataDaHeight(),
			Time:           blockTime,
		})
	}
	return items, nil
}

func getIndexedBlock(db *bolt.DB, height uint64) (indexedBlock, bool, error) {
	var blk indexedBlock
	found := false
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketBlocks))
		if b == nil {
			return errors.New("blocks bucket missing")
		}
		v := b.Get(encodeHeightKey(height))
		if len(v) == 0 {
			return nil
		}
		found = true
		return json.Unmarshal(v, &blk)
	})
	return blk, found, err
}

func getIndexedTx(db *bolt.DB, hash string) (indexedTx, bool, error) {
	var txOut indexedTx
	found := false
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketTxs))
		if b == nil {
			return errors.New("txs bucket missing")
		}
		v := b.Get([]byte(strings.ToLower(hash)))
		if len(v) == 0 {
			return nil
		}
		found = true
		return json.Unmarshal(v, &txOut)
	})
	return txOut, found, err
}

func scanTxByHash(ctx context.Context, client *rpcclient.Client, hash string, depth uint64) (map[string]any, error) {
	target := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(hash), "0x"))
	state, err := client.GetState(ctx)
	if err != nil {
		return nil, err
	}
	latest := state.GetLastBlockHeight()
	if latest == 0 {
		return map[string]any{"found": false, "hash": target, "scanned_from": uint64(0), "scanned_to": uint64(0)}, nil
	}

	lower := uint64(1)
	if depth < latest {
		lower = latest - depth + 1
	}

	for h := latest; h >= lower; h-- {
		resp, err := client.GetBlockByHeight(ctx, h)
		if err != nil {
			return nil, err
		}
		block := resp.GetBlock()
		if block == nil || block.GetData() == nil {
			if h == 1 {
				break
			}
			continue
		}
		for idx, tx := range block.GetData().GetTxs() {
			txHash := txHashHex(tx)
			if txHash == target {
				return map[string]any{
					"found":        true,
					"height":       h,
					"index":        idx,
					"hash":         txHash,
					"size":         len(tx),
					"raw_hex":      hex.EncodeToString(tx),
					"header_da":    resp.GetHeaderDaHeight(),
					"data_da":      resp.GetDataDaHeight(),
					"scanned_from": latest,
					"scanned_to":   lower,
				}, nil
			}
		}
		if h == 1 {
			break
		}
	}

	return map[string]any{
		"found":        false,
		"hash":         target,
		"scanned_from": latest,
		"scanned_to":   lower,
	}, nil
}

func encodeHeightKey(height uint64) []byte {
	k := make([]byte, 8)
	binary.BigEndian.PutUint64(k, height)
	return k
}

func txHashHex(tx []byte) string {
	h := sha256.Sum256(tx)
	return hex.EncodeToString(h[:])
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func getRPCURLFromEnv() string {
	for _, k := range []string{"EVNODE_RPC_URL", "WASM_RPC_URL", "NODE"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return defaultNodeRPCURL
}

func formatTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
