// Package cosmoswasm is the Chain SDK for building app-chains on the ev-node
// modular rollup framework with Celestia DA.
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
//   - Client.SubmitBlob, Client.RetrieveBlob, Client.RetrieveBlobData
//   - Client.SubmitTxBytes, Client.SubmitTxBase64
//   - Client.GetTxResult, Client.WaitTxResult
//   - Client.QuerySmart, Client.QuerySmartRaw
//   - Client.CommitRoot, Client.CommitCritical                 — blob batch + on-chain root
//   - Client.SubmitBatch                                       — batch blob upload
//   - [GetProof]                                               — Merkle inclusion proof
//   - [EstimateCost]                                           — cost comparison calculator
//   - [BatchBuilder], [NewBatchBuilder], [BatchBuilderConfig]  — auto-flush accumulator
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
//   - [DAClient]                                               — DA layer interface (Celestia / Mock)
//   - [DABridge], [NewDABridge]                                — high-level DA + executor bridge
//   - [DANamespaceConfig]                                      — per-app-chain DA config
//
//   Data integrity (used automatically by CommitRoot & BatchBuilder):
//   - [MerkleProof], [BuildMerkleProof], [VerifyMerkleProof]   — Merkle proof construction
//   - [ChunkBlob], [ReassembleChunks]                          — large blob splitting
//   - [CompressGzip], [DecompressGzip], [CompressIfBeneficial] — gzip helpers
//   - [MaybeDecompress], [IsGzipCompressed]
//
//   Request/response types:
//   - [ExecutorClient]                                         — transport interface (HTTP / Mock / gRPC)
//   - [BlobRef], [CommitReceipt], [CommitRootRequest]
//   - [SubmitTxResponse], [GetTxResultResponse], [TxExecutionResult]
//   - [BlobSubmitResponse], [BlobRetrieveResponse], [BlobBatchResponse]
//   - [CostEstimate], [CostBreakdown], [EstimateCostRequest]
//
// ## Tier 3 — Dev tooling (may change between minor versions)
//
//   Testing mocks:
//   - [MockExecutorClient], [NewMockClient]                    — in-memory executor mock
//   - [MockDAClient], [NewMockDAClient]                        — in-memory DA mock
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
// One import, call NewClient or NewClientFromConfig, then use SubmitBlob /
// CommitRoot / QuerySmart. Internal refactoring (compression algorithm,
// Merkle tree structure, tx encoding) will never break your code.
package cosmoswasm
