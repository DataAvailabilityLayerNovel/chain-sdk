// package cosmoswasm: BlobClient — tầng "blob-first" của SDK. dApp đẩy dữ liệu
// LỚN (telemetry game, ảnh, snapshot, log...) THẲNG lên Celestia DA, nhận về
// (commitment, height) rồi chỉ ghi cam kết đó on-chain qua BuildBlobCommitTx.
// Data lớn KHÔNG bao giờ vào state WASM → tiết kiệm gas, không phình chain.
//
// VI: BlobClient TÁCH RIÊNG khỏi Client (Client nói chuyện với cosmos-exec qua
// HTTP; BlobClient nói chuyện với Celestia bridge qua JSON-RPC). Hai mối quan
// tâm khác nhau nên không gộp. Luồng tx hiện có KHÔNG bị ảnh hưởng — đây là
// tính năng cộng thêm, bật khi cần.
package cosmoswasm

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	// libshare: kiểu Namespace + Blob gốc của Celestia (go-square).
	libshare "github.com/celestiaorg/go-square/v3/share"

	// jsonrpc: client DA JSON-RPC tới Celestia bridge (đã dùng trong
	// examples/forced-inclusion — đường này đã chạy thật).
	"github.com/DataAvailabilityLayerNovel/chain-sdk/pkg/da/jsonrpc"

	// merkle: cây SHA-256 nội bộ — dùng tính root cho SubmitBatch (1 root đại
	// diện N blob, cùng thuật toán với proof để verify membership sau này).
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/merkle"
)

const (
	// MaxBlobSize is the SDK safety cap for a single blob. Celestia itself
	// allows larger blobs (spanning many shares), but very large single blobs
	// are usually a sign you should chunk. Override by submitting via SubmitBatch.
	// VI: trần an toàn cho 1 blob (2 MiB). Không phải giới hạn cứng của Celestia,
	// chỉ là chặn nhầm lẫn — data lớn hơn nên chia nhỏ rồi SubmitBatch.
	MaxBlobSize = 2 * 1024 * 1024
	// MaxBatchTotal caps the total bytes in one SubmitBatch call.
	// VI: trần tổng bytes cho 1 lần SubmitBatch (8 MiB).
	MaxBatchTotal = 8 * 1024 * 1024
)

// BlobClientConfig configures a BlobClient.
//
// VI: cấu hình BlobClient. BridgeRPC + Namespace là bắt buộc.
type BlobClientConfig struct {
	// BridgeRPC is the Celestia bridge/DA JSON-RPC URL (e.g. http://localhost:26658).
	// VI: URL JSON-RPC của Celestia bridge node. BẮT BUỘC.
	BridgeRPC string
	// AuthToken is the DA auth token (Bearer). Empty if the node allows anon read/write.
	// VI: token auth của DA node (rỗng nếu node mở).
	AuthToken string
	// Namespace is the app's human-readable namespace name (e.g. "my-game").
	// Derived deterministically into a Celestia v0 namespace via NamespaceFromString.
	// VI: tên namespace của app — map deterministic sang namespace Celestia v0
	// qua NamespaceFromString. Mỗi app NÊN có namespace riêng. BẮT BUỘC.
	Namespace string
	// GasPrice is the DA gas price (utia per gas) used when submitting blobs.
	// Set it (> 0) to send an explicit price to the node. Leaving it 0 makes the
	// node auto-estimate the gas price — a path that panics with a nil-pointer
	// dereference on some celestia-node releases. Pass DA_GAS_PRICE here.
	// VI: gas price DA khi submit blob. Đặt > 0 để gửi giá tường minh; để 0 thì
	// node tự ước lượng — đường này panic (nil pointer) ở vài bản celestia-node.
	GasPrice float64
}

// BlobClient submits and retrieves application blobs directly on Celestia DA.
//
// VI: client blob-first. Giữ DA client + namespace đã resolve. Caller PHẢI gọi
// Close() khi xong (đóng kết nối WS/HTTP tới bridge).
type BlobClient struct {
	da    *jsonrpc.Client    // kết nối JSON-RPC tới Celestia bridge.
	ns    libshare.Namespace // namespace Celestia đã resolve từ config.
	nsHex string             // dạng hex để ghi on-chain / trả về cho caller.
	opts  *jsonrpc.SubmitOptions // tuỳ chọn submit (gas price tường minh) hoặc nil.
}

