# 0003. Decks are local; compatibility is by geometry

Status: Accepted 2026-10-01. Revised 2026-10-02 (M1, implementation): R8 documents key and dial counts per DeviceType but not the columns × rows split; a physical deck's DeviceType is found through an observed USB product → DeviceType map, and the split is observed and checked against R8's key count. Virtual decks remain in scope; their grid source (U8) is settled in M1.

## Context

Each Stream Deck profile is bound to one device ([R10](../references.md), documented). The same physical deck can move between hosts (KVM/Thunderbolt switch), and different hosts may have different decks of the same model, or virtual decks. The question is what the sync logic needs to know about a deck.

Observed on one Mac (app 7.5.1): the app records each deck it knows in its preferences (`Devices`, keyed by its device id), and each profile manifest carries `Device.Model` and `Device.UUID`. For a physical deck, `Device.UUID` has the shape `@(1)[<vendor>/<product>/<serial>]` and contains the deck's USB serial ([R9](../references.md), observed; the SDK only calls the id a "unique identifier"). Virtual decks have the id `@(0)[]`, with no serial ([R12](../references.md)).

Elgato documents a DeviceType table with each model's key count and dial count ([R8](../references.md), documented); the columns × rows split is not tabulated and is observed.

## Decision

- Each host enumerates **its own** decks from the app's own data (the prefs `Devices` list and profile manifests). The sync logic does **not** use USB or IOKit.
- Two decks are **compatible** when their geometry matches: columns × rows, plus dial/encoder count, from Elgato's DeviceType table. Model names are not compared directly. This makes virtual decks **in scope** when their geometry matches.
- Optionally, when a hardware serial is visible in the device id, record "deck seen on host" sightings in the store. That is **informational only** (for `status`/`log`); no decision depends on it.
- A USB attach event may be used as a trigger accelerator ([0012](0012-triggers.md)), never as a source of truth.
- When the deck a copy is bound to disappears from the app's device list (e.g. a virtual deck expiring), that copy stops syncing and notifies ([0026](0026-profile-identity.md)).

## Consequences

- Good: no dependency on USB enumeration in the core. Works with virtual decks and future device types as long as Elgato's table lists their geometry.
- Good: a profile can follow the user to a *different* physical deck of the same geometry ([0004](0004-shared-profiles-and-subscriptions.md)).
- Bad: the geometry table must be kept in step with Elgato's. A new device type is "unknown geometry" until added, and is then refused (stop, don't guess).
- Risk: two models with the same grid but different key semantics (e.g. touch strip) could be treated as compatible. The dials/encoders field is part of geometry for this reason.
- Risk: `Device.UUID` format is observed, not documented. It is only used to find local decks, and the schema guard ([0015](0015-schema-guard.md)) catches format changes.

## Alternatives considered

- **Key decks by USB serial via IOKit:** ties the core to USB, excludes virtual decks, and adds a platform dependency the app already solves.
- **Key decks by Elgato's `Device.UUID` as an opaque string:** a profile could then never move to a different physical deck.
- **Compare model ids:** distinct product ids can share a grid (e.g. revisions of the 15-key deck), so geometry is the real constraint.

## Verified by

No check yet; to be written in the plan:
- A geometry-table test that fails if a mapped DeviceType lacks columns/rows, or if a grid does not multiply to R8's key count: `TestGeometryTablesAreConsistent` (M1 Task 10), with known-bad tables.
- A subscribe test that fails when binding a 32-key profile to a 15-key deck.

## References

- [R8](../references.md): DeviceType table with key and dial counts (documented); columns × rows are observed
- [R9](../references.md): `Device.UUID` contains the USB serial (observed, one deck, one Mac)
- [R10](../references.md): profiles are device-specific (documented)
- [R12](../references.md): virtual decks, id `@(0)[]` (documented / observed)
