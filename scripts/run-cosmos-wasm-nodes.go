//go:build run_cosmos_wasm

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultSeqExecPort  = 50051
	defaultFullExecPort = 50052

	defaultSeqRPCPort  = 38331
	defaultFullRPCPort = 48331

	defaultSeqP2PPort  = 7860
	defaultFullP2PPort = 7861
)

type nodeConfig struct {
	name         string
	isSequencer  bool
	homeDir      string
	execHomeDir  string
	execGRPCPort int
	rpcPort      int
	p2pPort      int
}

type runConfig struct {
	chainID         string
	cleanOnStart    bool
	cleanOnExit     bool
	logLevel        string
	blockTime       time.Duration
	daAddress       string
	daSubmitAddress string
	daAuthToken     string
	daNamespace     string
	uploadNamespace string
	submitAPI       string
	submitAPIType   string
	submitInterval  time.Duration
	chainLogFile    string
}

type processHandle struct {
	name string
	cmd  *exec.Cmd
}

type nodeManager struct {
	ctx            context.Context
	cancel         context.CancelFunc
	projectRoot    string
	cfg            runConfig
	passphraseFile string
	binariesDir    string
	processes      []processHandle
	nodeDirs       []string
	sequencerPeer  string
	logFile        *os.File
	logMu          sync.Mutex
	nodes          []nodeConfig
	lastBlobHeight uint64
}

func main() {
	cfg := runConfig{}
	flag.StringVar(&cfg.chainID, "chain-id", "cosmos-wasm-local", "Chain ID for evcosmos nodes")
	flag.BoolVar(&cfg.cleanOnStart, "clean-on-start", true, "Remove old node home directories before start")
	flag.BoolVar(&cfg.cleanOnExit, "clean-on-exit", false, "Remove node home directories on exit")
	flag.StringVar(&cfg.logLevel, "log-level", "info", "evcosmos log level")
	flag.DurationVar(&cfg.blockTime, "block-time", 2*time.Second, "evcosmos block time")
	flag.DurationVar(&cfg.submitInterval, "submit-interval", 8*time.Second, "DA submitter interval")
	flag.Parse()

	nm := &nodeManager{
		cfg:       cfg,
		processes: make([]processHandle, 0, 8),
		nodeDirs:  make([]string, 0, 6),
		nodes: []nodeConfig{
			{
				name:         "sequencer",
				isSequencer:  true,
				execGRPCPort: defaultSeqExecPort,
				rpcPort:      defaultSeqRPCPort,
				p2pPort:      defaultSeqP2PPort,
			},
			{
				name:         "fullnode",
				isSequencer:  false,
				execGRPCPort: defaultFullExecPort,
				rpcPort:      defaultFullRPCPort,
				p2pPort:      defaultFullP2PPort,
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	nm.ctx = ctx
	nm.cancel = cancel

	defer nm.cleanup()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received signal %v, shutting down...", sig)
		nm.cancel()
	}()

	if err := nm.run(); err != nil {
		log.Printf("Runner failed: %v", err)
		os.Exit(1)
	}

	<-nm.ctx.Done()
}

func (nm *nodeManager) run() error {
	var err error
	nm.projectRoot, err = findProjectRoot()
	if err != nil {
		return err
	}
	_ = loadDotEnv(filepath.Join(nm.projectRoot, ".env"))
	nm.binariesDir = filepath.Join(nm.projectRoot, "build")
	nm.resolveDAFromEnv()
	if err := nm.validateDAConfig(); err != nil {
		return err
	}
	if err := nm.preflightDA(); err != nil {
		return err
	}

	if err := nm.preparePaths(); err != nil {
		return err
	}

	if err := nm.ensurePortsAvailable(); err != nil {
		return err
	}

	if err := nm.ensureBinaries(); err != nil {
		return err
	}

	if err := nm.initNodes(); err != nil {
		return err
	}

	if err := nm.startExecutionServices(); err != nil {
		return err
	}

	if err := nm.startSequencer(); err != nil {
		return err
	}

	if err := nm.startFullNode(); err != nil {
		return err
	}

	if err := nm.startDASubmitter(); err != nil {
		return err
	}

	if err := nm.waitForChainSync(); err != nil {
		return err
	}

	log.Printf("Cosmos/WASM stack is running")
	log.Printf("- celestia DA endpoint used by nodes: %s", nm.cfg.daAddress)
	log.Printf("- celestia DA submit endpoint: %s", nm.cfg.daSubmitAddress)
	log.Printf("- da namespace used by nodes: %s", nm.cfg.daNamespace)
	log.Printf("- da upload mode: dual (engram + celestia)")
	log.Printf("- da upload namespace: %s", nm.cfg.uploadNamespace)
	log.Printf("- sequencer rpc: http://127.0.0.1:%d", nm.nodes[0].rpcPort)
	log.Printf("- full node rpc: http://127.0.0.1:%d", nm.nodes[1].rpcPort)
	log.Printf("- sequencer execution gRPC: http://127.0.0.1:%d", nm.nodes[0].execGRPCPort)
	log.Printf("- full execution gRPC: http://127.0.0.1:%d", nm.nodes[1].execGRPCPort)
	log.Printf("DA submission can be observed in logs containing 'submit'/'da'")
	nm.logLatestBlobHeightHint()

	return nm.monitorProcesses()
}

func (nm *nodeManager) preparePaths() error {
	tmpDir := filepath.Join(nm.projectRoot, ".cosmos-wasm-runner")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("create runner temp dir: %w", err)
	}

	nm.passphraseFile = filepath.Join(tmpDir, "passphrase.txt")
	if err := os.WriteFile(nm.passphraseFile, []byte("secret\n"), 0o600); err != nil {
		return fmt.Errorf("write passphrase file: %w", err)
	}

	for i := range nm.nodes {
		node := &nm.nodes[i]
		node.homeDir = filepath.Join(nm.projectRoot, fmt.Sprintf(".evcosmos-%s", node.name))
		node.execHomeDir = filepath.Join(nm.projectRoot, fmt.Sprintf(".cosmos-exec-%s", node.name))
		nm.nodeDirs = append(nm.nodeDirs, node.homeDir, node.execHomeDir)
	}

	if nm.cfg.cleanOnStart {
		for _, dir := range nm.nodeDirs {
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("clean dir %s: %w", dir, err)
			}
		}
	}

	for _, dir := range nm.nodeDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}

	logPath := nm.cfg.chainLogFile
	if logPath == "" {
		logPath = filepath.Join(nm.projectRoot, ".logs", "cosmos-wasm-chain.log")
	}
	if !filepath.IsAbs(logPath) {
		logPath = filepath.Join(nm.projectRoot, logPath)
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open chain log file: %w", err)
	}
	nm.logFile = lf
	nm.cfg.chainLogFile = logPath

	return nil
}

