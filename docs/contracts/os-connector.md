# Contract A: Go core ↔ OS connector

Index of all contracts: [README.md](README.md).

Everything an operating system must provide for schrodeck to run on it. **This file is the single source of truth for the port list.** Other documents link here instead of repeating it.

The Go core (ADR [0018](../adr/0018-runtime-and-architecture.md)) contains all sync logic and never imports anything OS-specific. A **connector** is one adapter for each port below. macOS is the reference connector. Windows (or anything else) is a new connector: the core does not change.

Status of the Windows column: **unverified research notes**, not tested. Treat each one as a starting point.

## Ports

Signatures are Go-flavored sketches. The real interfaces live in the code and must match this document. A change to one is a change to both, in the same PR.

### 1. `Paths`: where things live

```go
type Paths interface {
    AppDataRoot() string      // Stream Deck data dir (contains ProfilesV3/)
    ProfilesDir() string      // <AppDataRoot>/ProfilesV3
    PluginsDir() string
    IconPacksDir() string
    StateDir() string         // schrodeck runtime state: B hashes, history, journal
    LogDir() string
    ConfigPointer() string    // optional per-host store-path pointer (ADR 0010)
    Home() string             // value of the built-in {{HOME}} variable (ADR 0006)
    StoreCandidates() []string // auto-detected sync folders, in preference order (ADR 0010)
}
```
| | macOS | Windows (unverified) |
|---|---|---|
| AppDataRoot | `~/Library/Application Support/com.elgato.StreamDeck` [R1](../references.md) | `%APPDATA%\Elgato\StreamDeck` (community sources) |
| StateDir / LogDir | `~/Library/Application Support/schrodeck`, `~/Library/Logs/schrodeck` | `%LOCALAPPDATA%\schrodeck` |
| StoreCandidates | `~/.dropbox/info.json` path, iCloud Drive | Dropbox `info.json` (`%LOCALAPPDATA%\Dropbox`), OneDrive (`%OneDrive%`) |

Invariant: StateDir and ProfilesDir are on volumes where the core can create sibling temp dirs, so that renames are atomic (see Filesystem guarantees).

### 2. `HostIdentity`: stable id for this host and user

```go
type HostIdentity interface {
    HardwareID() (string, error) // stable across renames and OS reinstalls
    UserName() string
    FriendlyName() string        // default display name for the registry
}
```
The core derives `host_id = sha256(HardwareID + ":" + UserName)[:12]` (ADR [0010](../adr/0010-host-identity-and-config-layering.md)). The raw HardwareID is never logged or stored.

| | macOS | Windows (unverified) |
|---|---|---|
| HardwareID | `IOPlatformUUID` | `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid` (note: it changes on OS reinstall, which is weaker than macOS) |
| FriendlyName | `ComputerName` | `COMPUTERNAME` |

### 3. `AppControl`: the Stream Deck app process

```go
type AppControl interface {
    Installed() (bool, error)
    Running() (bool, error)
    Quit(timeout time.Duration) error   // graceful; error if still running at timeout
    Launch() error                      // background, no focus steal
    WaitSettled(quietFor, max time.Duration) error // process up AND ProfilesDir stopped changing
}
```
Invariants: `Quit` must be **graceful**, so the app flushes its in-memory state first, never a kill. `Quit` returning nil guarantees that no app process can write ProfilesDir afterwards. The two-phase apply (ADR [0008](../adr/0008-two-phase-apply.md)) depends on that.

| | macOS | Windows (unverified) |
|---|---|---|
| Quit | `osascript -e 'quit app "Elgato Stream Deck"'`, poll the pid | `WM_CLOSE` to the main window, or the tray-exit path; must not be `TerminateProcess` |
| Launch | `open -gj -a "Elgato Stream Deck"` | `StreamDeck.exe` from its install path, minimized |

### 4. `DeviceEnumerator`: this host's decks

