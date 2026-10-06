# Contract C: Stream Deck profile format ↔ Go core

The on-disk profile format, as consumed by the platform-neutral core (normalization, hashing, staging). It is **cross-OS**, owned by Elgato, undocumented [R3](../references.md), and versioned by the profile manifest's `Version` field rather than by OS. Each assertion is *documented* or *observed*, with evidence in [references.md](../references.md). `schrodeck doctor` runs every probe; the schema guard (ADR [0015](../adr/0015-schema-guard.md)) refuses an unverified format.

This file is also **the single definition of the normalized hash** and the file allow-list. ADR [0006](../adr/0006-normalization-and-variables.md) links here.

Index of all contracts: [README.md](README.md).

## Assertions

| id | Assertion | Basis | Probe |
|---|---|---|---|
| P1 | Profiles live in `ProfilesV3/<UUID>.sdProfile/` with a top-level `manifest.json` and per-page `Profiles/<page>/manifest.json` + `Images/` | observed [R2](../references.md) | the structural key fingerprint and the set of file-name patterns match the recorded ones |
| P2 | The top-level manifest has `"Version": "3.0"` | observed | read and compare |
| P3 | Top-level `Device.UUID` binds the profile to one deck; `Device.Model` gives the model. It embeds the deck's USB serial, so schrodeck stores it only as the `{{DEVICE}}` placeholder ([contract D](store-format.md)) | observed [R9](../references.md) | present on every profile |
| P4 | Runtime-only fields: action `State`, `Pages.Current`; the app rewrites top-level manifests on launch | observed [R15](../references.md) | **launch-rewrite probe** (M3, because it restarts the app): hash every profile (normalized) → quit → relaunch → settle → re-hash. Normalized hashes must be equal. Any new differing field fails the probe and is reported by key path. |
| P5 | Action settings are plain JSON inside the page manifest; global plugin settings are not in profiles | documented [R7](../references.md) | none needed (documented) |
| P6 | `Open` actions store absolute paths in `Settings.path` | observed [R16](../references.md) | report any absolute path outside `{{HOME}}` |
| P7 | Every file in a profile matches the allow-list below | observed | an unexpected file inside a profile folder trips the schema guard: a new file type means the format changed |
| P9 | Instance identity: every action has an `ActionID`, and images are referenced by file name in `States[].Image`. A copy of the same content gets new `ActionID`s and new image file names | observed [R20](../references.md) | read-only: in a fixture pair (the same page copied twice), the normalized hashes are equal **only if** `ActionID` is dropped and image references are replaced by the image's content hash. Known-bad: hashing without that canonicalization must report the pair as different. Run by `deckformat/probe` as the combined `P9+P11` self-test: it hashes only the built-in fixture pair, so it cannot fail on a real host; its known-bad lives in `TestEachCanonicalizationIsNeeded` (`deckformat/normhash`). |
| P10 | Two profiles on one host may carry the **same** `ActionID`s | **unknown; informational only** (review F51) | not a gate: every install **always regenerates** `ActionID`s deterministically (ADR [0026](../adr/0026-profile-identity.md)), so schrodeck never creates duplicates, not even between a template and its first member copy. The restart-tier probe still records what the app does with a duplicate, for the record |
| P8 | Actions that switch to or open **another profile** (e.g. a switch-profile action) reference the target by that profile's **folder UUID**, and may also embed a device id | **likely, unverified** (review F46) | scan action settings for UUID-shaped values that match another local `.sdProfile` folder name, and for `@(` device ids. Report each reference with its key path. A matched profile reference is an inventory dependency (ADR [0013](../adr/0013-sync-scope-and-scripts.md)); a device id other than this copy's own makes the copy **DETACHED(foreign-device-id)** (ADR [0030](../adr/0030-fail-closed-detach.md), review F56) |
| P11 | Page folder UUIDs are per copy: the same page in two profiles has different `Profiles/<page>` folder names. The ordered `Pages.Pages` list and `Pages.Default` refer to pages by those UUIDs | observed [R20](../references.md) (the copied page's folder UUID differed) | read-only: in the fixture pair, the normalized hashes are equal only with page canonicalization (step 2 below). Known-bad: hashing raw page UUIDs reports the pair as different. How a **folder** button references its sub-page is still unknown (config model U-row P9), so folder sub-pages are not canonicalized yet. Run by `deckformat/probe` as the combined `P9+P11` self-test: it hashes only the built-in fixture pair, so it cannot fail on a real host; its known-bad lives in `TestEachCanonicalizationIsNeeded` (`deckformat/normhash`). |
| P12 | Manifests are compact JSON: no indentation or other insignificant whitespace, members in the order the app wrote them, no trailing newline | observed by hand (one Mac, app 7.5.1, three profiles, 2026-10-02; R-row added in Task 12) | the loader (P1) re-encodes every manifest and refuses any that do not round-trip byte for byte, naming the file; a formatting change by the app therefore fails P1 with a not-round-trip error instead of being rewritten |

## File allow-list

Relative to the `<UUID>.sdProfile/` folder. The folder's own name is never part of a path.

- `manifest.json`
- `Images/*`
- `Profiles/<page>/manifest.json`
- `Profiles/<page>/Images/*`

Known junk is **ignored silently** for hashing and copying: `.DS_Store`, sync-client artifacts (`* (conflicted copy)*`, `*.icloud`, `~$*`), and editor temp files (`*~`, `.*.swp`, `#*#`). Any other file outside the allow-list trips P7, because it may be app data we don't understand.

## Normalized hash

`hash` is what direction detection compares (ADR [0005](../adr/0005-direction-detection-three-way-hash.md)). It must change on a real user edit and on nothing else. The current definition is `norm_version = 1` (revised 2026-10-01, review F55):

1. **Inputs are the manifests only:** the top-level `manifest.json` and every allow-listed `Profiles/<page>/manifest.json`. Image files are not hashed on their own; an image counts only through a reference to it (step 3), so an **orphaned** image file (unreferenced, which the app may leave behind and clean up later) never changes the hash. A reference to an image file that doesn't exist hashes as `missing:<referenced name>`. (In the store, a missing file is also a `tree_digest` mismatch and therefore InFlight.)
2. **Canonical page names.** Each page folder is relabeled for hashing: a page listed in `Pages.Pages` becomes `page/<index>` (its position in that ordered list), the page `Pages.Default` points to becomes `default`, and any other page folder (e.g. a folder button's sub-page, whose encoding is still unknown, P11) is labelled `other/<lower-case UUID>`. Every occurrence of a relabeled page UUID inside the manifests (`Pages.Pages`, `Pages.Default`, and any action-settings value equal to a page UUID of this profile) is replaced by its label. Page-UUID relabeling applies to `Pages.Pages`, `Pages.Default` and action `Settings` values only, not to other string fields. UUID matching is case-insensitive. **Why this is sound:** page folder UUIDs change on copy (P11) while the ordered page list is user-meaningful, so reordering pages is still a real change. Paths use `/` and Unicode **NFC**.

   Structural errors, not hashed around: a page listed in `Pages.Pages` or named by `Pages.Default` that has no folder is an error; a page listed twice in `Pages.Pages` is an error; a `Pages.Default` that is also listed in `Pages.Pages` is an error; a non-string `Pages.Default` or `Pages.Pages` entry is an error.
3. For each manifest:
   - parse it as JSON;
   - remove the strip-list fields (action `State`, `Pages.Current`, top-level `Device.UUID`, and every action's **`ActionID`** [R20](../references.md)), which are one named constant in the code;
   - replace every image **reference** (`States[].Image` and any other `Images/<file>` value) with the referenced file's content hash (`sha256` of its bytes), so identical images under different file names hash the same and two buttons that swap images change the hash [R20](../references.md);
   - **canonicalize it with RFC 8785 (JSON Canonicalization Scheme)**.

   A stored tree is already in placeholder form (variables and `{{DEVICE}}`). A local copy is first put into placeholder form (ADR 0006).
4. `hash = sha256` over the lines `<canonical path>\0<sha256(canonical bytes)>\n`, sorted bytewise by canonical path. Each line digest and the final hash are written as lower-case hex.

**What this guarantees:** two copies of the same setup whose pages are all in `Pages.Pages` hash equal regardless of folder UUIDs, `ActionID`s, image file names, and orphaned images. Setups that contain folder sub-pages hash equal only if those sub-page UUIDs match (until P11's folder encoding is known); copies made by schrodeck keep page UUIDs, so this affects only independently made copies.

Any change to steps 1–4, including the strip list, increments `norm_version` and ships as a store `FORMAT` migration (ADR [0027](../adr/0027-store-lifecycle.md)). Hashes are only ever compared under the same `norm_version`.

The store's `tree_digest` uses the same file set and line format, but over the **raw** bytes with nothing stripped ([contract D](store-format.md)).