func (nm *nodeManager) ensurePortsAvailable() error {
	ports := make([]int, 0, len(nm.nodes)*3)
	for _, node := range nm.nodes {
		ports = append(ports, node.execGRPCPort, node.rpcPort, node.p2pPort)
	}

	for _, port := range ports {
		address := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("required port %d is already in use (%s). stop existing processes before running (hint: pkill -f cosmos-exec-grpc; pkill -f evcosmos)", port, address)
		}
		_ = ln.Close()
	}

	return nil
}

func (nm *nodeManager) ensureBinaries() error {
	type buildTarget struct {
		binPath string
		workDir string
		pkg     string
	}

	targets := []buildTarget{
		{
			binPath: filepath.Join(nm.binariesDir, "evcosmos"),
			workDir: filepath.Join(nm.projectRoot, "apps", "cosmos-wasm"),
			pkg:     ".",
		},
		{
			binPath: filepath.Join(nm.binariesDir, "cosmos-exec-grpc"),
			workDir: filepath.Join(nm.projectRoot, "apps", "cosmos-exec"),
			pkg:     "./cmd/cosmos-exec-grpc",
		},
	}

	if err := os.MkdirAll(nm.binariesDir, 0o755); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}

	for _, target := range targets {
		if _, err := os.Stat(target.binPath); err == nil {
			continue
		}

		log.Printf("Building binary: %s", filepath.Base(target.binPath))
		cmd := exec.CommandContext(nm.ctx, "go", "build", "-o", target.binPath, target.pkg)
		cmd.Dir = target.workDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build %s: %w", target.binPath, err)
		}
	}

	return nil
}

