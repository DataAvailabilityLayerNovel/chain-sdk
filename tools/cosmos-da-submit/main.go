package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	libshare "github.com/celestiaorg/go-square/v3/share"

	dajsonrpc "github.com/evstack/ev-node/pkg/da/jsonrpc"
	datypes "github.com/evstack/ev-node/pkg/da/types"
)

type submitPayload struct {
	Chain     string `json:"chain"`
	Sequence  uint64 `json:"sequence"`
	Timestamp string `json:"timestamp"`
}

// Engram API types
type EngramBlobMetadata struct {
	Height     int64  `json:"height"`
	Index      int    `json:"index"`
	Namespace  string `json:"namespace"`
	Commitment string `json:"commitment"`
	Data       string `json:"data"`
	Signer     string `json:"signer"`
	Size       int    `json:"size"`
	Timestamp  int64  `json:"timestamp"`
}

type EngramSubmitRequest struct {
	Height    int64                `json:"height"`
	Timestamp int64                `json:"timestamp"`
	Index     int                  `json:"index"`
	Hash      string               `json:"hash"`
	Count     int                  `json:"count"`
	Blobs     []EngramBlobMetadata `json:"blobs"`
}

func main() {
	var (
		daURL         string
		daFallbackURL string
		authToken     string
		namespace     string
		chainLogFile  string
		interval      time.Duration
		submitTimeout time.Duration
		chainName     string
		gasPrice      float64
		submitAPI     string
		apiType       string
		httpClient    = &http.Client{Timeout: 12 * time.Second}
	)

	flag.StringVar(&daURL, "da-url", "", "DA bridge RPC URL")
	flag.StringVar(&daFallbackURL, "da-fallback-url", "", "Fallback DA RPC URL for retry when primary fails")
	flag.StringVar(&authToken, "auth-token", "", "DA auth token")
	flag.StringVar(&submitAPI, "submit-api", "", "HTTP submit API endpoint")
	flag.StringVar(&apiType, "api-type", "simple", "API type: 'simple' (simple blob) or 'engram' (Engram format)")
	flag.StringVar(&namespace, "namespace", "rollup", "DA namespace (hex or string)")
	flag.StringVar(&chainLogFile, "chain-log-file", ".logs/cosmos-chain.log", "path to cosmos chain log file to extract real chain data")
	flag.DurationVar(&interval, "interval", 8*time.Second, "submit interval")
	flag.DurationVar(&submitTimeout, "submit-timeout", 20*time.Second, "timeout for a single DA blob submit attempt")
	flag.StringVar(&chainName, "chain", "cosmos-exec", "chain name label in submitted payload")
	flag.Float64Var(&gasPrice, "gas-price", 0, "DA gas price")
	flag.Parse()

	if submitAPI == "" && daURL == "" {
		fmt.Fprintln(os.Stderr, "[err][da-submitter] one of --submit-api or --da-url is required")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var (
		client         *dajsonrpc.Client
		fallbackClient *dajsonrpc.Client
		ns             libshare.Namespace
		err            error
	)

	if submitAPI == "" {
		ns, err = parseNamespace(namespace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[err][da-submitter] invalid namespace: %v\n", err)
			os.Exit(1)
		}

		client, err = dajsonrpc.NewClient(ctx, daURL, authToken, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[err][da-submitter] connect DA client failed: %v\n", err)
			os.Exit(1)
		}
		defer client.Close()
		fmt.Printf("[run][da-submitter] rpc mode da_url=%s namespace=%s interval=%s\n", daURL, ns.String(), interval)

		if strings.TrimSpace(daFallbackURL) != "" && strings.TrimSpace(daFallbackURL) != strings.TrimSpace(daURL) {
			fallbackClient, err = dajsonrpc.NewClient(ctx, daFallbackURL, authToken, "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "[wrn][da-submitter] connect fallback DA client failed: %v\n", err)
			} else {
				defer fallbackClient.Close()
				fmt.Printf("[run][da-submitter] rpc fallback enabled da_fallback_url=%s\n", daFallbackURL)
			}
		}
	} else {
		if apiType == "engram" {
			fmt.Printf("[run][da-submitter] engram mode submit_api=%s namespace=%s interval=%s\n", submitAPI, namespace, interval)
		} else {
			namespace = normalizeNamespaceForSubmitAPI(namespace)
			fmt.Printf("[run][da-submitter] http mode submit_api=%s namespace=%s interval=%s\n", submitAPI, namespace, interval)
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var seq uint64
	for {
		if submitAPI != "" {
			if apiType == "engram" {
				err = submitOnceEngram(ctx, httpClient, submitAPI, namespace, chainName, chainLogFile, seq)
			} else {
				err = submitOnceHTTP(ctx, httpClient, submitAPI, namespace, chainName, seq)
			}
		} else {
			err = submitOnceWithFallback(ctx, client, fallbackClient, ns, chainName, seq, gasPrice, submitTimeout)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "[err][da-submitter] submit failed seq=%d err=%v\n", seq, err)
		}
		seq++

		select {
		case <-ctx.Done():
			fmt.Println("[done][da-submitter] stopping")
			return
		case <-ticker.C:
		}
	}
}

func submitOnceWithFallback(
	ctx context.Context,
	primary *dajsonrpc.Client,
	fallback *dajsonrpc.Client,
	ns libshare.Namespace,
	chainName string,
	seq uint64,
	gasPrice float64,
	timeout time.Duration,
) error {
	err := submitOnce(ctx, primary, ns, chainName, seq, gasPrice, timeout)
	if err == nil {
		return nil
	}

	if fallback == nil || !shouldRetryOnFallback(err) {
		return err
	}

	fmt.Printf("[wrn][da-submitter] primary submit failed seq=%d err=%v; retrying fallback RPC\n", seq, err)
	fallbackErr := submitOnce(ctx, fallback, ns, chainName, seq, gasPrice, timeout)
	if fallbackErr == nil {
		fmt.Printf("[ok][da-submitter] fallback submit succeeded seq=%d\n", seq)
		return nil
	}

	return fmt.Errorf("primary: %v; fallback: %w", err, fallbackErr)
}

func shouldRetryOnFallback(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	patterns := []string{
		"method not found",
		"-32601",
		"context deadline exceeded",
		"connection refused",
		"no route to host",
		"transport: error while dialing",
		"sendrequest failed",
		"rpc client error",
	}

	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}

	return false
}

type submitAPIRequest struct {
	Namespace string `json:"namespace"`
	Data      string `json:"data"`
}

func submitOnceHTTP(ctx context.Context, httpClient *http.Client, endpoint, namespace, chainName string, seq uint64) error {
	requestBody := submitAPIRequest{
		Namespace: namespace,
		Data:      fmt.Sprintf("cosmos-exec seq=%d chain=%s time=%s", seq, chainName, time.Now().UTC().Format(time.RFC3339Nano)),
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal http payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post submit api: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("submit api status=%d body=%s", resp.StatusCode, string(respBody))
	}

	fmt.Printf("[ok][da-submitter] http_submit seq=%d status=%d body=%s\n", seq, resp.StatusCode, string(respBody))
	return nil
}

func submitOnceEngram(ctx context.Context, httpClient *http.Client, endpoint, namespace, chainName, chainLogFile string, seq uint64) error {
	now := time.Now().UTC()
	nowUnix := now.Unix()

	chainData, chainHeight, err := buildChainData(chainName, seq, chainLogFile, now)
	if err != nil {
		return fmt.Errorf("collect chain data: %w", err)
	}

	blobHash := sha256.Sum256(chainData)
	hashHex := "0x" + hex.EncodeToString(blobHash[:])
	commitSeed := append([]byte(namespace+":"), chainData...)
	commitHash := sha256.Sum256(commitSeed)
	commitment := "0x" + hex.EncodeToString(commitHash[:])

	requestBody := EngramSubmitRequest{
		Height:    chainHeight,
		Timestamp: nowUnix,
		Index:     0,
		Hash:      hashHex,
		Count:     1,
		Blobs: []EngramBlobMetadata{{
			Height:     chainHeight,
			Index:      0,
			Namespace:  namespace,
			Commitment: commitment,
			Data:       string(chainData),
			Signer:     "0x0000000000000000000000000000000000000000",
			Size:       len(chainData),
			Timestamp:  nowUnix,
		}},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal engram payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build engram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post engram api: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("engram api status=%d body=%s", resp.StatusCode, string(respBody))
	}

	fmt.Printf("[ok][da-submitter] engram_submit seq=%d status=%d chain_height=%d hash=%s\n", seq, resp.StatusCode, chainHeight, hashHex)
	return nil
}

type chainEvidence struct {
	Chain      string `json:"chain"`
	Sequence   uint64 `json:"sequence"`
	ObservedAt string `json:"observed_at"`
	ObservedTS int64  `json:"observed_ts"`
	Source     string `json:"source"`
	Line       string `json:"line"`
	Height     int64  `json:"height"`
}

func buildChainData(chainName string, seq uint64, chainLogFile string, now time.Time) ([]byte, int64, error) {
	line, height, err := latestChainEvidenceLine(chainLogFile)
	if err != nil {
		return nil, 0, err
	}

	payload := chainEvidence{
		Chain:      chainName,
		Sequence:   seq,
		ObservedAt: now.Format(time.RFC3339Nano),
		ObservedTS: now.Unix(),
		Source:     "cosmos-chain-log",
		Line:       line,
		Height:     height,
	}

	bz, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal chain evidence: %w", err)
	}

	return bz, height, nil
}