// NewBlobClient connects to the Celestia bridge and resolves the namespace.
//
// VI: kết nối bridge + resolve namespace. Lỗi nếu thiếu BridgeRPC/Namespace
// hoặc không kết nối được DA.
func NewBlobClient(ctx context.Context, cfg BlobClientConfig) (*BlobClient, error) {
	if strings.TrimSpace(cfg.BridgeRPC) == "" {
		return nil, errors.New("BridgeRPC is required")
	}
	if strings.TrimSpace(cfg.Namespace) == "" {
		return nil, errors.New("Namespace is required")
	}

	// Resolve namespace qua helper sẵn có (single source of truth) rồi chuyển
	// 29-byte wire form sang kiểu libshare.Namespace mà jsonrpc cần.
	nsObj := NamespaceFromString(cfg.Namespace)
	ns, err := libshare.NewNamespaceFromBytes(nsObj.Bytes())
	if err != nil {
		return nil, fmt.Errorf("resolve namespace %q: %w", cfg.Namespace, err)
	}

	da, err := jsonrpc.NewClient(ctx, cfg.BridgeRPC, cfg.AuthToken, "")
	if err != nil {
		return nil, fmt.Errorf("connect Celestia bridge %s: %w", cfg.BridgeRPC, err)
	}

	// Build explicit submit options when a gas price is given, so the node skips
	// its auto gas-price estimation (which nil-pointer panics on some releases).
	var opts *jsonrpc.SubmitOptions
	if cfg.GasPrice > 0 {
		opts = &jsonrpc.SubmitOptions{GasPrice: cfg.GasPrice, IsGasPriceSet: true}
	}

	return &BlobClient{da: da, ns: ns, nsHex: nsObj.Hex(), opts: opts}, nil
}

// Close releases the DA connection. Safe to call on a nil client.
//
// VI: đóng kết nối DA. nil-safe (gọi nhiều lần không panic).
func (b *BlobClient) Close() error {
	if b == nil || b.da == nil {
		return nil
	}
	b.da.Close()
	return nil
}

// Namespace returns the hex-encoded Celestia namespace this client uses.
//
// VI: trả namespace (hex) client đang dùng — tiện cho log / ghi on-chain.
func (b *BlobClient) Namespace() string { return b.nsHex }

// SubmitBlob uploads one blob to Celestia and returns its commitment + DA height.
// Record BOTH on-chain (BuildBlobCommitTx) — retrieval needs height + namespace
// + commitment, not commitment alone.
//
// VI: đẩy 1 blob lên Celestia, trả (commitment, height, namespace). Ghi CẢ HAI
// on-chain — retrieve cần đủ (height, namespace, commitment).
func (b *BlobClient) SubmitBlob(ctx context.Context, data []byte) (*BlobSubmitResponse, error) {
	if len(data) == 0 {
		return nil, errors.New("blob data is empty")
	}
	if len(data) > MaxBlobSize {
		return nil, fmt.Errorf("blob too large: %d bytes (max %d) — use SubmitBatch or chunk", len(data), MaxBlobSize)
	}

	blob, err := jsonrpc.NewBlobV0(b.ns, data)
	if err != nil {
		return nil, fmt.Errorf("build blob: %w", err)
	}

	// Submit: 1 lời gọi RPC THẲNG tới Celestia (ngoài luồng tx của chain).
	height, err := b.da.Blob.Submit(ctx, []*jsonrpc.Blob{blob}, b.opts)
	if err != nil {
		return nil, fmt.Errorf("submit blob to Celestia: %w", err)
	}

	return &BlobSubmitResponse{
		Commitment: hex.EncodeToString(blob.Commitment),
		Height:     height,
		Namespace:  b.nsHex,
		Size:       len(data),
	}, nil
}

