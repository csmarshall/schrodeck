# 0024. Five documented contracts, each with an owner and an enforcement mechanism

Status: Accepted 2026-10-01

## Context

ADR [0018](0018-runtime-and-architecture.md) splits schrodeck into a platform-neutral Go core and per-OS connectors. That creates several boundaries, and they fail in different ways:

- some **we own** and change deliberately: core ↔ connector, the shared store between hosts, the `--json` CLI;
- some **Elgato owns**. They are undocumented and unsupported [R3], and can change with any app update: how the app behaves on each OS, and the profile file format.

Mixing them hides which kind of breakage happened, leaves a would-be Windows porter guessing what to build, and lets facts drift between documents. The port count already drifted twice in one day (six, seven, nine) before this rule existed.

## Decision

Five contracts, indexed in [docs/contracts/README.md](../contracts/README.md), each with exactly one home:

| | Contract | Owner | Enforced by |
|---|---|---|---|
| **A** | Go core ↔ OS connector: the ports (**the single source of truth for the port list**), invariants, filesystem guarantees ([os-connector.md](../contracts/os-connector.md)) | us | conformance suite run against each real adapter on its OS's CI |
| **B** | OS connector ↔ the Stream Deck app on that OS: paths, prefs, process behavior ([client-os.md](../contracts/client-os.md)) | Elgato (observed) | `schrodeck doctor` probes + schema guard (ADR [0015](0015-schema-guard.md)) |
| **C** | Go core ↔ the app's profile format; cross-OS, versioned by manifest `Version` ([profile-format.md](../contracts/profile-format.md)) | Elgato (observed) | `doctor` launch-rewrite and round-trip probes + schema guard |
| **D** | schrodeck ↔ schrodeck across hosts and schrodeck versions: the store format ([store-format.md](../contracts/store-format.md)) | us | `FORMAT` version; readers refuse unknown formats; compatibility tests |
| **E** | Go core ↔ any UI or script: the `--json` CLI | us | `schema_version` in every document; golden-file tests |

- Other documents **link** to the contracts. They never restate port lists, counts, or assertions.
- **Ours (A, D, E):** the code and the contract change in the same PR, and a breaking change bumps the version.
- **Elgato's (B, C):** a row changes only with evidence, either a passing `doctor` run or a source in [references.md](../references.md). references.md holds *why we believe it*; contracts B and C hold *what we check*.
- **Porting to a new OS** = implement A and pass conformance, write that OS's section of B with a probe per assertion, confirm C on that OS with `doctor`, then apply.

## Consequences

- Good: a porter gets a complete, testable checklist.
- Good: an Elgato update breaks B or C visibly (the guard refuses and `doctor` names the failing probe) instead of silently corrupting profiles.
- Good: mixed schrodeck versions sharing one store are an explicit, tested case (D), not an accident.
- Bad: more documents to keep true. Mitigations: same-PR rule for ours; evidence-only rule for Elgato's.
- D was extracted into `docs/contracts/store-format.md` after the review (F22), as the single definition of store fields. E moved into [cli-json.md](../contracts/cli-json.md) when M0 defined it (issue #3).
- Risk: A's port boundaries may still be wrong for Windows in ways only a real Windows connector will reveal.

## Alternatives considered

- **One "platform" document:** mixes what we control with what we only observe; different change triggers, different tests.
- **Two contracts** (connector, client): the first cut of this ADR. Rejected because it buried the cross-OS profile format inside a per-OS document, and it omitted the two contracts we own that other parties depend on (store format between versions, `--json` for UIs).
- **Contracts only in code:** fine as the authority for A, D and E, but a porter reads prose first, and B and C need evidence and basis in prose.

## Verified by

No check yet; to be written in the plan:
- A: the conformance-suite skeleton runs against fake adapters in CI. Known-bad: a fake that violates `Quit`'s guarantee must fail it. A CI check also compares the port names in `os-connector.md` with the Go interfaces.
- C: `doctor`'s launch-rewrite probe, seen to fail when a fake runtime field is injected.
- D: a test where a reader built for `FORMAT` n refuses `FORMAT` n+1 and writes nothing.
- E: golden JSON files for `status`, `history`, `join --json`.

## References

- [R3](../references.md): file-level management is unsupported (documented).
- [R1, R2, R7, R9, R14, R15, R16](../references.md): the facts contracts B and C turn into assertions.
