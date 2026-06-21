# Telemetry Registry — Interaction Reference

Copy-paste-ready JSON for every **execute** (interact) and **query** message,
with the rules each one enforces.

> All message keys are `snake_case`. Enum values (e.g. match status) are also
> `snake_case`: `"lobby"`, `"active"`, `"finished"`.

CLI shape used throughout (wasmd):

```bash
# execute (state-changing tx)
wasmd tx wasm execute "$CONTRACT" '<JSON>' --from "$KEY" \
  --gas auto --gas-adjustment 1.3 -y

# query (read-only, free)
wasmd query wasm contract-state smart "$CONTRACT" '<JSON>'
```

---

## Instantiate

`max_players_per_match` is optional (defaults to **8**). The sender becomes the
`admin`.

```json
{ "max_players_per_match": 8 }
```

```bash
wasmd tx wasm instantiate "$CODE_ID" '{"max_players_per_match":8}' \
  --label "telemetry-registry" --admin "$ADMIN_ADDR" --from "$KEY" \
  --gas auto --gas-adjustment 1.3 -y
```

Minimal (use the default cap):

```json
{}
```

---

## Execute messages

### 1. `register_player`

Registers the sender as a player. **Anyone** may call, once per address.
`handle` is trimmed; it must be **1–32 chars** after trimming.
Errors: `InvalidHandle`, `AlreadyRegistered`.

```json
{ "register_player": { "handle": "Alice" } }
```

### 2. `create_match`

Creates a new match in the **lobby**. The sender becomes host **and** the first
player. Caller must be a **registered** player. Returns `match_id` in the tx
attributes (`match_id`).
Errors: `PlayerNotRegistered`.

```json
{ "create_match": {} }
```

### 3. `join_match`

Join an existing **lobby** match. Caller must be registered, not already in the
match, and the match must not be full (`max_players_per_match`).
Errors: `PlayerNotRegistered`, `MatchNotFound`, `BadMatchState` (not lobby),
`AlreadyJoined`, `MatchFull`.

```json
{ "join_match": { "match_id": 1 } }
```

### 4. `start_match`

Moves a match **lobby → active**. **Host only.**
Errors: `MatchNotFound`, `Unauthorized` (not host), `BadMatchState` (not lobby).

```json
{ "start_match": { "match_id": 1 } }
```

### 5. `record_telemetry`

Append one **per-frame telemetry commitment** (blob-first) to an **active**
match. Caller must be a **participant** of the match. The data itself lives on
Celestia; only the commitment is stored.
`height`, `namespace`, `tag` are optional — omitted values default to `0` / `""`
and `tag` defaults to `"telemetry"`.
Errors: `MatchNotFound`, `NotParticipant`, `BadMatchState` (not active).

Full:

```json
{
  "record_telemetry": {
    "match_id": 1,
    "commitment": "abcd1234",
    "height": 100,
    "namespace": "0x0000...ns",
    "tag": "telemetry"
  }
}
```

Minimal (only `commitment` required):

```json
{ "record_telemetry": { "match_id": 1, "commitment": "abcd1234" } }
```

### 6. `record_replay`

Record the **full-replay Merkle batch root** on a match. **Host only.** May be
called while the match is active **or** after it has finished. If `namespace` is
provided, a `"replay"` marker is also appended to the telemetry frames so
clients that only read `match_telemetry.frames` still see it.
Errors: `MatchNotFound`, `Unauthorized` (not host).

Full:

```json
{
  "record_replay": {
    "match_id": 1,
    "root": "deadbeef",
    "height": 101,
    "count": 5,
    "namespace": "0x0000...ns"
  }
}
```

Minimal (only `root` required):

```json
{ "record_replay": { "match_id": 1, "root": "deadbeef" } }
```

### 7. `finish_match`

Moves a match **active → finished**. **Host only.** `winner` and each
`scores[].player` are validated as real bech32 addresses. Every participant's
profile is updated: `matches_played += 1`, `total_score += score` (0 if absent
from `scores`), and `wins += 1` for the winner.
`winner` is optional; `scores` may be empty (`[]`).
Errors: `MatchNotFound`, `Unauthorized`, `BadMatchState` (not active),
`Std` (invalid address), `PlayerNotRegistered` (a listed player is not registered).