// RetrieveBlob fetches a blob back from Celestia. Needs the DA height returned
// by SubmitBlob (and recorded on-chain) — commitment alone is not enough.
//
// VI: lấy blob về từ Celestia. CẦN height (từ SubmitBlob, đã ghi on-chain) +
// commitment. Commitment dạng hex (chấp nhận tiền tố 0x).
func (b *BlobClient) RetrieveBlob(ctx context.Context, height uint64, commitmentHex string) ([]byte, error) {
	if height == 0 {
		return nil, errors.New("height is required to retrieve a blob")
	}
	com, err := decodeCommitment(commitmentHex)
	if err != nil {
		return nil, err
	}

	blob, err := b.da.Blob.Get(ctx, height, b.ns, com)
	if err != nil {
		return nil, fmt.Errorf("get blob from Celestia (height=%d): %w", height, err)
	}
	return blob.Data(), nil
}

// SubmitBatch uploads N blobs in a single Celestia submit (one DA height for
// all), and returns a Merkle root over their commitments so the whole batch can
// be recorded on-chain with a single 32-byte value (BuildBatchRootTx).
//
// VI: đẩy N blob trong 1 lần submit (cùng 1 height), trả root cây Merkle gom N
// commitment → ghi on-chain 1 lần cho cả batch. Vẫn trả từng commitment để
// retrieve / verify lẻ.
func (b *BlobClient) SubmitBatch(ctx context.Context, blobs [][]byte) (*BlobBatchResponse, error) {
	if len(blobs) == 0 {
		return nil, errors.New("no blobs to submit")
	}

	daBlobs := make([]*jsonrpc.Blob, 0, len(blobs))
	commitments := make([]string, 0, len(blobs))
	total := 0
	for i, data := range blobs {
		if len(data) == 0 {
			return nil, fmt.Errorf("blob %d is empty", i)
		}
		total += len(data)
		if total > MaxBatchTotal {
			return nil, fmt.Errorf("batch too large: >%d bytes total (max %d)", total, MaxBatchTotal)
		}
		blob, err := jsonrpc.NewBlobV0(b.ns, data)
		if err != nil {
			return nil, fmt.Errorf("build blob %d: %w", i, err)
		}
		daBlobs = append(daBlobs, blob)
		commitments = append(commitments, hex.EncodeToString(blob.Commitment))
	}

	height, err := b.da.Blob.Submit(ctx, daBlobs, b.opts)
	if err != nil {
		return nil, fmt.Errorf("submit batch to Celestia: %w", err)
	}

	// Root cây Merkle gom N commitment (leafIndex 0 chỉ để lấy root; path bỏ).
	root, _, err := merkle.BuildProof(commitments, 0)
	if err != nil {
		return nil, fmt.Errorf("compute batch merkle root: %w", err)
	}

	return &BlobBatchResponse{
		Root:        root,
		Commitments: commitments,
		Count:       len(commitments),
		Height:      height,
	}, nil
}

// VerifyBlob retrieves the blob at (height, commitment) and checks that the
// returned data re-derives the same commitment — i.e. the data is intact and
// genuinely the one committed on-chain (tamper-evident).
//
// VI: lấy blob về rồi tính lại commitment từ data, so với commitment yêu cầu.
// Khớp → data nguyên vẹn & đúng cái đã cam kết on-chain. Đây là kiểm tra toàn
// vẹn ở tầng SDK; bằng chứng DA-inclusion sâu hơn dùng GetCommitmentProof.
func (b *BlobClient) VerifyBlob(ctx context.Context, height uint64, commitmentHex string) (bool, error) {
	want := normalizeHex(commitmentHex)
	if want == "" {
		return false, errors.New("commitment is required")
	}

	data, err := b.RetrieveBlob(ctx, height, commitmentHex)
	if err != nil {
		return false, err
	}

	recomputed, err := jsonrpc.NewBlobV0(b.ns, data)
	if err != nil {
		return false, fmt.Errorf("recompute commitment: %w", err)
	}
	return hex.EncodeToString(recomputed.Commitment) == want, nil
}

// decodeCommitment parses a hex commitment (with or without 0x) into the DA type.
func decodeCommitment(commitmentHex string) (jsonrpc.Commitment, error) {
	h := normalizeHex(commitmentHex)
	if h == "" {
		return nil, errors.New("commitment is required")
	}
	raw, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("invalid commitment hex: %w", err)
	}
	return jsonrpc.Commitment(raw), nil
}

// normalizeHex trims spaces, a leading 0x, and lowercases.
func normalizeHex(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	return strings.ToLower(s)
}