```go
type Deck struct {
    AppDeviceID      string   // key of this deck in the app's prefs device list; local only; the deck_key of ADR 0026
    ManifestDeviceID string   // the Device.UUID value the app writes into profiles bound to this deck; what {{DEVICE}} expands to (ADR 0006)
    Geometry         Geometry // columns, rows, dials: the DeviceType's documented key and dial counts (R8) with an observed grid split; see ADR 0003
    Model      string
    Virtual    bool
    SerialHash string // optional (ADR 0003); "" if unknown. Derived from the deck serial: treat it as an identifier, never log or print it
}
type DeviceEnumerator interface { Decks() ([]Deck, error) }
```
Source: the app's own device list (prefs + manifests), not USB. Same on every OS in principle, but the prefs **format** differs (see `AppPrefs`).

Invariants: the adapter returns every deck, even when two share an `AppDeviceID` (e.g. two virtual decks with the empty id). The core then refuses to target either of them (ADR [0026](../adr/0026-profile-identity.md), review F31). Whether `AppDeviceID` and `ManifestDeviceID` are always the same string is **unverified** (they have the same shape on one Mac); the adapter must return both, and the round-trip probe ([contract B](client-os.md) M4) checks that an installed profile is bound to the intended deck.

Geometry keying (2026-10-02, M1): a physical deck's DeviceType comes from its USB (vendor, product), read from its device key, through an observed product → DeviceType map; its key and dial counts come from R8, and its columns × rows split is observed and must multiply to R8's key count. A product without an observed mapping, or a type without an observed grid, has no geometry, so the deck is not a destination.

### 5. `AppPrefs`: app version and per-deck selected profile (read-only)

```go
type AppPrefs interface {
    AppVersion() (string, error)              // for the schema guard (ADR 0015)
    SelectedProfile(appDeviceID string) (string, error) // ESDProfilesPreferred [R14](../references.md); read-only, never written (ADR 0019)
    DeviceRecords() ([]map[string]any, error) // raw device entries, for DeviceEnumerator; each carries its prefs key under "_key", and an entry that is not a dictionary is returned as {"_key": key, "_raw": value}
}
```
| | macOS | Windows (unverified) |
|---|---|---|
| Store | `~/Library/Preferences/com.elgato.StreamDeck.plist` | registry `HKCU\Software\Elgato Systems GmbH\StreamDeck` (the at-scale article uses this key [R3](../references.md)) |
| AppVersion | app bundle `Info.plist` `CFBundleShortVersionString` | file version of `StreamDeck.exe` |

Observed on macOS (2026-10-02): the `Devices` dictionary also holds one entry whose value is a string, not a device record. The connector keeps it visible as `_raw` (for observations) and the deck list skips it; `inventory` lists it by key and value type only (U7). Record values are JSON-friendly: plist dates become RFC 3339 UTC strings, and plist data becomes `data:<length>:<first 12 hex of its sha256>`, never the bytes, so a report shows whether a blob changed without carrying it.

### 6. `Watcher`: change notification

```go
type Event struct {
    Paths []string // changed paths seen in one debounce window; a hint, never truth
}
type Watcher interface {
    Watch(ctx context.Context, paths []string, debounce time.Duration) (<-chan Event, error) // the channel closes when ctx ends
}
```
Invariant: **events are hints, never truth.** The core always re-hashes, so missed or duplicated events are safe. The timer (via `Scheduler`) is the safety net (ADR [0012](../adr/0012-triggers.md)).