```json
{
  "finish_match": {
    "match_id": 1,
    "winner": "wasm1aliceaddr...",
    "scores": [
      { "player": "wasm1aliceaddr...", "score": 100 },
      { "player": "wasm1bobaddr...",   "score": 40 }
    ]
  }
}
```

No winner, no scores:

```json
{ "finish_match": { "match_id": 1, "winner": null, "scores": [] } }
```

### 8. `set_max_players` (admin)

Update the per-match player cap. **Admin only.**
Errors: `Unauthorized`.

```json
{ "set_max_players": { "max_players_per_match": 16 } }
```

### 9. `transfer_admin` (admin)

Hand admin rights to another (validated) address. **Admin only.**
Errors: `Unauthorized`, `Std` (invalid address).

```json
{ "transfer_admin": { "new_admin": "wasm1newadmin..." } }
```

---

## Query messages

### 1. `config`

Returns the global `Config` (`admin`, `max_players_per_match`).

```json
{ "config": {} }
```

Response:

```json
{ "admin": "wasm1...", "max_players_per_match": 8 }
```

### 2. `player`

A player's profile + aggregate stats. `address` is validated. Returns `null` if
unknown.

```json
{ "player": { "address": "wasm1aliceaddr..." } }
```

Response:

```json
{
  "handle": "Alice",
  "matches_played": 3,
  "wins": 1,
  "total_score": 240,
  "joined_at": 123456
}
```

### 3. `match`

The full match record by id. Returns `null` if not found.

```json
{ "match": { "match_id": 1 } }
```

### 4. `list_matches`

List matches, optionally filtered by `status`. `limit` defaults to **20**,
capped at **100**. Iterated by ascending match id.
`status` values: `"lobby"`, `"active"`, `"finished"`.

All matches (default limit):

```json
{ "list_matches": {} }
```

Only active, up to 50:

```json
{ "list_matches": { "status": "active", "limit": 50 } }
```

### 5. `match_telemetry`

The blob-first records of a match, so a client can pull the data from Celestia
via `Blob.Get(height, namespace, commitment)`. Returns empty frames if the match
doesn't exist (never errors).

```json
{ "match_telemetry": { "match_id": 1 } }
```

Response:

```json
{
  "match_id": 1,
  "frames": [
    { "commitment": "abcd1234", "height": 100, "namespace": "0x..ns", "kind": "telemetry", "recorded_at": 123460 }
  ],
  "replay_root": "deadbeef",
  "replay_height": 101
}
```

### 6. `leaderboard`

Players ranked by **wins**, then **total_score** (both descending). `limit`
defaults to **10**, capped at **100**.

```json
{ "leaderboard": { "limit": 10 } }
```

Response:

```json
{
  "players": [
    { "address": "wasm1...", "handle": "Alice", "wins": 1, "total_score": 100 }
  ]
}
```

### 7. `stats`

Global counters.

```json
{ "stats": {} }
```

Response:

```json
{ "total_players": 2, "total_matches": 1, "active_matches": 0 }
```

---

## Typical end-to-end flow

```bash
# 1. two players register
wasmd tx wasm execute "$C" '{"register_player":{"handle":"Alice"}}' --from alice -y
wasmd tx wasm execute "$C" '{"register_player":{"handle":"Bob"}}'   --from bob   -y

# 2. alice hosts, bob joins, alice starts  (match_id = 1)
wasmd tx wasm execute "$C" '{"create_match":{}}'            --from alice -y
wasmd tx wasm execute "$C" '{"join_match":{"match_id":1}}'  --from bob   -y
wasmd tx wasm execute "$C" '{"start_match":{"match_id":1}}' --from alice -y

# 3. record blob-first telemetry + replay root
wasmd tx wasm execute "$C" '{"record_telemetry":{"match_id":1,"commitment":"abcd1234","height":100,"namespace":"0xns"}}' --from alice -y
wasmd tx wasm execute "$C" '{"record_replay":{"match_id":1,"root":"deadbeef","height":101,"count":5}}'                   --from alice -y

# 4. finish with winner + scores
wasmd tx wasm execute "$C" '{"finish_match":{"match_id":1,"winner":"'"$ALICE"'","scores":[{"player":"'"$ALICE"'","score":100},{"player":"'"$BOB"'","score":40}]}}' --from alice -y

# 5. read results
wasmd query wasm contract-state smart "$C" '{"match_telemetry":{"match_id":1}}'
wasmd query wasm contract-state smart "$C" '{"leaderboard":{}}'
wasmd query wasm contract-state smart "$C" '{"stats":{}}'
```

