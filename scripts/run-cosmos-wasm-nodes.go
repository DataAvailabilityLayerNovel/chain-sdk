//go:build run_cosmos_wasm

// run-cosmos-wasm-nodes là orchestrator dev/local: dựng full stack rollup
// (sequencer + full node + 2 execution service gRPC) trên cùng host và đẩy
// log ra cả stdout lẫn .logs/cosmos-wasm-chain.log.
//
// Build tag `run_cosmos_wasm` đảm bảo file không lẫn vào `go build ./...`
// — chỉ chạy qua: `go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go`.
//
// Trình tự khởi tạo (run()):
//   1. findProjectRoot      — đi ngược tìm thư mục có go.mod + apps/
//   2. loadDotEnv           — đọc .env (DA endpoint, namespace, token...)
//   3. resolveDAFromEnv     — map env → runConfig
//   4. validateDAConfig     — bắt buộc có DA endpoint & namespace
//   5. preflightDA          — POST blob.GetAll thử để fail-fast nếu DA chết/sai token
//   6. preparePaths         — tạo home dir, passphrase, log file (+ clean nếu cần)
//   7. ensurePortsAvailable — kiểm tra 6 cổng (2 nodes × {gRPC, RPC, P2P}) còn rảnh
//   8. ensureBinaries       — go build evcosmos + cosmos-exec-grpc (incremental)
//   9. initNodes            — `evcosmos init` cho mỗi node + copy genesis từ seq sang full
//  10. startExecutionServices — bật 2 cosmos-exec-grpc, chờ TCP + grace gRPC handler
//  11. startSequencer       — bật evcosmos aggregator, lấy peer addr qua `net-info`
//  12. startFullNode        — bật evcosmos non-aggregator, peer = sequencer
//  13. waitForChainSync     — 5 phút chờ full node đuổi kịp seq (≤10 block); chỉ warn nếu fail
//  14. monitorProcesses     — block đến khi 1 process chết hoặc nhận SIGINT/SIGTERM
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

// Port mặc định cho 2 node trên cùng host. Chọn lệch dải để tránh đụng các
// service khác trong repo (testapp RPC 7331, evm-nodes...). Đổi ở đây thì
// phải khớp với các doc và devchain helper trong sdk/cosmoswasm/internal/devchain.
const (
	defaultSeqExecPort  = 50051 // cosmos-exec-grpc của sequencer (gRPC executor URL)
	defaultFullExecPort = 50052 // cosmos-exec-grpc của full node

	defaultSeqRPCPort  = 38331 // evcosmos JSON-RPC sequencer (sequencer truy vấn /status)
	defaultFullRPCPort = 48331 // evcosmos JSON-RPC full node

	defaultSeqP2PPort  = 7860 // libp2p sequencer (full node sẽ dial vào đây)
	defaultFullP2PPort = 7861 // libp2p full node
)

// nodeConfig: 1 entry/node. homeDir là `evcosmos` home, execHomeDir là `cosmos-exec-grpc` home.
// Tách 2 home dir vì state tier (chain state, mempool) và execution tier (wasm vm, kvstore)
// sống độc lập — clean 1 bên không động bên kia.
type nodeConfig struct {
	name         string
	isSequencer  bool
	homeDir      string
	execHomeDir  string
	execGRPCPort int
	rpcPort      int
	p2pPort      int
}

// runConfig: gom flag CLI + env vào 1 struct, không truyền lẻ tẻ qua các method.
// Một số field (daSubmitAddress, uploadNamespace, submitAPI, submitInterval) hiện chỉ
// dùng để log/diagnostic — DA submission thật do evnode runtime tự xử lý qua --evnode.da.*.
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

