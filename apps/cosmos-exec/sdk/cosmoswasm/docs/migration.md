# Migration Guide

## v0.2 to v0.3

### Internal package separation

Implementation details have moved to `internal/` subpackages. **If your code only imports `cosmoswasm`**, nothing changes. If you were reaching into unexported helpers, here is what moved:

| Before (v0.2)                              | After (v0.3)                         |
|--------------------------------------------|--------------------------------------|
| `buildProtoTxBytes()` (unexported in tx.go)| `internal/txcodec.BuildProtoTxBytes` |
| `normalizeJSONMsg()` (unexported in tx.go) | `internal/txcodec.NormalizeJSONMsg`  |
| chain runner logic in `chain.go`           | `internal/devchain`                  |
| Merkle tree construction                   | `internal/merkle`                    |
| gzip helpers (internals)                   | `internal/compress`                  |
| blob chunking (internals)                  | `internal/chunk`                     |

Go's `internal/` rule means external modules **cannot** import these packages. This is intentional — it lets us refactor internals without breaking your code.

### Gas constants unexported

The following constants were public in v0.2 but are now unexported (lowercase):

- `CelestiaFixedGas` / `CelestiaGasPerByte` / `CelestiaShareSize`
- `CosmosBaseTxGas` / `CosmosGasPerMsgByte` / `CosmosGasPerStoreByte`

**Why:** These are implementation details of `EstimateCost()`. If Celestia updates its gas model, we can adjust them without a major version bump. If you referenced these constants directly, use `EstimateCost()` instead — it encapsulates the gas model.

### New additions (non-breaking)

| Symbol | What |
|--------|------|
| `DAClient` interface | Celestia DA layer abstraction |
| `DABridge`, `NewDABridge` | High-level DA + executor bridge |
| `MockDAClient`, `NewMockDAClient` | In-memory DA mock for tests |
| `DALChainConfig.Validate()` | Config validation before starting chain |
| `BatchBuilder` | Auto-flush blob accumulator |
| `SDKError` | Structured errors with Op, Cause, Hint |
| `CommitCritical` | Like CommitRoot but returns error on partial failure |

### API tier classification

All exported symbols are now classified into three tiers (see `go doc cosmoswasm`):

- **Tier 1 (Core)** — stable, start here: `NewClient`, `SubmitBlob`, `CommitRoot`, etc.
- **Tier 2 (Power-user)** — stable, use when needed: tx builders, namespace, DA, Merkle.
- **Tier 3 (Dev tooling)** — may change between minor versions: mocks, local chain runner.

Tier 1 + 2 are the stability contract. Tier 3 may change in minor releases.

## Future: v1.0 plan

When the SDK reaches v1.0, the following commitments apply:

- **Tier 1 and Tier 2** become the long-term stable API. Breaking changes require a new major version.
- **Tier 3** (mocks, dev chain runner) remains flexible between minor versions.
- **`internal/`** packages can change freely — they are not importable by external code.

### Migration checklist for v1.0 (when released)

1. Replace any `NewClient("http://...")` calls with `NewClientFromConfig(SDKConfig{...})` for production use.
2. Use `SDKError` and sentinel errors (`ErrNotReachable`, `ErrTxFailed`, etc.) for structured error handling.
3. Use `BatchBuilder` instead of manual loop + `SubmitBlob` for high-throughput workloads.
4. Use `DABridge.SubmitAndCommit` instead of separate DA + executor calls.
5. Remove any references to unexported gas constants — use `EstimateCost()`.