func latestChainEvidenceLine(path string) (string, int64, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("read chain log file %s: %w", path, err)
	}

	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 {
		return "", 0, fmt.Errorf("chain log file is empty: %s", path)
	}

	evidencePattern := regexp.MustCompile(`(?i)block|height|finalize|commit`)
	excludePattern := regexp.MustCompile(`(?i)da-submitter|engram_submit|http_submit|\[da\]`)
	var fallbackLine string

	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if excludePattern.MatchString(line) {
			continue
		}
		if fallbackLine == "" {
			fallbackLine = line
		}
		if !evidencePattern.MatchString(line) {
			continue
		}
		height := parseHeightFromLine(line)
		if height > 0 {
			return line, height, nil
		}
	}

	if fallbackLine != "" {
		return fallbackLine, 0, nil
	}

	return "", 0, fmt.Errorf("no chain runtime line found in %s", path)
}

func parseHeightFromLine(line string) int64 {
	re := regexp.MustCompile(`(?i)height[=:\s]+([0-9]+)`)
	matches := re.FindStringSubmatch(line)
	if len(matches) >= 2 {
		if h, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			return h
		}
	}

	re2 := regexp.MustCompile(`(?i)block[=:\s]+([0-9]+)`)
	matches2 := re2.FindStringSubmatch(line)
	if len(matches2) >= 2 {
		if h, err := strconv.ParseInt(matches2[1], 10, 64); err == nil {
			return h
		}
	}

	return 0
}