// nodeManager là singleton điều phối toàn stack. ctx/cancel propagate xuống mọi
// exec.CommandContext nên khi nhận SIGINT thì tất cả child process tự nhận
// context cancel — cleanup() chỉ còn gửi SIGTERM phòng trường hợp process bướng.
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

	if err := nm.waitForChainSync(); err != nil {
		return err
	}

	log.Printf("Cosmos/WASM stack is running")
	log.Printf("- celestia DA endpoint used by nodes: %s", nm.cfg.daAddress)
	log.Printf("- da namespace used by nodes: %s", nm.cfg.daNamespace)
	log.Printf("- da submission path: evnode runtime (aggregator)")
	log.Printf("- sequencer rpc: http://127.0.0.1:%d", nm.nodes[0].rpcPort)
	log.Printf("- full node rpc: http://127.0.0.1:%d", nm.nodes[1].rpcPort)
	log.Printf("- sequencer execution gRPC: http://127.0.0.1:%d", nm.nodes[0].execGRPCPort)
	log.Printf("- full execution gRPC: http://127.0.0.1:%d", nm.nodes[1].execGRPCPort)
	log.Printf("DA submission can be observed in evcosmos logs containing 'da_submitter'/'da_height'")
	nm.logLatestBlobHeightHint()

	return nm.monitorProcesses()
}

// preparePaths: dựng home dir cho 2 node + 2 exec service, file passphrase chung,
// log file append-only. Nếu cleanOnStart thì xóa toàn bộ state cũ trước — bắt buộc
// khi đổi chain_id, genesis, hoặc bật A+B treasury (xem docs/fee-economics.md mục 6b).
func (nm *nodeManager) preparePaths() error {
	tmpDir := filepath.Join(nm.projectRoot, ".cosmos-wasm-runner")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("create runner temp dir: %w", err)
	}

	// Passphrase dev cố định "secret" — chỉ dùng local, KHÔNG đem lên môi trường thật.
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

// ensurePortsAvailable: fail-fast preflight. Listen+close từng port để biết có
// process khác (vd lần chạy trước chưa kill sạch) đang giữ — báo lỗi ngay thay vì
// để evcosmos/cosmos-exec-grpc crash giữa chừng với log khó đọc.
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

// ensureBinaries: build evcosmos + cosmos-exec-grpc vào ./build/. Luôn gọi
// `go build` để pick up source change; Go's incremental build làm trường hợp
// no-op gần như free.
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
		// Always invoke `go build` so source changes are picked up. Go's
		// incremental compilation makes the no-op case cheap.
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

