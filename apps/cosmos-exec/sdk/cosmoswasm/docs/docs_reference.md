# Documentation Index

| File | What's inside |
|------|---------------|
| [Getting Started](getting-started.md) | End-to-end: compile .wasm → start chain → deploy contract → interact via SDK |
| [Architecture](architecture.md) | Project structure: every folder/file, data flow, key interfaces |
| [Configuration](configuration.md) | All SDK + server config fields, env vars, dev/staging/prod profiles |
| [API Reference](api-reference.md) | Every public method: params, response, errors, example code |
| [Error Handling](error-handling.md) | SDKError, sentinel errors, retry policy, mapping errors to app actions |
| [Production Guide](production-guide.md) | Timeout/retry tuning, auth, rate limiting, monitoring, SLOs |
| [Troubleshooting](troubleshooting.md) | Common failures, diagnostic curl commands, debug checklist |
| [Migration Guide](migration.md) | v0.2→v0.3 changes, internal separation, v1.0 plan |
| [Chain Runtime Flow](chain-flow.md) | Tx lifecycle, block production, DA submission, node sync, P2P broadcast |
| [Auto Account Creation & Tx Indexing](auto-account-creation.md) | Permissionless first-tx flow for browser dApps (Keplr), `/auth/account` peek behavior, `tx_hashes` field on blocks |
| [Sequencer & Security](sequencer-security.md) | Vì sao không cần validator set; single vs based sequencer; forced inclusion; chống kiểm duyệt; liên hệ 0-fee |
| [Node Operations](node-operations.md) | Sequencer + full node start ntn; 2 tiến trình/node; ports; data on-chain (Celestia) vs off-chain (local); mọi biến đọc từ đâu |
| [Frontend Integration](frontend-integration.md) | my-dapp-web: proxy, Keplr (preferNoSetFee), ký tx, explorer/DA view, faucet UI; hợp đồng cấu hình phí FE↔backend |
| [Fee Economics](fee-economics.md) | Bật fee thật vs Cosmos chain thường; cost model DA+gas; chi phí Celestia; điểm hòa vốn; LazyMode; **treasury+faucet (A+B) đã implement** — env, `/faucet` endpoint, nút web "Get test tokens"; checklist production |