func (nm *nodeManager) initNodes() error {
	evcosmos := filepath.Join(nm.binariesDir, "evcosmos")

	for _, node := range nm.nodes {
		args := []string{
			"init",
			"--home", node.homeDir,
			"--chain_id", nm.cfg.chainID,
			"--evnode.node.aggregator=" + strconv.FormatBool(node.isSequencer),
			"--evnode.da.address", nm.cfg.daAddress,
			"--evnode.da.auth_token", nm.cfg.daAuthToken,
			"--evnode.rpc.address", fmt.Sprintf("127.0.0.1:%d", node.rpcPort),
			"--evnode.p2p.listen_address", fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", node.p2pPort),
			"--evnode.signer.passphrase_file", nm.passphraseFile,
		}
		if err := runCommand(nm.ctx, filepath.Join(nm.projectRoot, "apps", "cosmos-wasm"), evcosmos, args...); err != nil {
			return fmt.Errorf("init %s: %w", node.name, err)
		}
	}

	seqGenesis := filepath.Join(nm.nodes[0].homeDir, "config", "genesis.json")
	fullGenesis := filepath.Join(nm.nodes[1].homeDir, "config", "genesis.json")
	if err := copyFile(seqGenesis, fullGenesis); err != nil {
		return fmt.Errorf("copy genesis to full node: %w", err)
	}

	return nil
}

func (nm *nodeManager) startExecutionServices() error {
	binary := filepath.Join(nm.binariesDir, "cosmos-exec-grpc")

	for _, node := range nm.nodes {
		args := []string{
			"--address", fmt.Sprintf("127.0.0.1:%d", node.execGRPCPort),
			"--home", node.execHomeDir,
		}
		cmd := exec.CommandContext(nm.ctx, binary, args...)
		if err := nm.startProcess("cosmos-exec-grpc-"+node.name, cmd); err != nil {
			return err
		}
		addr := fmt.Sprintf("127.0.0.1:%d", node.execGRPCPort)
		if err := waitForTCP(addr, 20*time.Second); err != nil {
			return fmt.Errorf("execution service not reachable for %s: %w", node.name, err)
		}
		// Extra wait to ensure gRPC handler is fully initialized
		if err := waitForGRPCHealthy(addr, 10*time.Second); err != nil {
			return fmt.Errorf("execution service gRPC not ready for %s: %w", node.name, err)
		}
	}

	return nil
}

func (nm *nodeManager) startSequencer() error {
	node := nm.nodes[0]
	args := []string{
		"start",
		"--home", node.homeDir,
		"--grpc-executor-url", fmt.Sprintf("http://127.0.0.1:%d", node.execGRPCPort),
		"--evnode.node.aggregator=true",
		"--evnode.da.address", nm.cfg.daAddress,
		"--evnode.da.auth_token", nm.cfg.daAuthToken,
		"--evnode.da.namespace", nm.cfg.daNamespace,
		"--evnode.rpc.address", fmt.Sprintf("127.0.0.1:%d", node.rpcPort),
		"--evnode.p2p.listen_address", fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", node.p2pPort),
		"--evnode.signer.passphrase_file", nm.passphraseFile,
		"--evnode.node.block_time", nm.cfg.blockTime.String(),
		"--evnode.log.level", nm.cfg.logLevel,
	}

	cmd := exec.CommandContext(nm.ctx, filepath.Join(nm.binariesDir, "evcosmos"), args...)
	cmd.Dir = filepath.Join(nm.projectRoot, "apps", "cosmos-wasm")
	if err := nm.startProcess("evcosmos-sequencer", cmd); err != nil {
		return err
	}

	if err := waitForHTTPStatus(fmt.Sprintf("http://127.0.0.1:%d/status", node.rpcPort), 45*time.Second); err != nil {
		return fmt.Errorf("sequencer rpc not ready: %w", err)
	}

	peer, err := nm.getNodePeerAddress(node.homeDir)
	if err != nil {
		return err
	}
	nm.sequencerPeer = peer
	log.Printf("Sequencer peer address: %s", nm.sequencerPeer)

	return nil
}

func (nm *nodeManager) startFullNode() error {
	node := nm.nodes[1]
	args := []string{
		"start",
		"--home", node.homeDir,
		"--grpc-executor-url", fmt.Sprintf("http://127.0.0.1:%d", node.execGRPCPort),
		"--evnode.node.aggregator=false",
		"--evnode.da.address", nm.cfg.daAddress,
		"--evnode.da.auth_token", nm.cfg.daAuthToken,
		"--evnode.da.namespace", nm.cfg.daNamespace,
		"--evnode.rpc.address", fmt.Sprintf("127.0.0.1:%d", node.rpcPort),
		"--evnode.p2p.listen_address", fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", node.p2pPort),
		"--evnode.p2p.peers", nm.sequencerPeer,
		"--evnode.node.block_time", nm.cfg.blockTime.String(),
		"--evnode.log.level", nm.cfg.logLevel,
	}

	cmd := exec.CommandContext(nm.ctx, filepath.Join(nm.binariesDir, "evcosmos"), args...)
	cmd.Dir = filepath.Join(nm.projectRoot, "apps", "cosmos-wasm")
	if err := nm.startProcess("evcosmos-fullnode", cmd); err != nil {
		return err
	}

	if err := waitForHTTPStatus(fmt.Sprintf("http://127.0.0.1:%d/status", node.rpcPort), 60*time.Second); err != nil {
		return fmt.Errorf("full node rpc not ready: %w", err)
	}

	return nil
}

