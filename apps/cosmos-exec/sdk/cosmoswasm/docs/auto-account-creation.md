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

## 3. Why we still need a custom ante chain even with `x/auth`

A frequent assumption: "the chain enables `x/auth`, so signatures are checked automatically." They are not. `x/auth` ships the **building blocks** — `AccountKeeper`, account types, and a set of `Decorator`s under `x/auth/ante` — but it does **not** wire them into the tx pipeline. The chain has to assemble its own `AnteHandler` from those decorators. [`NewPermissionlessAnteHandler`](../../../app/ante.go) is that wiring.

On top of the wiring itself, cosmos-exec has two constraints that force a *custom* (not stock) chain:

### 3.1 cosmos-exec has no CheckTx admission step

In a normal Cosmos-SDK chain the ante handler runs **twice**: once in CheckTx (mempool admission) and again in DeliverTx (block execution). The mempool stage cheaply rejects garbage before it ever enters a block.

cosmos-exec doesn't have that stage:

```
POST /tx/submit  →  InjectTx (queues raw bytes)  →  FinalizeBlock
                                                    └─► ante chain runs here, once
```

Consequences:

- **Without an ante chain, nothing rejects a bad tx.** It fails deep inside message execution, after gas has been metered and state may have been touched.
- The ante chain is the **only** point where signature / sequence / fee validation happens, so it must be exhaustive. Dropping a decorator a stock chain has is not a free optimization — it's a security hole.

### 3.2 Auto-account-creation forces a non-stock ordering

Stock `authante.NewAnteHandler` was written for chains where every signer is pre-funded. Its order is:

```
… → DeductFee → SetPubKey → SigVerification → IncrementSequence
```

There is no slot for "create the account first." Inserting `AutoCreateAccount` requires picking the exact right position (after `ValidateBasic` so we don't create accounts for malformed txs; before `DeductFee` so the fee payer lookup finds the new account — see §1.a). That's why we ship our own constructor instead of calling `authante.NewAnteHandler`.

### 3.3 What `x/auth` provides vs. what the ante chain provides

| Concern                                | `x/auth` module                                 | Ante chain                                                  |
| -------------------------------------- | ----------------------------------------------- | ----------------------------------------------------------- |
| Account storage (`AccountKeeper`)      | Yes — `BaseAccount`, account_number, sequence   | Reads/writes it                                             |
| Signature cryptography primitives      | Yes — sign-mode handlers, pubkey types          | Calls them                                                  |
| **Deciding which checks run, in what order, for every tx** | **No**                       | **Yes — this is the ante chain's whole job**                |
| Replay protection                      | Stores `sequence` only                          | `IncrementSequenceDecorator` actually bumps it              |
| Auto-create / fee policy / size limits | Not opinionated                                 | Chain-specific decorators (`AutoCreate`, `DeductFee`, etc.) |

In short: `x/auth` is a **library**; the ante chain is the **policy** that says when and how the library gets called.

---

## 4. How signature verification actually works

### 4.1 What the client signs

The wallet (Keplr, the cosmos-exec Go SDK signer, etc.) builds a `SignDoc` containing four fields:

| Field             | Source                                          |
| ----------------- | ----------------------------------------------- |
| `body_bytes`      | Messages + memo + timeout height                |
| `auth_info_bytes` | Fee, gas limit, signer infos (pubkey + sequence)|
| `chain_id`        | From `/status` (or hard-coded)                  |
| `account_number`  | From `/auth/account/{addr}` (peeked, see §1.b)  |

These four are concatenated and hashed; the wallet signs the digest with the private key. Signature + original `body_bytes` / `auth_info_bytes` are bundled into a `TxRaw` and submitted.

**Critical:** the signature commits to `account_number` and `chain_id`. If the server rebuilds `SignDoc` with a different value for either, the signature won't verify — that's the failure mode `Peek` (§1.b) exists to prevent.

### 4.2 What the server verifies

Five decorators participate, each doing one focused job:

1. **`SetPubKeyDecorator`** — for accounts with no pubkey on record yet, copy the pubkey from the tx's `SignerInfo` into the account and persist. (First-tx case: `AutoCreate` made the account but didn't know its pubkey; this step records it.)
2. **`ValidateSigCountDecorator`** — caps the number of signatures per tx (DOS guard against pubkey arrays).
3. **`SigGasConsumeDecorator`** — charges gas proportional to verification cost (multi-sig is more expensive than single-sig).
4. **`SigVerificationDecorator`** — the actual crypto:
   - Loads the account from `AccountKeeper`, reads stored `account_number` and current `sequence`.
   - Rebuilds `SignDoc` with these server-side values + `chain_id`.
   - Calls the sign-mode handler (`SIGN_MODE_DIRECT`, `SIGN_MODE_LEGACY_AMINO_JSON`, …) to recompute the digest.
   - Verifies the digest against the supplied signature using the stored pubkey.
   - Any mismatch — wrong account number, wrong sequence, wrong chain-id, swapped pubkey, tampered body — surfaces as `signature verification failed … unauthorized`.
5. **`IncrementSequenceDecorator`** — bumps `sequence` by 1, so the same signed tx can't be replayed.

Replay protection works because `(account_number, sequence)` is a one-shot pair: after `IncrementSequence` runs, re-submitting the same bytes will fail at step 4 (the server now rebuilds `SignDoc` with `sequence+1`, the digest no longer matches the old signature).

### 4.3 Why none of these decorators are optional

| Decorator omitted        | What breaks                                                       |
| ------------------------ | ----------------------------------------------------------------- |
| `SetPubKey`              | Sig verification fails — no pubkey to check against               |
| `SigVerification`        | Anyone can forge a tx as anyone — no authentication at all        |
| `IncrementSequence`      | One signed tx can be replayed forever                             |
| `ValidateBasic`          | Malformed txs reach message execution and panic / burn gas        |
| `ConsumeGasForTxSize`    | Free DOS via huge tx bodies                                       |
| `DeductFee`              | (Fee-bearing chains only.) Free txs even when fees are required   |

The master switch is `COSMOS_EXEC_ENFORCE_SIGNATURES`. When **off** (default for dev/tests), `app.go` skips `SetAnteHandler` entirely — no ante chain runs, unsigned txs are accepted. When **on**, the full chain above runs in `FinalizeBlock` and unsigned / invalid-sig txs are rejected. Production must set this to `true`.

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
