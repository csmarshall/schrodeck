# Contract D: store format (schrodeck ↔ schrodeck, across hosts and versions)

The on-disk format of the shared store. **This file is the single definition** of store paths, record fields, the write protocol, how R is derived, and what garbage collection may delete. ADRs and the spec link here and don't restate fields. Owner: us. Versioned by the store's `FORMAT` file. Decided in ADR [0009](../adr/0009-store-write-protocol.md) (protocol), ADR [0005](../adr/0005-direction-detection-three-way-hash.md) (direction), ADR [0025](../adr/0025-deletion-and-unshare.md) (tombstones, reshare) and ADR [0027](../adr/0027-store-lifecycle.md) (migration, GC).

Index of all contracts: [README.md](README.md).

## Invariant: every store file has exactly one writer

Every path written by schrodeck contains the `host_id` of the only host that ever writes it. Two hosts never write the same path, even with identical content. The sync client therefore never has two versions of one file to reconcile, and never needs to make a conflicted copy of a schrodeck file.

Write-once objects that two hosts might both produce (identical revisions, identical trees) are stored **once per writer** under a host-scoped path. A reader accepts any writer's copy that passes its digest check. There are exactly **two exceptions**, each with its own safety argument (review F45):

- **`config.toml`** (common settings): rarely written, with an explicit conflict path (`config resolve`, ADR [0010](../adr/0010-host-identity-and-config-layering.md)).
- **`FORMAT`**: written by `init` when it creates an empty store, and by `migrate` (ADR [0027](../adr/0027-store-lifecycle.md)). It holds one integer. Concurrent `init`s on the same empty store write identical bytes. `migrate` refuses unless `FORMAT` still holds the version it migrates *from*, and writes the new integer last. A reader that finds a provider conflict copy of `FORMAT`, or a value it doesn't know, is read-only and notifies; it never guesses between two values.

`forget-host` (ADR 0027) is the one sanctioned case of a host **deleting** another host's files: it runs only by explicit command, for a host that is retired, so the owner can no longer write them.

Revised 2026-10-01 (review F26): the first version let two hosts write the same `revisions/<id>.json`, `trees/<digest>/` and `tombstone.json`.

