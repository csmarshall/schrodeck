# Contract E: the `--json` CLI (Go core ↔ any UI or script)

Index of all contracts: [README.md](README.md). Decided in ADR [0024](../adr/0024-documented-contracts.md); UIs call the CLI and never reimplement sync logic (ADR [0018](../adr/0018-runtime-and-architecture.md)).

Current `schema_version`: **1**

## Rules

- Every command accepts `--json` **after the command name** (`schrodeck status --json`) and then prints **exactly one JSON object on one line** to stdout. Logs and usage messages go to stderr only.
- The object always starts with `schema_version`. Adding a member anywhere is **not** a breaking change; removing, renaming or changing the type of one **is**, and bumps `schema_version` in the same PR (`internal/cli/json.go`, `SchemaVersion`).
- Golden files in `internal/cli/testdata/golden/` pin every document shape, including the `error` envelope. A PR that changes one updates the golden file and this page together.

## Envelope

| member | type | meaning |
|---|---|---|
| `schema_version` | integer | this contract's version |
| `command` | string | the command that ran |
| `ok` | boolean | `false` when the command reports failure (exit code 1) |
| `data` | object | the command's result; absent when `error` is present |
| `error` | object `{message}` | present only when the command could not run |

## Exit codes

| code | meaning |
|---|---|
| 0 | success |
| 1 | the command ran and reports failure (`ok: false`), or could not run (`error`) |
| 2 | usage error; nothing is printed to stdout |

## Commands

| command | `data` |
|---|---|
| `version` | `{schrodeck_version}` |
| `status` | `{schrodeck_version}`; members are added as features land |
| `doctor` | `{checks: [{id, status, detail?}]}`, `status` ∈ `pass` \| `fail` \| `skip` |