func submitOnce(
	ctx context.Context,
	client *dajsonrpc.Client,
	ns libshare.Namespace,
	chainName string,
	seq uint64,
	gasPrice float64,
	timeout time.Duration,
) error {
	payload := submitPayload{
		Chain:     chainName,
		Sequence:  seq,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	blob, err := dajsonrpc.NewBlobV0(ns, payloadBytes)
	if err != nil {
		return fmt.Errorf("build blob: %w", err)
	}

	opts := &dajsonrpc.SubmitOptions{}
	if gasPrice > 0 {
		opts.GasPrice = gasPrice
		opts.IsGasPriceSet = true
	}

	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	submitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	daHeight, err := client.Blob.Submit(submitCtx, []*dajsonrpc.Blob{blob}, opts)
	if err != nil {
		return fmt.Errorf("blob submit rpc: %w", err)
	}

	fmt.Printf("[ok][da-submitter] seq=%d da_height=%d blob_size=%d\n", seq, daHeight, len(payloadBytes))
	return nil
}

func parseNamespace(raw string) (libshare.Namespace, error) {
	if raw == "" {
		raw = "rollup"
	}

	raw = strings.TrimSpace(raw)

	if decodedHex, err := hex.DecodeString(strings.TrimPrefix(raw, "0x")); err == nil && len(decodedHex) > 0 {
		if len(decodedHex) > datypes.NamespaceSize {
			return libshare.Namespace{}, fmt.Errorf("hex namespace too long: got %d bytes, max %d", len(decodedHex), datypes.NamespaceSize)
		}

		if len(decodedHex) < datypes.NamespaceSize {
			padded := make([]byte, datypes.NamespaceSize)
			copy(padded[datypes.NamespaceSize-len(decodedHex):], decodedHex)
			decodedHex = padded
		}

		ns, err := libshare.NewNamespaceFromBytes(decodedHex)
		if err == nil {
			return ns, nil
		}

		if nsHex, parseErr := datypes.ParseHexNamespace(raw); parseErr == nil {
			return libshare.NewNamespaceFromBytes(nsHex.Bytes())
		}
		return libshare.Namespace{}, fmt.Errorf("invalid hex namespace: %w", err)
	}

	textBytes := []byte(raw)
	if len(textBytes) > 0 && len(textBytes) <= datypes.NamespaceSize {
		padded := make([]byte, datypes.NamespaceSize)
		copy(padded[datypes.NamespaceSize-len(textBytes):], textBytes)
		if ns, err := libshare.NewNamespaceFromBytes(padded); err == nil {
			return ns, nil
		}
	}

	ns := datypes.NamespaceFromString(raw)
	return libshare.NewNamespaceFromBytes(ns.Bytes())
}

func normalizeNamespaceForSubmitAPI(raw string) string {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "0x"))
	if raw == "" {
		return "726F6C6C7570"
	}

	hexPattern := regexp.MustCompile(`^[0-9a-fA-F]+$`)
	if hexPattern.MatchString(raw) {
		hexFull := strings.ToLower(raw)
		if len(hexFull)%2 == 1 {
			hexFull = "0" + hexFull
		}
		if len(hexFull) > 20 {
			return hexFull[len(hexFull)-20:]
		}
		return hexFull
	}

	rawBytes := []byte(raw)
	if len(rawBytes) > 10 {
		rawBytes = rawBytes[:10]
	}
	return fmt.Sprintf("%x", rawBytes)
}