| | macOS | Windows (unverified) |
|---|---|---|
| | **Recursive** FSEvents in the resident agent (launchd `WatchPaths` isn't recursive, ADR [0012](../adr/0012-triggers.md)) | `ReadDirectoryChangesW` with `bWatchSubtree` |

Invariant: watching is **recursive**. A change three directory levels below a watched root must produce an event.

### 7. `Scheduler`: running unattended

```go
type Scheduler interface {
    Install(spec AgentSpec) error   // a resident per-user agent; optional device-attach trigger
    Uninstall() error
    Status() (AgentStatus, error)   // for `doctor` and `install --check`
}
```
| | macOS | Windows (unverified) |
|---|---|---|
| | LaunchAgent plist (`RunAtLoad`, `KeepAlive`) running `schrodeck agent`, which holds the Watcher and the safety timer; IOKit attach notification optional | Task Scheduler (logon + interval) or a per-user startup entry; device-attach via WMI/`RegisterDeviceNotification` (optional) |

### 8. `Notifier`: user-visible notifications

```go
type Notifier interface {
    Available() bool               // false → the core falls back to log-only plus `status`
    Notify(n Notification) error   // title, body, severity, persistent?
}
```
Deduplication is **not** the connector's job. The core sends at most one notification per key `(condition, profile_id, version)` while that condition persists, with an optional reminder interval (ADR [0016](../adr/0016-notifications.md)). A connector only delivers what it's given.
| | macOS | Windows (unverified) |
|---|---|---|
| | Swift helper app in `~/Applications` (ADR [0016](../adr/0016-notifications.md)); it must be installed there, or the OS refuses with no prompt | Toast notifications (needs an AppUserModelID / Start-menu shortcut) |

### 9. `StoreSync`: is the shared folder current?

```go
type Freshness int // Unknown (zero value, fails closed) | Fresh | InFlight | Conflict
type StoreSync interface {
    ReadFreshness(paths []string) (Freshness, error)  // before reading the store
    PushConfirmed(paths []string) (bool, error)       // after writing: uploaded?
    EnsureDownloaded(paths []string) error            // pull online-only placeholders
}
```
`Unknown` is a valid answer: the core then relies only on the store protocol's in-flight check (ADR [0009](../adr/0009-store-write-protocol.md)), and `status` says freshness is unknown.

| | macOS | Windows (unverified) |
|---|---|---|
| | File Provider ubiquitous-item URL keys (ADR [0023](../adr/0023-store-freshness-via-file-provider.md)) | Cloud Files API (`CfGetPlaceholderStateFromFindData`, sync-state properties) as used by OneDrive/Dropbox |

## Filesystem guarantees the core assumes

These are not ports. They are properties a connector must **confirm** (by passing the conformance tests below) or **work around** inside its adapters:

- **Atomic same-volume rename** of a directory onto a fresh name. Windows can't rename over an existing directory, so the core uses rename-aside-then-rename-in. That must work.
- **Exclusive advisory lock** on a lock file (`flock` on macOS; `LockFileEx` on Windows).
- **fsync** of a file and its directory for the apply journal.
- **Path separators and case:** canonicalization uses `/` and is case-sensitive. A case-insensitive filesystem (default on both macOS and Windows) must not produce two store paths that differ only by case.
- **Home-path canonicalization** (`{{HOME}}`) uses `Paths.Home()`, including Windows drive letters and backslashes.
- **Cross-OS sharing is out of scope for v1.** Profiles that embed OS-specific paths or plugins won't work on the other OS even if the sync succeeds. Variables can bridge simple path differences.

## Conformance

Every connector must pass the shared **conformance suite**: tests written once against these interfaces and run against each real adapter on its OS's CI runner. At minimum:

- `AppControl`: quit-then-check-not-running; quit timeout returns an error and leaves the app running; launch + settle.
- `Watcher`: a write produces an event; a write three directory levels deep produces an event (known-bad: a non-recursive watcher fails); a burst within the debounce window produces one event.
- `StoreSync`: a local non-synced file returns `Unknown` (known-bad control); a provider file returns a non-Unknown state.
- `HostIdentity`: stable across two calls and two processes; differs across two users on one host.
  The "differs across two users" case is verified at the derivation level (`TestHostID`), because CI has no second OS user.
- Filesystem guarantees: rename-aside/rename-in under a concurrent reader; the lock excludes a second process; journal fsync survives a simulated crash (kill between steps).

A connector that can't pass a test documents why, and what the core does instead (e.g. `Notifier.Available() == false`).
