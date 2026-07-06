// Package cosmoswasm is the Chain SDK for building app-chains on the ev-node
// modular rollup framework with Celestia DA.
//
// VI (tóm tắt): SDK Go giúp dApp build & gửi tx tới chain cosmos-exec, đồng
// thời upload "blob" dữ liệu off-chain lên DA (Celestia) và ghi commitment
// on-chain. Chia 3 tầng API:
//   - Tier 1 (Core): NewClient / Submit / Query — dùng cho mọi app.
//   - Tier 2 (Power-user): BuildTx, Namespace, Merkle, Chunk — khi cần kiểm
//     soát sâu.
//   - Tier 3 (Dev tooling): DALChain local — chỉ dùng cho test.
// File này chỉ chứa documentation comment, không có code chạy.
//
// # API Tiers
//
// The SDK surface is divided into three tiers. When writing application code,
// start with Tier 1 — you should rarely need anything below it.
//
// ## Tier 1 — Core API (stable, start here)
//
// These are the primary entry points that every SDK user needs:
//
//   - [SDKConfig], [DefaultSDKConfig], [NewClientFromConfig]  — production client setup
//   - [NewClient]                                              — quick dev client (localhost)
//   - Client.SubmitTxBytes, Client.SubmitTxBase64
//   - Client.GetTxResult, Client.WaitTxResult
//   - Client.GetTxFinality, Client.WaitTxFinality              — soft vs DA finality
//   - Client.SimulateTx, Client.EstimateCost                   — gas thật + ước lượng chi phí trước khi ký
//   - Client.QuerySmart, Client.QuerySmartRaw
//   - Client.GetLatestBlock, Client.GetBlockByHeight, Client.GetPendingTxCount — đọc block & mempool
//   - [NewBlobClient], BlobClient.SubmitBlob, BlobClient.RetrieveBlob — blob-first DA (Celestia)
//   - BlobClient.SubmitBatch, BlobClient.VerifyBlob            — batch upload + integrity check
//   - [BuildBlobCommitTx], [BuildBatchRootTx]                  — record commitment/root on-chain
//   - [StoreBlobAndRecord], [StoreBatchAndRecord]              — gộp upload DA + ghi on-chain (1 call)
//   - [SDKError], sentinel errors ([ErrNotReachable], etc.)    — structured errors
//
// ## Tier 2 — Power-user utilities (stable, use when needed)
//
// Lower-level building blocks for advanced patterns. Most users won't need
// these directly — Tier 1 calls them internally.
//
//   Transaction building:
//   - [BuildStoreTx], [BuildInstantiateTx], [BuildExecuteTx]   — CosmWasm tx construction
//   - [BuildBlobCommitTx], [BuildBatchRootTx]                  — blob-first on-chain recording
//   - [EncodeTxBase64], [EncodeTxHex], [DefaultSender]
//
//   Namespace & DA layer:
//   - [Namespace], [NewNamespaceV0], [NamespaceFromString], [NamespaceFromHex]
//
//   Data integrity (used automatically by SubmitBatch & BatchBuilder):
//   - [MerkleProof], [BuildMerkleProof], [VerifyMerkleProof]   — Merkle proof construction
//   - [ChunkBlob], [ReassembleChunks]                          — large blob splitting
//   - [CompressGzip], [DecompressGzip], [CompressIfBeneficial] — gzip helpers
//   - [MaybeDecompress], [IsGzipCompressed]
//
//   Request/response types:
//   - [ExecutorClient]                                         — transport interface (HTTP / gRPC)
//   - [SubmitTxResponse], [GetTxResultResponse], [TxExecutionResult]
//   - [BlobSubmitResponse], [BlobRetrieveResponse], [BlobBatchResponse]
//
// ## Tier 3 — Dev tooling (may change between minor versions)
//
//   Local chain runner:
//   - [DALChainConfig], [StartDALChain], [DALChainProcess]     — local chain for dev/test
//
// # Internal packages (NOT public API)
//
// Implementation details live in internal/ subpackages. These are NOT
// importable by external code — Go enforces this at the compiler level.
// The public functions listed above are thin wrappers that delegate to
// internal/ and expose only stable types.
//
//   - internal/merkle    — binary SHA-256 Merkle tree
//   - internal/compress  — gzip utilities
//   - internal/chunk     — blob splitting
//   - internal/txcodec   — protobuf tx encoding
//   - internal/devchain  — local chain process management
//
// You can freely refactor internal packages without breaking any external code.
//
// # Versioning policy
//
// Tier 1 and Tier 2 are the stable contract — breaking changes only happen in
// major versions. Tier 3 (mocks, dev tooling) and internal packages may change
// between minor versions. Additions are always backward-compatible.
//
// # Design principle
//
// User code should only need:
//
//	import cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
//
// One import, call NewClient or NewClientFromConfig for tx/query, and
// NewBlobClient for blob-first DA. Internal refactoring (compression algorithm,
// Merkle tree structure, tx encoding) will never break your code.
package cosmoswasm