Revised 2026-10-02 (issue #5, owner's decision): heads are per **member copy** (`heads/<host_id>/<copy_id>.json`), not per host, so one Mac can hold several peer copies of one setup (one per deck). Every path still contains its only writer's `host_id`. A host writes its store paths from one process at a time (the run lock, ADR [0012](../adr/0012-triggers.md)), so its member copies never race on a path; two copies on one host that produce the identical revision or tree share that host's single write-once, byte-identical copy of it.

## Layout

```
<store>/
  FORMAT                                        integer store format version (text). Written by `init` (empty store) and `migrate`. Multi-writer exception (see above)
  config.toml                                   common config: shared settings only (ADR 0010). Multi-writer exception (see above)
  hosts/<host_id>.toml                          per-host config: friendly name, variable values, subscriptions, pins. Owner: that host
  profiles/<profile_id>/
    profile.json.<host_id>                      write-once at init/share: the setup record {profile_id, geometry, name}. Owner: the host that created the setup from its template (ADR 0029). The template itself is never recorded or stored
    heads/<host_id>/<copy_id>.json              head of one member copy of this profile on that host. Owner: that host
    revisions/<revision_id>/<host_id>.json          revision record, written once by each host that produced it
    trees/<tree_digest>/<host_id>/              stored tree, written once by each host that uploaded it
    tombstones/<host_id>.json                   this host's unshare records (append-only list). Owner: that host
    reshares/<host_id>.json                     this host's reshare records (append-only list). Owner: that host
  icon-packs/<pack_id>/<tree_digest>/<host_id>/ icon-pack trees (ADR 0013)
  scripts/<path_id>/<sha256>.<host_id>          opt-in script copies (ADR 0013)
  events/<host_id>.jsonl                        append-only event trail. Owner: that host (ADR 0017)
  inventory/<host_id>.json                      dependency inventory. Owner: that host (ADR 0013, 0014)
  tmp/<host_id>/                                staging area. Owner: that host; cleaned by that host only
```

`profile_id` is a random UUIDv4 assigned by `share` (ADR [0026](../adr/0026-profile-identity.md)). `host_id` is defined in ADR [0010](../adr/0010-host-identity-and-config-layering.md).

`host_id` and `copy_id` are lower-case text wherever they appear (paths and records): `host_id` is 12 lower-case hex digits, the leading part of `sha256(hardware id + ":" + user name)`, and `copy_id` is a lower-case hyphenated UUID (8-4-4-4-12). Writers emit exactly that form and readers compare it exactly; only the canonical profile folder name is upper case, as the app names its folders.

`copy_id` names one **member copy**: `uuid5(NAMESPACE_SCHRODECK, host_id + ":" + profile_id + ":" + deck_key)` (ADR [0026](../adr/0026-profile-identity.md)). A host can hold several member copies of one profile, one per chosen deck, so heads are kept per copy (issue #5). `deck_key` embeds the deck's USB serial ([R9](../references.md)), so it never appears in a store path or record; only this one-way hash of it does. `copy_id` is opaque but not secret: someone who already knows the `host_id`, the `profile_id` and a candidate serial could confirm the guess, which is acceptable for a folder only the user's own machines replicate. It includes `host_id` so the same physical deck behind a switch gets a different `copy_id` on each Mac, and the store doesn't reveal which hosts share a deck. Heads are grouped under `heads/<host_id>/` so the single writer stays visible in the path and `forget-host` removes one directory. Everything in a `copy_id` is known to the host after losing its local state, so it can find its heads again (ADR [0005](../adr/0005-direction-detection-three-way-hash.md) Recover).

`NAMESPACE_SCHRODECK` = `uuid5(NameSpace_URL, "https://github.com/csmarshall/schrodeck/ns/v1")` = `5a7d742c-c29c-52c8-b996-ed8ccdcb83f8` (RFC 9562 name-based UUIDs). It is derived from that URL rather than chosen at random, and the URL carries a version: a different namespace would change every `copy_id` and canonical folder, so it can only change with a store `FORMAT` migration. The canonical folder name is that UUID in **upper case** plus `.sdProfile`, matching how the app names profile folders. Implemented in `internal/identity`.

## Records

**Revision** (`revisions/<revision_id>/<host_id>.json`, write-once). The file contains **exactly** the fields that make up the id, serialized with RFC 8785 (JCS), so every writer produces byte-identical content:

| field | meaning |
|---|---|
| `hash` | normalized content hash of the tree, per [contract C § normalized hash](profile-format.md#normalized-hash) |
| `tree` | `tree_digest` of the stored tree (below) |
| `parents` | list of parent `revision_id`s: `[]` for the root, one for an ordinary push, two or more for a `resolve` or `rebase` that joins tips |
| `norm_version` | normalization version used for `hash` (integer, ADR [0006](../adr/0006-normalization-and-variables.md)) |
| `fingerprint` | the format fingerprint the source files matched (ADR [0015](../adr/0015-schema-guard.md)) |
| `kind` | `edit` \| `resolve` \| `rollback` \| `rebase` (norm/format migration, not a content change) |

`revision_id = sha256(JCS(revision record))`. Author and time are **not** in the revision. Who wrote a revision, when, and with which app version is per-host metadata: the writer's head (below) and its event line (ADR [0017](../adr/0017-observability.md)). `status` shows "updated by <host>" from the first head and event that reference the revision.

**Head** (`heads/<host_id>/<copy_id>.json`, owned by that host): `{revision_id, updated_at, app_version, deck_model}`. It means "this member copy of the profile is at `revision_id`". `updated_at` (UTC), `app_version` and `deck_model` (the deck's model code from the app's device list, e.g. `20GAT9902`, never its serial; it lets `status` tell two copies on one host apart) are display-only (ADR 0005). A host rewrites a copy's head only after a successful push or apply of that copy, and that copy's B is set only after that write succeeds (review F34).

**Tree** (`trees/<tree_digest>/<host_id>/`, write-once): the full profile directory as the app wrote it, with variables replaced by placeholders (ADR [0006](../adr/0006-normalization-and-variables.md)), **including the reserved placeholder `{{DEVICE}}` in place of every `Device.UUID` value**. A raw device id, which embeds the deck's USB serial ([R9](../references.md)), therefore never reaches the shared folder (review F27). Runtime fields are kept; they are excluded only from `hash`. `tree_digest` = sha256 over the sorted lines `<NFC relative path>\0<sha256(file bytes)>\n` for every file under the allow-list in [contract C](profile-format.md#file-allow-list), computed on the stored (placeholder) bytes. The folder's own name is not part of the relative paths.

**Tombstone and reshare records** (ADR [0025](../adr/0025-deletion-and-unshare.md)). Each host appends to its own list:

- `tombstones/<host_id>.json`: a list of `{record_id, profile_id, at_revision, supersedes: [reshare record_id…], updated_at}`.
- `reshares/<host_id>.json`: a list of `{record_id, profile_id, supersedes: [tombstone record_id…], updated_at}`.

`record_id = sha256(JCS(record without record_id and updated_at))`. Each record lists the records of the other kind it has seen and supersedes. A profile is **Unshared** if any tombstone record is not superseded by some reshare record. A reshare written while another host concurrently unshares does not supersede the unseen tombstone, so the profile stays unshared. That is the safe outcome: nothing is applied, and a second `reshare` fixes it.

**Event line** (`events/<host_id>.jsonl`): `{ts, host_id, profile_id, from_state, to_state, action, revision_ids, trigger, result}` (ADR [0017](../adr/0017-observability.md)).

## Deriving R from the heads

This is evaluated per profile, **after** the freshness check: if the provider does not report the profile's store files as current, the profile is InFlight and nothing below runs (review F30).

1. Read every `heads/*/*.json` (one per member copy, on every host). Resolve each head's `revision_id` and its ancestors through `revisions/` (any writer's copy whose JCS bytes hash to the id). Revisions are never garbage-collected, so a missing revision can only mean the sync client hasn't delivered it yet → **InFlight**.
1a. **Live tip** (review F49): the revision of a live head (after the version filter in rule 4) that is **not an ancestor of another live head's revision**. A stale head sitting on an ancestor is never a tip, even if its hash equals a newer tip's, so it can't win the tie-break in rule 5 and orphan the newer chain.
2. **Equivalence (tips only).** Two **live tips** are *equivalent* (≡) if they are the same revision, or have the same `hash` under the same `norm_version`. Equivalent tips have identical normalized content, so they are never treated as a fork (review F27, owner's decision). Equivalence is **never** evaluated against ancestors (review F40): returning to earlier content (an undo, or a `rollback`) produces a new revision whose hash equals an ancestor's, and that revision must win over the tip it descends from.
3. **Subsumption.** Revision X *subsumes* head H **iff** H's revision is X or an ancestor of X (ancestry only), **or** H's revision and X are both live tips and H's revision ≡ X.
4. **Version filter.** Only heads whose revision's `norm_version` is ≥ this host's current `norm_version` count as live tips. A head at an older `norm_version` is a host that hasn't upgraded yet. It is subsumed through the `rebase` revision's parent link once `migrate` has run, and otherwise ignored for R (review F28). If its revision is **not** an ancestor of a rebase revision (the old host pushed an edit after `migrate`, under the old version), it is a **pending older-version edit**: excluded from R, but never silently ignored. Every upgraded host reports "pending edit from <host> on an older version" in `status` and the event log, and sends one deduplicated notification (review F42, owner's decision). When that host upgrades, R doesn't subsume its `B.revision`, so that copy becomes DETACHED(stale-version-edit) ([ADR 0030](../adr/0030-fail-closed-detach.md)) and `resolve` decides what happens to the edit. If **no** head is at this host's `norm_version` or newer, the store hasn't been migrated to this host's version yet: the profile is **VersionMismatch** (read-only, "run `schrodeck migrate`"). An upgraded host never pushes into an older-`FORMAT` store except through `migrate` (ADR [0027](../adr/0027-store-lifecycle.md)).
5. If one live tip subsumes every live head, **R = that tip**. When several equivalent tips qualify, R is the one with the lowest `revision_id`, so every host picks the same one. (Only equivalent tips can tie: by rule 3, a non-equivalent ancestor-content match no longer subsumes anything, so a rollback revision is the unique R.)
6. Otherwise (two live tips that are neither ancestry-related nor equivalent) → **Forked**. There is no R until a `resolve` revision whose parents include every tip.
7. To apply R, its tree must be present: at least one writer's copy of `trees/<R.tree>/` must pass its digest check. If none does → **InFlight**.

A head belonging to a retired host is simply subsumed by R and changes nothing. No logic counts hosts or copies, or needs a membership list.

**Several copies on one host** are just several heads (issue #5). Nothing above groups heads by host, so two copies on one Mac that both change from the same base produce two tips: the profile is Forked (or the later copy Diverged, depending on the order the host evaluates them), exactly as for two Macs, and neither revision is lost. A copy left unchanged while its sibling pushed is simply Behind and is applied.

**Store went backwards** (review F34): if R does not subsume a copy's own `B.revision` (for example, the copy's head was lost, or a provider restore reverted the store), that copy becomes **DETACHED(store-went-backwards)** ([ADR 0030](../adr/0030-fail-closed-detach.md)) and notifies once. It is never read as Behind, because applying R would silently revert content. `schrodeck resolve` chooses. Detached copies are local state only: a detached copy's head stays where it was and remains an ordinary (older) head for everyone else's R.

## Write protocol (push)

1. Stage the tree in `tmp/<host_id>/<random>/`, re-read it, and verify `tree_digest` and `hash`.
2. Rename it to `trees/<tree_digest>/<host_id>/`. If this host's copy already exists and passes its digest check, delete the staged copy. **Another host's copy is never trusted as a substitute**: it may be mid-GC on its writer or partially delivered (review F25).
3. Write `revisions/<revision_id>/<host_id>.json` via stage + rename (skip only if this host's own copy exists and is valid).
4. Wait (bounded, default 5 minutes) for the provider to report the tree and revision as uploaded (ADR [0023](../adr/0023-store-freshness-via-file-provider.md)). The head is moved only after that, so other hosts don't see a head before its data can reach them. If the provider reports `Unknown` freshness, or the wait times out, move the head anyway: readers' InFlight rule makes that safe. `status` then says "pushed, upload unconfirmed".
5. Rewrite this copy's `heads/<host_id>/<copy_id>.json` **last**, via stage + rename.
6. Only after step 5 succeeds: set this copy's local B := (revision_id, L).

Readers never trust a head whose revision or tree is not fully present and digest-valid. Ordering on other hosts therefore doesn't matter: a head that arrives before its data only yields InFlight.

## Garbage collection: trees only

Owner's decision (review F25). The ancestry walk needs revisions, and revisions are small (a few hundred bytes), so **revision records are never deleted**. GC deletes **tree copies only**, and each host deletes only **its own** copies (`trees/<digest>/<host_id>/`). A tree digest is **pinned**, and none of its copies is deleted, while it is:

- the tree of R or of any head's revision (the tips);
- the tree of any revision within `retention.shared` (Y) generations behind any head (restore points, ADR [0011](../adr/0011-history-and-rollback.md));
- listed in any host's `hosts/<host_id>.toml` `pins` (e.g. a held `rollback --local` target, or a live B awaiting re-materialization, ADR [0006](../adr/0006-normalization-and-variables.md)).

A rollback or re-materialization whose target tree has been collected from the store **falls back to this host's local history ring** (ADR [0011](../adr/0011-history-and-rollback.md)), which already holds B's content and every local restore point (review F44). Only if neither has it does it fail cleanly ("tree collected; choose a newer restore point"). It never applies a partial tree.

## Versioning

- `FORMAT` is an integer. A host reads only the formats it knows and **writes only its own format**. A host that sees a newer `FORMAT` is read-only: it reports status, refuses to push or apply, and notifies once that it needs upgrading (ADR [0027](../adr/0027-store-lifecycle.md)).
- `norm_version` lives in each revision. Hashes are compared only under a matching `norm_version` (ADR [0006](../adr/0006-normalization-and-variables.md)). A change to `norm_version` ships as a `FORMAT` bump with a `rebase` migration (ADR 0027).

## Enforced by

- A reader built for `FORMAT` n refuses `FORMAT` n+1 and writes nothing (test).
- **Single writer:** a store written by several simulated hosts, including two hosts producing the identical revision and tree, contains no path written by more than one host, except `config.toml` and `FORMAT`. Known-bad: the same simulation with host-less revision paths must show a shared path.
- **Rollback / undo to earlier content (review F40):** history A(h0) → C(h1), then host X writes RB(h0) with parent C (a `rollback`, or a manual undo) ⇒ R = RB on **every** host; X stays InSync and no host applies C. **Known-bad:** under the previous rule ("equivalent to one of X's ancestors"), C also subsumes RB (RB ≡ ancestor A) while RB subsumes C (C is its parent), so two non-equivalent tips both qualify. An implementation that picks C reverts the rollback everywhere; one that reports Forked creates a spurious fork. This test must fail against that rule.
- **Fork detection:** two heads with the same parent and different hashes ⇒ Forked on every host. **Equivalence:** two heads with different revision ids but the same hash and `norm_version` ⇒ InSync, not Forked.
- **In flight:** a head whose revision is missing, or whose R tree is missing a file or has a truncated file ⇒ InFlight, no apply. Extra files (`.DS_Store`, `* (conflicted copy)*`, `.icloud` placeholders) are outside the allow-list and don't change `tree_digest`.
- **GC never wedges R:** 50 revisions with Y = 2, then GC on every host ⇒ R still derivable on every host, with no InFlight. Known-bad: a GC that also deletes revisions must wedge this test.
- **Store went backwards:** a copy's own head reverted to an ancestor ⇒ DETACHED(store-went-backwards), no apply, persists until `resolve`.
- **Several copies on one host (issue #5):** one host with two member copies of one profile, both edited from the same B before a run ⇒ both revisions are reachable from a live head, the profile is Forked on every host (or the second copy Diverged, depending on evaluation order), and nothing is applied. **Known-bad:** the same simulation with one head per host (`heads/<host_id>.json`) overwrites the first copy's head with the second's, so the first revision is reachable from no live head; the test must fail against it. And: one copy edited, its sibling untouched ⇒ the sibling goes Behind and is applied, and the edited copy stays InSync.
- **No raw device ids in the store:** every stored tree contains `{{DEVICE}}` and no `@(` device id string, and no store path, head or host file contains a `deck_key` (scan test over a store written from real-shaped fixtures, including a host with two decks on one setup).
