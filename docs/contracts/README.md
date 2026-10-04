# Contracts

Every boundary schrodeck depends on, who owns it, and what catches a break. Decided in ADR [0024](../adr/0024-documented-contracts.md). Other documents link here; they don't restate these contracts.

| | Contract | Between | Owner | Versioned by | Enforced by | Where |
|---|---|---|---|---|---|---|
| **A** | OS connector | Go core ↔ an OS connector | us | our releases | conformance suite on each OS's CI | [os-connector.md](os-connector.md) (single source of truth for the port list) |
| **B** | Client, per OS | OS connector ↔ the Stream Deck app on that OS (paths, prefs, process behavior) | Elgato (observed) | OS + app version | `schrodeck doctor` probes + schema guard | [client-os.md](client-os.md) |
| **C** | Profile format | Go core ↔ the app's profile files, on any OS | Elgato (observed) | manifest `Version` (+ app version) | `doctor` launch-rewrite and round-trip probes + schema guard | [profile-format.md](profile-format.md) |
| **D** | Store format | schrodeck ↔ schrodeck across hosts **and across schrodeck versions** | us | the store's `FORMAT` file | readers refuse an unknown `FORMAT`; `norm_version` per revision; fork, in-flight and single-writer tests | [store-format.md](store-format.md) (single definition of store paths, records and the write protocol) |
| **E** | CLI `--json` | Go core ↔ any UI or script | us | a `schema_version` field in every JSON document | golden-file tests on JSON output | [cli-json.md](cli-json.md) |

## Rules

- **Ours (A, D, E):** the code and the contract doc change in the same PR. A breaking change bumps the version (`FORMAT` for D, `schema_version` for E). A host that sees a newer `FORMAT` than it understands refuses to write, and says why.
- **Elgato's (B, C):** rows are added or changed only by evidence: a passing `doctor` run, or a cited source in [references.md](../references.md). Hand observations are labeled as such, never as "passed".
- **New OS:** implement A and pass its conformance suite, write that OS's section of B with a probe per assertion, confirm that C holds on that OS (`doctor`), then apply.
