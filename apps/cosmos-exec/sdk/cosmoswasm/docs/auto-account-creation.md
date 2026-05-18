# Auto Account Creation & Tx Indexing

This page documents two backend features that enable a fully **permissionless, browser-based signing flow** (e.g. a Keplr-driven dApp talking to cosmos-exec via HTTP):

1. **Auto-create account** on the first signed tx — no faucet, no pre-funding.
2. **`tx_hashes` in `BlockInfo`** — so explorers can list txs per block and link to per-tx detail.

If you only call cosmos-exec through the Go SDK with a long-lived keypair that's already been funded, you don't need to worry about either — both features are transparent.

---

## 1. Permissionless first tx (no faucet)

### The problem

In a vanilla Cosmos SDK chain a new user can't submit a tx until their account exists in `x/auth` state. Normally a faucet sends them tokens first (via `MsgSend`), which side-effects an `AccountKeeper.NewAccount` call. cosmos-exec runs without a fee token and without a faucet, so that bootstrap path doesn't exist.

### The fix — two pieces

#### a) `AutoCreateAccountDecorator` (ante handler)

`apps/cosmos-exec/app/ante.go` inserts a custom decorator into the ante chain:

```
SetUpContext → ExtensionOptions → ValidateBasic → TxTimeoutHeight
→ ValidateMemo → ConsumeGasForTxSize
→ AutoCreateAccount        ← creates account if missing
→ DeductFee                ← needs the account to exist
→ SetPubKey → ValidateSigCount → SigGasConsume → SigVerification
→ IncrementSequence
```

`AutoCreateAccount` iterates `tx.GetSignaturesV2()` and, for every signer whose address isn't in `AccountKeeper`, calls `NewAccountWithAddress` + `SetAccount`. The new account gets a fresh, globally-unique `account_number` from the SDK's `GlobalAccountNumber` sequence.

**Ordering is critical.** `AutoCreate` must run **before** `DeductFee`, because `DeductFeeDecorator` looks up the fee payer (= first signer by default) in state and rejects the tx if not found. If you swap the order you get:

```
fee payer address: <garbled bytes> does not exist: unknown address
```

The garbled bytes are `[]byte` (raw 20-byte address) formatted with `%s` — `FeeTx.FeePayer()` returns bytes, not bech32, in cosmos-sdk v0.50.

#### b) `/auth/account` peeks the future number

There's still a chicken-and-egg problem: the client has to put `account_number` into `SignDoc` **before** `AutoCreate` runs. If `/auth/account` returns the zero value for not-yet-existing addresses, the client signs with `0`, but `AutoCreate` assigns (say) `7`, and `SigVerificationDecorator` rejects the signature with:

```
signature verification failed; please verify account number (0) and chain-id (...): (unable to verify single signer signature): unauthorized
```

So `executor.GetAccountInfo` now **peeks** the next sequence value when the account doesn't exist:

```go
acc := e.app.AccountKeeper.GetAccount(queryCtx, addr)
if acc == nil {
    nextNum, _ := e.app.AccountKeeper.AccountNumber.Peek(queryCtx)
    return AccountInfo{
        Address: bech32Addr, AccountNumber: nextNum, Sequence: 0, Exists: false,
    }, nil
}
```

`collections.Sequence.Peek` returns the value `Next()` will return — i.e. the same number `NewAccountWithAddress` will assign — without incrementing the counter. The client signs with that number, `AutoCreate` assigns the same number, sig verification succeeds.

### Caveat — racy under concurrent first-tx submission

Two callers polling `/auth/account` for **different new addresses** at the same moment will both receive the same peeked number. The first tx to land wins; the second's signature will fail (its signed `account_number` is now off by one) and the client must retry (which will refetch and see the bumped peek). Fine for single-user dev and most dApp UIs. **Not** suitable as-is if you expect a stampede of first-time signers from independent clients.

For that scenario, replace this with either:

- a real faucet that funds new addresses before they sign (standard cosmos flow), or
- a registration endpoint that synchronously creates the account and returns the assigned `account_number`.

### Why not just pin `account_number = 0` for all auto-created accounts?

The SDK's `x/auth` keeper enforces a `Unique` index on `account_number → address`. Module accounts (`fee_collector`, `mint`, etc.) already occupy `0, 1, 2 …`. Trying to `SetAccount` a second account with `account_number = 0` panics with:

```
collections: conflict: index uniqueness constrain violation: 0
```

So we keep account numbers unique (as the SDK expects) and surface the right number to the client at query time.

### Hardening for production

`COSMOS_EXEC_ENFORCE_SIGNATURES=true` activates the full ante chain (sig verification, sequence increment, etc.) — without it, AutoCreate is also unreachable. Always set this in prod regardless of whether you keep AutoCreate.

If you don't want auto-creation, simply omit `NewAutoCreateAccountDecorator` from the chain in `NewPermissionlessAnteHandler`. New addresses will then need to be funded via `MsgSend` first.

---

## 2. `tx_hashes` on `BlockInfo`

`/blocks/latest` and `/blocks/{height}` now include the hashes of the txs included in that block:

```json
{
  "height": 42,
  "time": "2026-05-14T10:00:00Z",
  "app_hash": "ab12…",
  "num_txs": 2,
  "tx_hashes": [
    "72b2169c6d86cd70ba96da1ffdc096dda2c8e462db2e00c95b872520847e4445",
    "f1e3a8c2…"
  ]
}
```

The field is populated in `executor.ExecuteTxs` from the same `validTxs` slice that gets handed to `FinalizeBlock`, using the same `hashTx` (SHA-256 of the tx bytes) that `/tx/{hash}` keys results by. Each hash in the list is directly resolvable via `GET /tx/{hash}`.

Combined with the existing `/tx/{hash}` endpoint, this is enough to build an etherscan-style explorer flow without any extra indexing:

```
GET /blocks/latest                 → recent block + its tx_hashes
GET /tx/{hash}                     → tx code, log, events for each hash
```

### Backwards compatibility

- `tx_hashes` is omitted (JSON `omitempty`) when the block had no txs.
- Blocks persisted **before** this change re-load from disk with `tx_hashes` unset. Explorers should handle the missing field — treat `num_txs > 0` with no `tx_hashes` as "this block predates indexing; hashes can't be reconstructed from disk".

---

## End-to-end flow for a Keplr dApp

```
1. User connects Keplr, picks chain "cosmos-wasm-local"
2. Frontend: GET /auth/account/{user_addr}
                       └─→ returns {account_number: 7, sequence: 0, exists: false}
                           (peeked from GlobalAccountNumber)
3. Frontend: build SignDoc with account_number=7, sequence=0
4. Keplr.signDirect(...) → user approves → returns signed TxRaw
5. Frontend: POST /tx/submit { tx_base64 }
6. Backend ante chain:
   a. AutoCreate: addr not in state → NewAccountWithAddress → account_number=7
   b. DeductFee: account 7 exists, fee is 0 → pass
   c. SigVerify: rebuild SignDoc with account_number=7 → matches client's sig
   d. IncrementSequence: account 7 sequence becomes 1
7. Tx executes (MsgStoreCode / MsgInstantiateContract / MsgExecuteContract)
8. Frontend polls GET /tx/result?hash=… until found
9. Frontend follows tx_hash links from /blocks/{height}.tx_hashes
   to render per-block tx lists in an explorer view
```

Subsequent txs from the same user just hit step 6b → 6d (the `addr not in state` branch in 6a is skipped because `HasAccount` is now true).
