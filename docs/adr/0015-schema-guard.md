# 0015. Schema guard: stop when the app's files change shape

Status: Accepted 2026-10-01. Revised 2026-10-01 (review F5): the guard blocks pushes as well as applies; incoming revisions are checked against this host's known-good fingerprints; the file-pattern set is part of the fingerprint; the app bundle is watched by the resident agent. Revised 2026-10-01 (review round 2: F32, F39): the fingerprint is defined exactly and excludes the app version, so patch updates that don't change the format don't stop sync; the read-only `doctor` pass that M2 needs is separated from M3's app-restarting probes. Revised 2026-10-02 (M1, implementation): key paths collapse array indices to `[]` and the key-slot member names under `Actions` to `*`, and stop at `Settings` (plugin-defined content); paths carry a `manifest:` or `page:` prefix. Whether the content-dependent key set (e.g. `AppIdentifier` only on some profiles) makes the fingerprint flap on ordinary edits is measured in M1 (observation F1) before M2 relies on it.

## Context

Elgato calls file-level profile management "not officially supported and may break with future software updates" ([R3](../references.md), documented). Elgato publishes a JSON schema for **plugin** manifests (`https://schemas.elgato.com/streamdeck/plugins/manifest.json`, returned 200 on 2026-10-01). No **profile** schema was found: the equivalent profiles URL returned 404, and the doc search found none ([R2](../references.md), observed). Profiles carry `"Version": "3.0"` (observed).

## Decision

**The format fingerprint** is a digest over exactly these, computed from profile files only:
1. the top-level manifest `Version` value (`"3.0"` observed);
2. the structural key set: for top-level and page manifests, the sorted set of key paths (with array indices collapsed, e.g. `Controllers[].Actions.*.Settings`), not their values;
3. the set of file-name patterns present, matched against the allow-list ([contract C](../contracts/profile-format.md) P7).

`fingerprint = sha256(JCS({version, key_paths, file_patterns}))`. It is recorded in every revision ([contract D](../contracts/store-format.md)).

**The app version is deliberately not part of the fingerprint** (owner's decision, review F32). Patch releases rarely change the profile format. If the app version were included, a host one patch level behind would refuse every revision from an updated host, and sync would stop in one direction for no format reason. The app version is still recorded, as display metadata in each head, and still triggers re-checking:

- **Local app update** (noticed by the resident agent watching the app bundle, [0012](0012-triggers.md)): the agent recomputes this host's fingerprint over its own profiles, read-only.
  - **Unchanged:** sync continues, with no pause and no prompt. The new app version is added to the verified-versions list in [contract B](../contracts/client-os.md) as "fingerprint unchanged", which is not the same as a passing restart probe.
  - **Changed:** **both pushes and applies pause** for every profile until `schrodeck doctor` passes and the user confirms the new fingerprint as known-good.

- Plugin manifests are validated against Elgato's published schema.
- The resident agent ([0012](0012-triggers.md)) watches the app bundle and notices updates.
- Pushes pause too, not only applies: after an app update that migrated the profile format, L changes on every profile, and pushing would spread the new format to hosts still on the old app (review F5).
- **`doctor` has two tiers** (review F39). The **read-only tier** (M1/M2) recomputes and compares fingerprints and runs every probe that doesn't restart the app; it is enough to accept a fingerprint for **pushing**. The **restart tier** (M3) adds the launch-rewrite and round-trip probes ([contract C](../contracts/profile-format.md) P4, [contract B](../contracts/client-os.md) M4). It is required before the first **apply** under a new fingerprint.
- The apply plan ([0008](0008-two-phase-apply.md)) refuses an incoming revision whose fingerprint is not in **this host's** known-good set (e.g. it was saved by a newer app that changed the format). It notifies once, deduplicated ("*<profile>* was saved in a profile format this Mac hasn't verified; update Stream Deck here and run `schrodeck doctor`") and waits. A local edit on this host to that profile meanwhile is **held, not pushed** (HoldLocal, [0005](0005-direction-detection-three-way-hash.md)), so one out-of-date host never forks the group.

## Consequences

- Good: "stop, don't guess" when Elgato changes the format.
- Good: a patch update that doesn't change the format costs nothing; only a real format change pauses sync.
- Bad: a real format change pauses syncing on the updated host until `doctor` runs, and holds that profile's edits on hosts that haven't updated yet.
- Risk: a semantic change that keeps the same keys and versions (e.g. a field's meaning changes) passes the guard. No structural check can catch that.

## Alternatives considered

- **Best effort (no guard):** an unrecognized format could be rewritten wrongly on every host.
- **Pin to exact app versions only:** too strict. Patch updates rarely change formats, and the fingerprint is more precise.

## Verified by

No check yet; to be written in the plan:
- A fixture with an added unknown key ⇒ guard trips.
- A changed `Version` ⇒ guard trips.
- A tripped guard ⇒ zero pushes and zero applies (filesystem-port assertion), with status still reported.
- An incoming revision with an unknown fingerprint ⇒ the plan refuses it; known-good: a matching fingerprint passes.
- The unchanged fixture ⇒ guard passes (known-good).
- An app-version change with an unchanged fingerprint ⇒ sync continues, no pause. Known-bad: a fingerprint that includes the app version must pause here, and fail this test.
- A host with an unknown incoming fingerprint and a local edit ⇒ no push (HoldLocal), one notification.

## References

- [R2](../references.md): ProfilesV3 layout (observed)
- [R3](../references.md): unsupported, may break (documented)