---

## Off-chain payload formats (what the commitments point to)

The contract only stores **commitments / Merkle roots** — never the game data
itself. The actual bytes live on Celestia DA and are produced by the SDK example
[`game-telemetry/main.go`](../../../ev-node/apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry/main.go).
There are **two distinct payload streams**, each with its own on-chain message.

| | Per-frame telemetry | Full-match replay |
| --- | --- | --- |
| Produced by | `makeFrame()` | `makeReplay()` |
| Format | JSON object | text event-log |
| Size | small (one frame) | bulk (e.g. 300 KiB) |
| Pushed to Celestia via | `SubmitBlob` (1 blob) | `SubmitBatch` (chunks + Merkle) |
| Recorded on-chain via | `record_telemetry` (commitment) | `record_replay` (Merkle root) |

### 1. Per-frame telemetry → `record_telemetry`

Each frame is a JSON snapshot of all players at one tick:

```json
{
  "match": "match-42",
  "tick": 3,
  "ts": 1718000000000,
  "players": [
    { "id": "p1", "pos": [4.5, 0, 12.3], "hp": 97 },
    { "id": "p2", "pos": [-3, 4.2, 9.0], "hp": 87 },
    { "id": "p3", "pos": [1.5, 1.1, -3.7], "hp": 64 }
  ]
}
```

- `pos` is `[x, y, z]`; `hp` is the player's health that tick.
- The JSON is optionally compressed (`CompressIfBeneficial`), then `SubmitBlob`
  returns `(commitment, height, namespace)` — exactly the fields fed into
  `record_telemetry`.

### 2. Full-match replay → `record_replay`

The replay is a flat **event-log text**, repeated/extended to the full match
size. One representative segment:

```
tick:move(p1,1.5,0,12.3);move(p2,-3,4,9);shoot(p1,p3);
```

Grammar of that line:

| Token | Meaning |
| --- | --- |
| `tick:` | start of a new tick / frame boundary |
| `move(p1,1.5,0,12.3)` | player `p1` moved to position `x=1.5, y=0, z=12.3` |
| `move(p2,-3,4,9)` | player `p2` moved to `(-3, 4, 9)` |
| `shoot(p1,p3)` | player `p1` shot player `p3` |
| `;` | event separator |

How it becomes the on-chain `record_replay` fields:

```
makeReplay(300 KiB)                 # the "tick:move(...);shoot(...)" bytes
  → ChunkBlob(replay, 64 KiB)       # split into chunks + meta
  → SubmitBatch(chunks)             # push every chunk to Celestia DA
        ⇒ { root, height, count, commitments }
  → record_replay { match_id, root, height, count, namespace }
```

Only the 32-byte **Merkle `root`** (plus `height`/`count`) lands on-chain — not
the 300 KiB. The `root` is what `record_replay` stores in `Match.replay_root`.

### Reading the data back

The whole point of keeping `height` + `namespace` on-chain is retrieval:

```
query match_telemetry / match  → (height, namespace, commitment | root, commitments)
  → RetrieveBlob(height, commitment)     # per chunk/frame, from Celestia
  → MaybeDecompress / ReassembleChunks   # restore original bytes
```

For the replay this reassembles the exact `tick:move(...)` byte stream and
verifies it against the Merkle root; for a telemetry frame it restores the JSON
snapshot above.
