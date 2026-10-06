# Contract E: the `--json` CLI (Go core ↔ any UI or script)

Index of all contracts: [README.md](README.md). Decided in ADR [0024](../adr/0024-documented-contracts.md); UIs call the CLI and never reimplement sync logic (ADR [0018](../adr/0018-runtime-and-architecture.md)).

Current `schema_version`: **1**

## Rules

- Every command accepts `--json` **anywhere after the command name** (`schrodeck status --json`, `schrodeck observe stop u2 --json --out r.md`); other flags may also follow positional arguments. The command then prints **exactly one JSON object on one line** to stdout. Logs and usage messages go to stderr only.
- The object always starts with `schema_version`. Adding a member anywhere is **not** a breaking change; removing, renaming or changing the type of one **is**, and bumps `schema_version` in the same PR (`internal/cli/json.go`, `SchemaVersion`).
- Enumerations (for example a check's `status`) may gain values without a version bump; a UI must treat an unknown value as "not pass".
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
| `status` | `{schrodeck_version, host?: {host_id, app: {installed, running, version?}, decks, profiles}}`; `host` is absent on an OS without a connector |
| `inventory` | `{host_id, app_version?, norm_version, fingerprint?, decks: [{key, model?, columns?, rows?, dials?, virtual, destination, why?}], profiles: [{folder, name, device, pages, hash?, hash_error?, app_identifier?}], load_errors?, unmatched?: {profiles?, decks?}}`; device ids are redacted unless `--show-ids`; `folder` and `unmatched.profiles` are this computer's real profile folder names, so this output is for the screen, never for a repository |
| `doctor` | `{tier, fingerprint?, accepted_now?, accept_refused?, checks: [{id, contract, tier, status, detail?, evidence?}]}`, `status` ∈ `pass` \| `fail` \| `skip` \| `info`; `ok` is false when any check fails; `accept_refused` says why `--accept-fingerprint` recorded nothing; evidence may name real profile folders, as for `inventory` |
| `observe` | `{name, snapshot?: "taken", report?: {name, before, after, app_version, changes: [{profile, where, path, kind, before?, after?}]}, written?}` (`written`: the resolved path the report was written to, redacted like the report) |
| `fixture` | `{folder, out, strings}` (`folder` and `out`: where the fixture was written, after pseudonymization and path resolution, with `out` redacted; `strings`: every remaining string, for human review) |