// initNodes: chạy `evcosmos init` cho cả 2 node. Sau đó copy genesis.json từ
// sequencer sang full node — phải cùng genesis nếu không hai bên sẽ tính state
// root lệch và full node reject mọi block của sequencer.
//
// Khi --clean-on-start=false và signer.json của node đã tồn tại thì skip init
// (`evcosmos init` sẽ fail nếu signer.json đã có). Riêng full node init với
// aggregator=false vẫn tạo 1 genesis.json với proposer_address=null vì không có
// signer — luôn phải đè bằng genesis của sequencer ở dưới, không skip copy.
func (nm *nodeManager) initNodes() error {
	evcosmos := filepath.Join(nm.binariesDir, "evcosmos")

	for _, node := range nm.nodes {
		signerPath := filepath.Join(node.homeDir, "config", "signer.json")
		if _, err := os.Stat(signerPath); err == nil {
			log.Printf("Skipping init for %s: signer already present at %s", node.name, signerPath)
			continue
		}

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

// startExecutionServices: bật cosmos-exec-grpc cho mỗi node TRƯỚC khi bật evcosmos.
// evcosmos sẽ dial vào URL gRPC này (--grpc-executor-url) trong startSequencer/Full;
// nếu exec service chưa sẵn sàng thì evcosmos retry-fail.
//
// Hai bước chờ: waitForTCP (port mở) + waitForGRPCHealthy (handler init xong).
// Cần cả hai vì process có thể đã bind port nhưng gRPC service chưa register handler.
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

// startSequencer: bật evcosmos với aggregator=true. Sau khi RPC sẵn sàng, gọi
// `evcosmos net-info` để lấy multiaddr libp2p của sequencer; full node sẽ dial
// vào đây qua --evnode.p2p.peers (xem startFullNode).
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

// startFullNode: bật evcosmos với aggregator=false, peers = sequencer multiaddr.
// Full node không tạo block, chỉ subscribe gossip + replay DA + verify state.
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

// waitForChainSync: smoke test. Đợi tới 5 phút để full node lọt vào sync window
// (≤10 block lag). Cố ý KHÔNG fatal khi timeout — chỉ log WARN rồi cho stack
// chạy tiếp, để user còn vào RPC/log mà chẩn đoán nguyên nhân chậm (DA chết,
// peer dial fail, blob backpressure...).
func (nm *nodeManager) waitForChainSync() error {
	seqURL := fmt.Sprintf("http://127.0.0.1:%d/status", nm.nodes[0].rpcPort)
	fullURL := fmt.Sprintf("http://127.0.0.1:%d/status", nm.nodes[1].rpcPort)

	deadline := time.Now().Add(5 * time.Minute)
	lastSeq, lastFull := int64(0), int64(0)
	for time.Now().Before(deadline) {
		seqHeight, err1 := fetchLatestHeight(seqURL)
		fullHeight, err2 := fetchLatestHeight(fullURL)
		if err1 == nil && err2 == nil {
			lastSeq, lastFull = seqHeight, fullHeight
			if seqHeight > 0 && fullHeight > 0 && fullHeight <= seqHeight && seqHeight-fullHeight <= 10 {
				log.Printf("Sync check OK: sequencer=%d fullnode=%d", seqHeight, fullHeight)
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}

	log.Printf("WARN: full node did not reach sync window within 5m (sequencer=%d fullnode=%d). Continuing to run — inspect logs and RPCs to diagnose.",
		lastSeq, lastFull)
	return nil
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

// monitorProcesses: 1 goroutine/child Wait(); process chết bất thường thì cancel
// ctx kéo toàn stack xuống cùng (fail-fast — tránh trạng thái half-up khó debug).
// SIGINT/SIGTERM bên ngoài đi qua nm.cancel() ở main → ctx.Done() chặn ở select.
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

	return nil
}

// preflightDA: gọi thử `blob.GetAll` ở height=1 với namespace dummy để xác nhận
// (a) endpoint sống, (b) auth token hợp lệ, (c) token có quyền read. Bắt lỗi
// 401/permission-denied sớm — nếu để evcosmos tự fail thì log gây rối, mất 30-60s
// trước khi rõ nguyên nhân.
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

// cleanup: 2 pha — SIGTERM cho mọi child, đợi 800ms, rồi SIGKILL những đứa còn
// sống. Chạy qua defer trong main() nên luôn được gọi (kể cả khi run() lỗi giữa
// chừng). cleanOnExit chỉ xóa home dir, không động .env hay binary.
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

// emitBlobHeightHint: parse log của evcosmos để rút ra DA blob height và in
// hint cho user. Có deduplicate (lastBlobHeight) để không spam khi cùng 1 height
// được nhắc nhiều lần. Mục đích: lúc dev muốn verify blob trên Celestia thì
// biết ngay height nào để query (xem scripts/query_celestia_blob.sh).
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

// resolveDAFromEnv: map env DA_*/ENGRAM_*/COSMOS_DA_* → runConfig.
// DA_BRIDGE_RPC ưu tiên hơn DA_RPC (bridge node có public RPC; light node thường không).
// DA_NAMESPACE default "rollup" cho khớp engram-api convention.
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

// loadDotEnv: minimal .env parser (chấp nhận `export FOO=bar`, comment #...,
// quote " ' bao value). Không overwrite biến đã set ở shell — env shell luôn thắng.
// Cố ý không dùng godotenv để giữ script này zero-dep (chỉ stdlib).
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
		rawValue := strings.TrimSpace(parts[1])
		value := ""
		if strings.HasPrefix(rawValue, "\"") || strings.HasPrefix(rawValue, "'") {
			value = strings.Trim(rawValue, "\"'")
		} else {
			if idx := strings.Index(rawValue, "#"); idx >= 0 {
				rawValue = strings.TrimSpace(rawValue[:idx])
			}
			value = strings.TrimSpace(rawValue)
		}
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
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
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

// findProjectRoot: đi ngược từ cwd lên đến khi gặp thư mục có cả go.mod VÀ apps/.
// Check 2 điều kiện vì repo này multi-module — go.mod có ở nhiều nơi (apps/cosmos-exec,
// apps/testapp...), chỉ root mới có thêm thư mục apps/.
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