func (nm *nodeManager) startDASubmitter() error {
	argsEngram := []string{
		"run", "./tools/cosmos-da-submit",
		"--namespace", nm.cfg.uploadNamespace,
		"--chain-log-file", nm.cfg.chainLogFile,
		"--interval", nm.cfg.submitInterval.String(),
		"--chain", "cosmos-wasm",
		"--submit-api", nm.cfg.submitAPI,
		"--api-type", nm.cfg.submitAPIType,
	}
	cmdEngram := exec.CommandContext(nm.ctx, "go", argsEngram...)
	cmdEngram.Dir = nm.projectRoot
	if err := nm.startProcess("cosmos-da-submit-engram", cmdEngram); err != nil {
		return fmt.Errorf("start cosmos-da-submit-engram: %w", err)
	}

	argsCelestia := []string{
		"run", "./tools/cosmos-da-submit",
		"--namespace", nm.cfg.daNamespace,
		"--chain-log-file", nm.cfg.chainLogFile,
		"--interval", nm.cfg.submitInterval.String(),
		"--chain", "cosmos-wasm",
		"--da-url", nm.cfg.daSubmitAddress,
		"--da-fallback-url", nm.cfg.daAddress,
		"--auth-token", nm.cfg.daAuthToken,
	}
	cmdCelestia := exec.CommandContext(nm.ctx, "go", argsCelestia...)
	cmdCelestia.Dir = nm.projectRoot
	if err := nm.startProcess("cosmos-da-submit-celestia", cmdCelestia); err != nil {
		return fmt.Errorf("start cosmos-da-submit-celestia: %w", err)
	}

	return nil
}

func (nm *nodeManager) waitForChainSync() error {
	seqURL := fmt.Sprintf("http://127.0.0.1:%d/status", nm.nodes[0].rpcPort)
	fullURL := fmt.Sprintf("http://127.0.0.1:%d/status", nm.nodes[1].rpcPort)

	deadline := time.Now().Add(5 * time.Minute) // 5 minutes for initial sync
	for time.Now().Before(deadline) {
		seqHeight, err1 := fetchLatestHeight(seqURL)
		fullHeight, err2 := fetchLatestHeight(fullURL)
		if err1 == nil && err2 == nil {
			if seqHeight > 0 && fullHeight > 0 && fullHeight <= seqHeight && seqHeight-fullHeight <= 10 {
				log.Printf("Sync check OK: sequencer=%d fullnode=%d", seqHeight, fullHeight)
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}

	return errors.New("full node did not reach sync window in time")
}

func (nm *nodeManager) getNodePeerAddress(home string) (string, error) {
	evcosmos := filepath.Join(nm.binariesDir, "evcosmos")
	output, err := runCommandOutput(nm.ctx, filepath.Join(nm.projectRoot, "apps", "cosmos-wasm"), evcosmos,
		"net-info", "--home", home,
	)
	if err != nil {
		return "", fmt.Errorf("get net-info: %w", err)
	}

	re := regexp.MustCompile(`/ip4/[^\s]+/tcp/\d+/p2p/[A-Za-z0-9]+`)
	match := re.FindString(string(output))
	if match == "" {
		return "", fmt.Errorf("could not parse peer address from net-info output")
	}

	return match, nil
}

func (nm *nodeManager) monitorProcesses() error {
	errCh := make(chan error, len(nm.processes))
	for _, p := range nm.processes {
		proc := p
		go func() {
			err := proc.cmd.Wait()
			if err != nil && !errors.Is(err, context.Canceled) {
				errCh <- fmt.Errorf("process %s exited: %w", proc.name, err)
				return
			}
			errCh <- nil
		}()
	}

	for {
		select {
		case <-nm.ctx.Done():
			return nil
		case err := <-errCh:
			if err != nil {
				nm.cancel()
				return err
			}
		}
	}
}

func (nm *nodeManager) validateDAConfig() error {
	if nm.cfg.daAddress == "" {
		return errors.New("node DA endpoint is empty: set DA_BRIDGE_RPC or DA_RPC in .env")
	}
	if nm.cfg.daSubmitAddress == "" {
		return errors.New("celestia submit endpoint is empty: set DA_RPC or DA_BRIDGE_RPC in .env")
	}

	if nm.cfg.daNamespace == "" {
		return errors.New("DA namespace for nodes is empty")
	}

	if nm.cfg.uploadNamespace == "" {
		return errors.New("DA upload namespace is empty")
	}
	if nm.cfg.submitAPI == "" {
		return errors.New("engram submit endpoint is empty: set COSMOS_DA_SUBMIT_API or ENGRAM_API_BASE")
	}

	return nil
}

func (nm *nodeManager) preflightDA() error {
	payload := `{"jsonrpc":"2.0","id":1,"method":"blob.GetAll","params":[1,["AAAAAAAAAAAAAAAAAAAAAAAAAAECAwQFBgcICRA="]]}`
	req, err := http.NewRequestWithContext(nm.ctx, http.MethodPost, nm.cfg.daAddress, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build DA preflight request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(nm.cfg.daAuthToken) != "" {
		req.Header.Set("Authorization", "Bearer "+nm.cfg.daAuthToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("DA preflight request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyText := strings.ToLower(string(body))

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("DA preflight unauthorized (401): DA_AUTH_TOKEN is invalid/expired for %s", nm.cfg.daAddress)
	}

	if strings.Contains(bodyText, "missing permission") || strings.Contains(bodyText, "need 'read'") {
		return fmt.Errorf("DA preflight permission denied: token is missing or lacks read permission for %s", nm.cfg.daAddress)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("DA preflight failed with status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	log.Printf("DA preflight OK: endpoint=%s", nm.cfg.daAddress)
	return nil
}

func (nm *nodeManager) startProcess(name string, cmd *exec.Cmd) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe for %s: %w", name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe for %s: %w", name, err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	nm.processes = append(nm.processes, processHandle{name: name, cmd: cmd})
	nm.streamLogs(name, stdout)
	nm.streamLogs(name, stderr)
	log.Printf("Started process: %s (pid=%d)", name, cmd.Process.Pid)

	return nil
}

func (nm *nodeManager) cleanup() {
	nm.cancel()

	for _, process := range nm.processes {
		if process.cmd == nil || process.cmd.Process == nil {
			continue
		}

		_ = process.cmd.Process.Signal(syscall.SIGTERM)
	}

	time.Sleep(800 * time.Millisecond)

	for _, process := range nm.processes {
		if process.cmd == nil || process.cmd.Process == nil {
			continue
		}
		if process.cmd.ProcessState == nil || !process.cmd.ProcessState.Exited() {
			_ = process.cmd.Process.Kill()
		}
	}

	if nm.cfg.cleanOnExit {
		for _, dir := range nm.nodeDirs {
			if err := os.RemoveAll(dir); err != nil {
				log.Printf("Failed to remove %s: %v", dir, err)
			}
		}
	}

	if nm.passphraseFile != "" {
		_ = os.Remove(nm.passphraseFile)
	}

	if nm.logFile != nil {
		_ = nm.logFile.Close()
	}
}

func (nm *nodeManager) streamLogs(name string, reader io.Reader) {
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			formatted := fmt.Sprintf("[%s] %s", name, line)
			nm.writeLogLine(formatted)
			nm.emitBlobHeightHint(name, line)
		}
	}()
}

func (nm *nodeManager) writeLogLine(line string) {
	log.Print(line)
	nm.logMu.Lock()
	defer nm.logMu.Unlock()
	if nm.logFile != nil {
		_, _ = nm.logFile.WriteString(time.Now().Format(time.RFC3339) + " " + line + "\n")
	}
}

func (nm *nodeManager) emitBlobHeightHint(source, line string) {
	if strings.Contains(line, "engram_submit") && !strings.Contains(line, "da_height=") {
		nm.writeLogLine("[runner][blob-height] engram_submit acknowledged (status=200) but DA blob height is not provided by this API")
		return
	}

	h, ok := extractBlobHeight(line)
	if !ok || h == 0 {
		return
	}

	nm.logMu.Lock()
	if h == nm.lastBlobHeight {
		nm.logMu.Unlock()
		return
	}
	nm.lastBlobHeight = h
	nm.logMu.Unlock()

	nm.writeLogLine(fmt.Sprintf("[runner][blob-height] blob_height=%d source=%s", h, source))
}

func extractBlobHeight(line string) (uint64, bool) {
	re := regexp.MustCompile(`(?i)(?:blob_height|data_da_height|header_da_height|da_height)[=:\s]+([0-9]+)`)
	matches := re.FindStringSubmatch(line)
	if len(matches) < 2 {
		return 0, false
	}

	h, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return 0, false
	}

	return h, true
}

func (nm *nodeManager) logLatestBlobHeightHint() {
	seqURL := fmt.Sprintf("http://127.0.0.1:%d", nm.nodes[0].rpcPort)

	type latestBlockResponse struct {
		HeaderDAHeight uint64 `json:"header_da_height"`
		DataDAHeight   uint64 `json:"data_da_height"`
		Height         uint64 `json:"height"`
	}

	out, err := runCommandOutput(nm.ctx, nm.projectRoot, "go", "run", "./tools/evnode-rpc", "latest-block")
	if err != nil {
		log.Printf("[runner][blob-height] unable to query latest block DA height from %s: %v", seqURL, err)
		return
	}

	var resp latestBlockResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		log.Printf("[runner][blob-height] unable to parse latest block response from %s: %v", seqURL, err)
		return
	}

	blobHeight := resp.DataDAHeight
	if blobHeight == 0 {
		blobHeight = resp.HeaderDAHeight
	}
	if blobHeight == 0 {
		log.Printf("[runner][blob-height] latest block has no DA height yet (height=%d)", resp.Height)
		return
	}

	nm.emitBlobHeightHint("latest-block", fmt.Sprintf("blob_height=%d", blobHeight))
	log.Printf("[runner][blob-height] tip: ./scripts/query_celestia_blob.sh --height %d", blobHeight)
}

func (nm *nodeManager) resolveDAFromEnv() {
	bridgeRPC := firstNonEmpty(os.Getenv("DA_BRIDGE_RPC"), os.Getenv("DA_RPC"))
	submitRPC := firstNonEmpty(os.Getenv("DA_BRIDGE_RPC"), os.Getenv("DA_RPC"))
	nm.cfg.daAuthToken = os.Getenv("DA_AUTH_TOKEN")
	nm.cfg.daNamespace = firstNonEmpty(os.Getenv("DA_NAMESPACE"), "rollup")
	nm.cfg.uploadNamespace = firstNonEmpty(os.Getenv("ENGRAM_NAMESPACE"), os.Getenv("DA_NAMESPACE"), "rollup")
	nm.cfg.submitAPI = firstNonEmpty(os.Getenv("COSMOS_DA_SUBMIT_API"), submitAPIFromBase(os.Getenv("ENGRAM_API_BASE")))
	nm.cfg.submitAPIType = firstNonEmpty(os.Getenv("COSMOS_DA_SUBMIT_API_TYPE"), "engram")
	nm.cfg.chainLogFile = os.Getenv("CHAIN_LOG_FILE")
	nm.cfg.daAddress = bridgeRPC
	nm.cfg.daSubmitAddress = submitRPC
}

func loadDotEnv(path string) error {
	bz, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	lines := strings.Split(string(bz), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}

	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func submitAPIFromBase(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/data/submit-tx"
}

func runCommand(ctx context.Context, dir, binary string, args ...string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommandOutput(ctx context.Context, dir, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func copyFile(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer df.Close()

	_, err = io.Copy(df, sf)
	return err
}

func waitForTCP(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 800*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", address)
}

func waitForGRPCHealthy(address string, timeout time.Duration) error {
	// Verify gRPC server is accepting connections and handler is initialized
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 1*time.Second)
		if err == nil {
			conn.Close()
			// Extra small sleep to ensure handler initialization
			time.Sleep(100 * time.Millisecond)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for gRPC healthy on %s", address)
}

func waitForHTTPStatus(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func fetchLatestHeight(statusURL string) (int64, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(statusURL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	re := regexp.MustCompile(`"latest_block_height"\s*:\s*"?(\d+)"?`)
	match := re.FindSubmatch(body)
	if len(match) != 2 {
		return 0, fmt.Errorf("latest_block_height not found")
	}

	height, err := strconv.ParseInt(string(match[1]), 10, 64)
	if err != nil {
		return 0, err
	}

	return height, nil
}

func findProjectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	current := wd
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(current, "apps")); err == nil {
				return current, nil
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("project root not found")
		}
		current = parent
	}
}
