# M1 Read-only Insight (the Format Toolkit) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `deckformat`, the standalone Stream Deck format toolkit of ADR 0031 (lossless parser, contract C hasher, semantic diff, redaction and fixture export, observation harness, probe runner), wire it into read-only `schrodeck status`, `inventory`, `doctor`, `observe` and `fixture export` on macOS, then use it on a real Mac to settle the format unknowns before any write path exists.

**Architecture:** `deckformat` (nested module, no OS code; it reads the app's files only through `fs.FS`, and its `observe`, `redact` and `pathguard` packages write snapshots and exports with `os`, only to paths the caller names) holds everything about the file format. The root module adds platform-neutral identity and deck logic, a read-only macOS connector (prefs via `defaults export`, `ioreg`, `pgrep`), a `doctor` package that assembles contract B and C probes plus the ADR 0015 fingerprint check, and new CLI commands. Nothing writes the Stream Deck app's files or restarts the app; schrodeck writes only its own state dir and paths the user names with `--out`.

**Tech Stack:** Go 1.27.1; stdlib `encoding/json/jsontext` (ordered tokens, RFC 8785 canonicalization; available without GOEXPERIMENT in 1.27); `golang.org/x/text` v0.42.0 (Unicode NFC, deckformat); `howett.net/plist` v1.0.1 (BSD-style licence, macOS connector only); Python 3 stdlib for one independent reference implementation used to compute a golden value.

**Spec:** [docs/specs/2026-10-01-sync-design.md](../../specs/2026-10-01-sync-design.md). Decisions: [ADR 0031](../../adr/0031-format-toolkit-module.md) (toolkit), [ADR 0015](../../adr/0015-schema-guard.md) (fingerprint), [ADR 0006](../../adr/0006-normalization-and-variables.md) and [contract C](../../contracts/profile-format.md) (hash), [ADR 0010](../../adr/0010-host-identity-and-config-layering.md) and [ADR 0026](../../adr/0026-profile-identity.md) (identity), [ADR 0003](../../adr/0003-decks-are-local-geometry-compatibility.md) (decks). Delivery order: [docs/adr/README.md](../../adr/README.md#delivery-order), row M1. Builds on [the M0 plan](2026-10-02-m0-foundations.md), which must be merged first.

## Global Constraints

- Everything in the M0 plan's Global Constraints applies (public repo and placeholders, no literal backslash-u escapes, Go 1.27.1 pinned in `go.mod`, MPL-2.0 header, OS-free core, `deckformat` imports nothing from the root module, contract A is the only home of the port list, contract E rules, exit codes, logging, shell rules, known-bad inputs for every detector, no hard-wrapped prose).
- **M1 writes nothing to the Stream Deck app's files and never quits or launches the app.** `deckformat` reads the app's files only through `fs.FS`. The only writes are schrodeck's own state dir (`known-fingerprints.json`, observation snapshots that are deleted on `observe stop`), and paths the user passes with `--out`. Every such path is checked by `pathguard.RefuseInside` on its **final** target (symlinks and `../` resolved, letter case ignored) against the app data root and the profiles directory, and `--name` must be a single path element (Task 6, Task 11).
- **Tests never read a real Stream Deck install by default.** The one test that does (`TestLiveReadOnly`, Task 10) runs only with `SCHRODECK_LIVE=1`, so `go test ./...` on a development Mac touches nothing personal unless asked to.
- **Observation reports carry no real ids:** profile, page and action UUIDs become stable pseudonyms (`profile-1`, `profile-1/page/0`, `uuid-3`; Task 7), and serials are removed wherever they appear, not only inside a device id (Task 6).
- **The selected profile is never read for sync and never written** (ADR 0019); `doctor`'s M2 probe and observation snapshots read it, read-only.
- **Device ids embed USB serials (R9):** commands redact them by default (`inventory --show-ids` is the only opt-out), observation reports and fixtures are redacted, and no fixture in the repository contains a real one (fixtures use `@(1)[4057/143/<deck>]`).
- **Unknown data is never dropped:** a manifest that cannot be re-encoded byte for byte is an error, not a best effort.
- Hash definition: contract C `norm_version = 1`, implemented exactly; any change to it is a contract C change plus a `norm_version` bump, never a silent code change.
- `copy_id`, canonical folders and `host_id` are derived (ADRs 0010, 0026, contract D); the only fixed input is the namespace URL, which is versioned in its path.
- Workflow per issue: `gh issue create` → worktree `~/work/claude/schrodeck-worktrees/<n>-<slug>` from `origin/main` → commits `feat(#n): …` / `docs(#n): …` → PR with `Closes #n` → green CI → code-review subagent (must not `open` anything, must end with DECISIONS NEEDED) → owner review → squash merge → `git pull` in `~/work/personal/schrodeck`. Every `git add` names its paths (never `git add -A`: command logs and other stray files must not be staged). Every PR body ends with the executing session's attribution link (the planning session's is shown in Task 3). M1 is six issues/PRs, merged in order:

  | Issue (title) | Tasks |
  |---|---|
  | deckformat: lossless manifest model and profile loader | 1, 2, 3 |
  | deckformat: normalized hash, norm_version 1 | 4 |
  | deckformat: semantic diff, redaction and the write guard | 5, 6 |
  | deckformat: observation harness and probe runner | 7, 8 |
  | schrodeck: identity, read-only macOS connector, inventory/status/doctor/observe/fixture | 9, 10, 11 |
  | Settle format unknowns on a real Mac (U1–U10, P8, P10, P11, F1, geometry; issues #1, #2) | 12 |

## Review Focus

1. **Real manifests carry content the fixtures lack** (float literals such as `1.50`, non-ASCII titles, escaped strings, unknown members): each must round-trip byte for byte or fail closed with an error that names the file, never be silently rewritten. Pinned by `TestRoundTripPreservesEverything` and `TestParseRefusesWhatItCannotReproduce` (Task 1), `TestUnknownFieldSurvivesRoundTrip` (Task 2), and Task 12 step 1 (doctor's P1 on the real profiles). The plan's code was run read-only against the development Mac on 2026-10-02: all three real profiles loaded and round-tripped.
2. **The app's prefs `Devices` dictionary holds an entry that is not a device record** (observed: one string-valued entry), and several virtual decks may share `@(0)[]`: the deck list must skip the former, mark the latter as non-destinations, and never crash. Pinned by `TestDeviceRecordsKeepsOddEntriesVisible` and `TestEnumerateAndAnnotate` (Task 10).
3. **The ADR 0015 fingerprint may flap on ordinary edits**, because the key-path set depends on content (a smart profile's `AppIdentifier`, a title colour that only appears once set): that would pause sync on normal use. `TestSchemaDependsOnOptionalKeys` (Task 3) documents the hazard; Task 12 observation F1 measures it on a real Mac before M2 relies on it.
4. **Page folders are upper-case on disk while every reference to them is lower-case** (observed on all three real profiles): loading and hashing must match case-insensitively. Pinned by `TestPagesAreKeyedCaseInsensitively` (Task 2) and `TestCopyHashesEqual` (Task 4).
5. **Redaction misses** (a bare serial outside any device id, in a settings value or a prefs field; a home path inside a `file://` URL; the Mac's name in a URL; a token under a setting named like a secret; a real profile or page UUID in a committed report): reports and fixtures must pass the repository leak scan. Pinned by `TestString`, `TestBareSerialsAreRedacted` and `TestExportFixtureLeavesNothingPersonal`, each with a known-bad control (Task 6), `TestCompareReportsAndRedacts` and `TestReportsCarryNoRealIDs` (Task 7), and `TestInventoryRedactsDeviceIDsByDefault` (Task 11).

---

## File structure

```
deckformat/
├── go.mod                         + golang.org/x/text
├── jsondoc/   jsondoc.go (+_test)        ordered JSON tree, byte-exact round trip, JCS
├── fixture/   fixture.go                 synthetic profiles as fstest.MapFS (no real data)
├── profile/   profile.go schema.go (+_test)   loader, allow-list (P1, P7), accessors, ADR 0015 schema/fingerprint
├── normhash/  normhash.go (+_test), testdata/refhash.py   contract C hash, independent Python reference
├── semdiff/   semdiff.go (+_test)        changes in Stream Deck terms (raw and semantic)
├── redact/    redact.go (+_test)         scrub identifiers (incl. bare serials), synthetic images, fixture export
├── pathguard/ pathguard.go (+_test)      refuse writes inside protected roots (symlinks, ../, case)
├── observe/   observe.go (+_test)        snapshot / settle / save / load / compare / report (UUIDs → pseudonyms)
└── probe/     probe.go contractc.go (+_test)   probe runner, contract C read-only probes
internal/
├── identity/  identity.go (+_test)       host_id, NAMESPACE_SCHRODECK, copy_id, canonical folder
├── decks/     decks.go (+_test)          device keys, R8 DeviceType table + observed product map, destinations, unmatched
├── host/      host.go                    the ports one command run needs
├── connector/ connector_darwin.go connector_other.go
│   └── macos/ parse.go (+_test) macos_darwin.go (+_test)
├── doctor/    doctor.go (+_test)         contract B probes, fingerprint known-good set
└── cli/       cli.go commands.go observe.go json.go (+_test, golden files)
tools/observe-m1.sh                                                                          (Task 12)
docs/
├── contracts/ os-connector.md profile-format.md store-format.md cli-json.md client-os.md   (edited)
├── adr/0003-…, 0015-schema-guard.md, 0026-profile-identity.md                              (edited)
├── observations/*.md                                                                        (Task 12)
└── references.md, streamdeck-config-model.md                                                (Task 12)
```

---

### Task 1: `jsondoc`, the lossless JSON tree

Issue: "deckformat: lossless manifest model and profile loader". Create the worktree first:

```bash
cd ~/work/personal/schrodeck && git fetch origin
gh issue create --title "deckformat: lossless manifest model and profile loader" --body "M1 Tasks 1-3 of docs/superpowers/plans/2026-10-02-m1-format-toolkit.md: jsondoc (byte-exact round trip), synthetic fixtures, profile loader with contract C allow-list (P1, P7), ADR 0015 schema fingerprint."
# use the issue number it prints as <n>
git worktree add -b <n>-deckformat-model ~/work/claude/schrodeck-worktrees/<n>-deckformat-model origin/main
cd ~/work/claude/schrodeck-worktrees/<n>-deckformat-model
```

**Files:**
- Create: `deckformat/jsondoc/jsondoc.go`, `deckformat/jsondoc/jsondoc_test.go`

**Interfaces:**
- Produces (package `github.com/csmarshall/schrodeck/deckformat/jsondoc`): `Kind` (`Null, Bool, Number, String, Object, Array`); `Member{Name string; Value *Value}`; `*Value` with `Parse([]byte) (*Value, error)`, `ErrNotRoundTrip`, `Encode() []byte`, `Canonical() ([]byte, error)`, `Kind()`, `Raw() []byte`, `Str() (string, bool)`, `Members() []Member`, `Items() []*Value`, `Get(name) *Value`, `Lookup(path ...string) *Value`, `Set(name, *Value)`, `Delete(name) bool`, `RenameMember(i int, name string)`, `SetString(s)`, `NewString(s) *Value`, `Clone() *Value`, `Walk(func(path []string, v *Value))` (array elements appear as `"[i]"`), `FromAny(any) (*Value, error)`.

Why not `encoding/json`: decoding into structs or maps drops unknown members or rewrites literals (`1.50` → `1.5`), and ADR 0031 requires unknown fields to survive. `TestNaiveDecodeWouldLoseData` shows the hazard is real.

- [ ] **Step 1: Write the failing tests**

`deckformat/jsondoc/jsondoc_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jsondoc

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// bs is a backslash. Escape sequences in test inputs are assembled from it so
// no tool in the authoring path can rewrite them into the characters they
// stand for.
const bs = "\x5c"

func TestRoundTripPreservesEverything(t *testing.T) {
	inputs := []string{
		// unknown members, float literals, escapes, non-ASCII, nesting
		`{"b":1.50,"a":"x` + bs + `/y","c":[1,{"z":true}],"d":null,"e":"Café ☕","f":"` + bs + `u00e9","g":[],"h":{},"i":-1e2,"Unknown":{"Deep":[0.10]}}`,
		// the shape of a real page manifest
		`{"Controllers":[{"Actions":{"0,0":{"ActionID":"11111111-0000-4000-8000-000000000001","Settings":{},"State":0}},"Type":"Keypad"}],"Icon":"","Name":""}`,
	}
	for _, in := range inputs {
		v, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("Parse(%s): %v", in, err)
		}
		if got := string(v.Encode()); got != in {
			t.Fatalf("Encode changed the document:\n got %s\nwant %s", got, in)
		}
	}
}

// Known-bad: a typed-struct or map[string]any parser drops or rewrites data.
// This is what Parse protects against; the test shows the hazard is real.
func TestNaiveDecodeWouldLoseData(t *testing.T) {
	in := `{"b":1.50,"Unknown":1}`
	var m map[string]any
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(m)
	if string(out) == in {
		t.Fatal("expected encoding/json to rewrite 1.50 or reorder members; the round-trip guarantee would then be untested")
	}
}

func TestParseRefusesWhatItCannotReproduce(t *testing.T) {
	cases := map[string]struct {
		in           string
		notRoundTrip bool
	}{
		"indented":            {"{\"a\": 1}", true},
		"trailing newline":    {"{\"a\":1}\n", true},
		"escaped member name": {`{"a` + bs + `u0041":1}`, true},
		"duplicate names":     {`{"a":1,"a":2}`, false},
		"invalid UTF-8":       {"{\"a\":\"\xff\"}", false},
		"truncated":           {`{"a":1`, false},
		"two values":          {`{}{}`, false},
	}
	for name, c := range cases {
		_, err := Parse([]byte(c.in))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if errors.Is(err, ErrNotRoundTrip) != c.notRoundTrip {
			t.Errorf("%s: error %v, want ErrNotRoundTrip=%v", name, err, c.notRoundTrip)
		}
	}
}

func TestAccessorsAndMutation(t *testing.T) {
	v, err := Parse([]byte(`{"Pages":{"Current":"x","Pages":["a","b"]},"Name":"N"}`))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := v.Lookup("Pages", "Current").Str(); !ok || s != "x" {
		t.Fatalf("Lookup Current = %q %v", s, ok)
	}
	if n := len(v.Lookup("Pages", "Pages").Items()); n != 2 {
		t.Fatalf("Items = %d", n)
	}
	c := v.Clone()
	c.Lookup("Pages").Delete("Current")
	c.Set("Name", NewString("M"))
	c.Set("New", NewString("y"))
	c.Lookup("Pages", "Pages").Items()[0].SetString("z")
	if got := string(c.Encode()); got != `{"Pages":{"Pages":["z","b"]},"Name":"M","New":"y"}` {
		t.Fatalf("mutated = %s", got)
	}
	if got := string(v.Encode()); got != `{"Pages":{"Current":"x","Pages":["a","b"]},"Name":"N"}` {
		t.Fatalf("Clone shares state with the original: %s", got)
	}
}

func TestWalkPaths(t *testing.T) {
	v, _ := Parse([]byte(`{"a":[{"b":1}],"c":2}`))
	var paths []string
	v.Walk(func(p []string, _ *Value) { paths = append(paths, strings.Join(p, ".")) })
	if got := strings.Join(paths, " | "); got != " | a | a.[0] | a.[0].b | c" {
		t.Fatalf("Walk paths = %q", got)
	}
}

func TestCanonicalIsJCS(t *testing.T) {
	v, _ := Parse([]byte(`{"b":1.50,"a":"x` + bs + `/y"}`))
	c, err := v.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if string(c) != `{"a":"x/y","b":1.5}` {
		t.Fatalf("Canonical = %s", c)
	}
}

func TestFromAny(t *testing.T) {
	v, err := FromAny(map[string]any{"z": []any{int64(1), uint64(2), 1.5, true, nil}, "a": "s"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(v.Encode()); got != `{"a":"s","z":[1,2,1.5,true,null]}` {
		t.Fatalf("FromAny = %s", got)
	}
	if _, err := Parse(v.Encode()); err != nil {
		t.Fatalf("FromAny output does not round-trip: %v", err)
	}
	if _, err := FromAny(struct{}{}); err == nil {
		t.Fatal("unsupported type accepted")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd deckformat && go test ./jsondoc/`
Expected: FAIL, `undefined: Parse` (and the other identifiers).

- [ ] **Step 3: Implement**

`deckformat/jsondoc/jsondoc.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package jsondoc is an ordered JSON tree that keeps every scalar exactly as
// it was written (number literals, string escapes) and every unknown member.
// Parse refuses any input it could not re-encode byte for byte, so code that
// reads a Stream Deck manifest and writes it back can never silently change
// data it does not understand (ADR 0031: unknown fields are preserved).
package jsondoc

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
)

// Kind is a JSON value's type.
type Kind byte

// Value kinds.
const (
	Null Kind = iota
	Bool
	Number
	String
	Object
	Array
)

// Member is one name/value pair of an object, in document order.
type Member struct {
	Name  string
	Value *Value
}

// Value is one JSON value. Scalars keep their literal bytes.
type Value struct {
	kind    Kind
	raw     []byte
	members []Member
	items   []*Value
}

// ErrNotRoundTrip means the input is valid JSON but re-encoding the parsed
// tree would not reproduce it byte for byte (for example it is indented, has
// a trailing newline, or escapes an object member name).
var ErrNotRoundTrip = errors.New("jsondoc: input does not re-encode byte-identically")

// Parse reads one JSON value. Duplicate member names and invalid UTF-8 are
// errors.
func Parse(data []byte) (*Value, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	v, err := readValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("jsondoc: data after the top-level value")
	}
	if !bytes.Equal(v.Encode(), data) {
		return nil, ErrNotRoundTrip
	}
	return v, nil
}

func readValue(dec *jsontext.Decoder) (*Value, error) {
	switch dec.PeekKind() {
	case '{':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		v := &Value{kind: Object}
		for dec.PeekKind() != '}' {
			tok, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			name := tok.String() // a token is only valid until the next read
			child, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			v.members = append(v.members, Member{Name: name, Value: child})
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return v, nil
	case '[':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		v := &Value{kind: Array}
		for dec.PeekKind() != ']' {
			child, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			v.items = append(v.items, child)
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return v, nil
	default:
		raw, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		v := &Value{raw: bytes.Clone(raw)}
		switch raw.Kind() {
		case 'n':
			v.kind = Null
		case 't', 'f':
			v.kind = Bool
		case '"':
			v.kind = String
		default:
			v.kind = Number
		}
		return v, nil
	}
}

// Encode returns compact JSON: members in document order, scalars as read.
func (v *Value) Encode() []byte { return v.appendTo(nil) }

func (v *Value) appendTo(b []byte) []byte {
	switch v.kind {
	case Object:
		b = append(b, '{')
		for i, m := range v.members {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendQuoted(b, m.Name)
			b = append(b, ':')
			b = m.Value.appendTo(b)
		}
		return append(b, '}')
	case Array:
		b = append(b, '[')
		for i, it := range v.items {
			if i > 0 {
				b = append(b, ',')
			}
			b = it.appendTo(b)
		}
		return append(b, ']')
	default:
		return append(b, v.raw...)
	}
}

// appendQuoted quotes s. Names and strings in a tree come from Parse (valid
// UTF-8) or NewString, so AppendQuote cannot fail here.
func appendQuoted(b []byte, s string) []byte {
	out, err := jsontext.AppendQuote(b, s)
	if err != nil {
		panic(fmt.Sprintf("jsondoc: invalid UTF-8 in %q", s))
	}
	return out
}

// Canonical returns the RFC 8785 (JCS) form of the value.
func (v *Value) Canonical() ([]byte, error) {
	c := jsontext.Value(v.Encode())
	if err := c.Canonicalize(); err != nil {
		return nil, err
	}
	return c, nil
}

// Kind returns the value's type.
func (v *Value) Kind() Kind { return v.kind }

// Raw returns a scalar's literal bytes (nil for objects and arrays).
func (v *Value) Raw() []byte { return v.raw }

// Str returns a string value's text.
func (v *Value) Str() (string, bool) {
	if v == nil || v.kind != String {
		return "", false
	}
	out, err := jsontext.AppendUnquote(nil, v.raw)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// Members returns an object's members in order. The slice is shared with the
// value: use RenameMember, Set and Delete to change it.
func (v *Value) Members() []Member {
	if v == nil {
		return nil
	}
	return v.members
}

// Items returns an array's elements in order (shared with the value).
func (v *Value) Items() []*Value {
	if v == nil {
		return nil
	}
	return v.items
}

// Get returns the named member of an object, or nil.
func (v *Value) Get(name string) *Value {
	if v == nil || v.kind != Object {
		return nil
	}
	for _, m := range v.members {
		if m.Name == name {
			return m.Value
		}
	}
	return nil
}

// Lookup follows a path of member names from v; nil if any step is missing.
func (v *Value) Lookup(path ...string) *Value {
	cur := v
	for _, name := range path {
		cur = cur.Get(name)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// Set replaces the named member's value, or appends the member.
func (v *Value) Set(name string, val *Value) {
	for i, m := range v.members {
		if m.Name == name {
			v.members[i].Value = val
			return
		}
	}
	v.members = append(v.members, Member{Name: name, Value: val})
}

// Delete removes the named member and reports whether it existed.
func (v *Value) Delete(name string) bool {
	for i, m := range v.members {
		if m.Name == name {
			v.members = append(v.members[:i], v.members[i+1:]...)
			return true
		}
	}
	return false
}

// RenameMember renames the i-th member.
func (v *Value) RenameMember(i int, name string) { v.members[i].Name = name }

// SetString turns v into a string value holding s.
func (v *Value) SetString(s string) {
	*v = Value{kind: String, raw: appendQuoted(nil, s)}
}

// NewString returns a string value.
func NewString(s string) *Value { return &Value{kind: String, raw: appendQuoted(nil, s)} }

// Clone returns a deep copy.
func (v *Value) Clone() *Value {
	if v == nil {
		return nil
	}
	c := &Value{kind: v.kind, raw: bytes.Clone(v.raw)}
	for _, m := range v.members {
		c.members = append(c.members, Member{Name: m.Name, Value: m.Value.Clone()})
	}
	for _, it := range v.items {
		c.items = append(c.items, it.Clone())
	}
	return c
}

// Walk calls fn for v and every value below it, depth first, with the path
// of member names and array indices ("[3]") leading to it.
func (v *Value) Walk(fn func(path []string, v *Value)) { v.walk(nil, fn) }

func (v *Value) walk(path []string, fn func([]string, *Value)) {
	fn(path, v)
	for _, m := range v.members {
		m.Value.walk(append(path[:len(path):len(path)], m.Name), fn)
	}
	for i, it := range v.items {
		it.walk(append(path[:len(path):len(path)], "["+strconv.Itoa(i)+"]"), fn)
	}
}

// FromAny converts decoded Go data (as produced by a plist or JSON decoder)
// into a Value. Map keys are sorted. Supported: nil, bool, string, integer
// and float types, []any, map[string]any.
func FromAny(x any) (*Value, error) {
	switch t := x.(type) {
	case nil:
		return &Value{kind: Null, raw: []byte("null")}, nil
	case bool:
		return &Value{kind: Bool, raw: []byte(strconv.FormatBool(t))}, nil
	case string:
		return NewString(t), nil
	case int:
		return &Value{kind: Number, raw: []byte(strconv.FormatInt(int64(t), 10))}, nil
	case int64:
		return &Value{kind: Number, raw: []byte(strconv.FormatInt(t, 10))}, nil
	case uint64:
		return &Value{kind: Number, raw: []byte(strconv.FormatUint(t, 10))}, nil
	case float64:
		return &Value{kind: Number, raw: jsontext.AppendFloat(nil, t, 64)}, nil
	case []any:
		v := &Value{kind: Array}
		for _, e := range t {
			c, err := FromAny(e)
			if err != nil {
				return nil, err
			}
			v.items = append(v.items, c)
		}
		return v, nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		v := &Value{kind: Object}
		for _, k := range keys {
			c, err := FromAny(t[k])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			v.members = append(v.members, Member{Name: k, Value: c})
		}
		return v, nil
	}
	return nil, fmt.Errorf("jsondoc: unsupported type %T", x)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd deckformat && go test ./jsondoc/ && go vet ./jsondoc/`
Expected: `ok  github.com/csmarshall/schrodeck/deckformat/jsondoc`

- [ ] **Step 5: Commit**

```bash
git add deckformat/jsondoc
git commit -m "feat(#<n>): jsondoc: ordered JSON tree with byte-exact round trip and JCS"
```

---

### Task 2: Synthetic fixtures and the profile loader

**Files:**
- Create: `deckformat/fixture/fixture.go`, `deckformat/profile/profile.go`, `deckformat/profile/profile_test.go`
- Modify: `docs/contracts/profile-format.md` (new row P12: the app writes compact JSON)

**Interfaces:**
- Consumes: `jsondoc`.
- Produces (package `fixture`): `Device = "@(1)[4057/143/<deck>]"`, `Model = "20GAT9902"`; types `Button{Slot, ActionID, Plugin, Settings, Title string; State int; Image string; ImageSeed byte; MissingImage bool}`, `Page{ID string; Buttons []Button; Encoder bool; Orphans map[string]byte}`, `Profile{ID, Name, Model, Device string; Pages []Page; Default Page; Current string; AppIdentifier *string; Extra map[string][]byte}`; `(Profile) Folder() string`, `(Profile) FS() fstest.MapFS`; `XL() Profile`; `CopyOf(p Profile, salt string) Profile`; `PNG(seed byte) []byte`; `Merge(...fstest.MapFS) fstest.MapFS`; `WriteTo(dir string, fsys fs.FS) error`; `PageFolder(id string) string`.
- Produces (package `profile`): `KnownVersion = "3.0"`, `Suffix = ".sdProfile"`; `Page{Folder string; Manifest *jsondoc.Value; Images map[string][]byte}`; `Profile{Folder string; Manifest *jsondoc.Value; Images map[string][]byte; Pages map[string]*Page /* key: lower-case folder */; Junk []string}`; `UnexpectedFileError{Profile, Path string}`; `IsJunk(name) bool`; `Load(fsys fs.FS, folder string) (*Profile, error)`; `LoadResult{Profiles []*Profile; Errors []error; Skipped []string}`; `LoadAll(fsys fs.FS) (LoadResult, error)`; accessors `Version()`, `Name()`, `DeviceUUID()`, `DeviceModel()`, `AppIdentifier() (string, bool)`, `PageOrder() ([]string, error)` (lower-cased), `DefaultPage() string` (lower-cased), `Files() map[string][]byte`, `SortedPageKeys() []string`; `ErrMalformed`.

The fixture package is synthetic by construction: made-up ids, `<deck>` instead of a serial, generated images. Its shapes follow the real Mac (compact JSON, sorted keys, no trailing newline, upper-case folder names, lower-case references, `Images/` beside each page manifest), checked read-only on 2026-10-02.

- [ ] **Step 1: Write the fixture package**

`deckformat/fixture/fixture.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package fixture builds synthetic Stream Deck profiles for tests. Nothing
// here comes from a real machine: ids are made up, device ids carry the
// <deck> placeholder instead of a serial, and images are generated. The
// shapes (compact JSON with sorted keys, upper-case folder names, lower-case
// references, Images/ next to each page manifest) follow what was observed
// on a real Mac (docs/streamdeck-config-model.md, contract C).
package fixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing/fstest"
)

// Device is the placeholder device id every fixture is bound to: the shape
// of a real one (vendor 4057, product 143) with <deck> where the serial goes.
const Device = "@(1)[4057/143/<deck>]"

// Model is the Device.Model value used by fixtures.
const Model = "20GAT9902"

// Button is one key.
type Button struct {
	Slot      string // "col,row"
	ActionID  string
	Plugin    string // action UUID, e.g. com.elgato.streamdeck.system.open
	Settings  string // raw JSON object
	Title     string
	State     int
	Image     string // file name under the page's Images/, "" for none
	ImageSeed byte   // content of the generated image
	// MissingImage references Image without writing the file.
	MissingImage bool
}

// Page is one page.
type Page struct {
	ID      string // lower-case UUID used in references; its folder is upper-case
	Buttons []Button
	Encoder bool            // controller Type "Encoder" instead of "Keypad"
	Orphans map[string]byte // image files no button references: name -> seed
}

// Profile is one synthetic profile.
type Profile struct {
	ID            string // lower-case UUID; folder is upper-case + ".sdProfile"
	Name          string
	Model         string
	Device        string
	Pages         []Page // Pages.Pages order
	Default       Page   // the empty page Pages.Default points to (U2)
	Current       string // Pages.Current
	AppIdentifier *string
	// Extra files relative to the profile folder, e.g. to trip the allow-list.
	Extra map[string][]byte
}

// Folder is the profile's folder name.
func (p Profile) Folder() string { return strings.ToUpper(p.ID) + ".sdProfile" }

// XL returns the base fixture: two pages of Open and Hotkey buttons with
// images, plus the empty default page.
func XL() Profile {
	return Profile{
		ID:     "aaaaaaaa-0000-4000-8000-000000000001",
		Name:   "Fixture XL",
		Model:  Model,
		Device: Device,
		Pages: []Page{
			{ID: "aaaaaaaa-0000-4000-8000-0000000000a1", Buttons: []Button{
				{Slot: "0,0", ActionID: "11111111-0000-4000-8000-000000000001", Plugin: "com.elgato.streamdeck.system.open",
					Settings: `{"openInBrowser":true,"path":"/Users/<user>/bin/demo.sh"}`, Title: "Demo", Image: "IMG00000000000000000000000001.png", ImageSeed: 1},
				{Slot: "1,0", ActionID: "11111111-0000-4000-8000-000000000002", Plugin: "com.elgato.streamdeck.system.hotkey",
					Settings: `{"Coalesce":true,"Hotkeys":[{"KeyCmd":true,"NativeCode":0}]}`, Title: "Copy", Image: "IMG00000000000000000000000002.png", ImageSeed: 2},
			}},
			{ID: "aaaaaaaa-0000-4000-8000-0000000000a2", Buttons: []Button{
				{Slot: "7,3", ActionID: "11111111-0000-4000-8000-000000000003", Plugin: "com.elgato.streamdeck.page.next",
					Settings: `{}`, Title: "", Image: "IMG00000000000000000000000003.png", ImageSeed: 3},
			}},
		},
		Default: Page{ID: "aaaaaaaa-0000-4000-8000-0000000000d0"},
		Current: "aaaaaaaa-0000-4000-8000-0000000000a1",
	}
}

// newID derives a fresh lower-case UUID-shaped id from a salt and an old id,
// the way the app gives a copy new ids.
func newID(salt, old string) string {
	sum := sha256.Sum256([]byte(salt + ":" + old))
	h := hex.EncodeToString(sum[:16])
	return h[0:8] + "-" + h[8:12] + "-4" + h[13:16] + "-8" + h[17:20] + "-" + h[20:32]
}

// CopyOf returns the same content with every instance id changed: profile id,
// page ids, ActionIDs and image file names, as the app does when it copies a
// page or profile (R20, contract C P9 and P11).
func CopyOf(p Profile, salt string) Profile {
	c := p
	c.ID = newID(salt, p.ID)
	ren := map[string]string{}
	page := func(pg Page) Page {
		out := pg
		out.ID = newID(salt, pg.ID)
		ren[pg.ID] = out.ID
		out.Buttons = nil
		for _, b := range pg.Buttons {
			nb := b
			nb.ActionID = newID(salt, b.ActionID)
			if b.Image != "" {
				nb.Image = "IMG" + strings.ReplaceAll(strings.ToUpper(newID(salt, b.Image)), "-", "")[:26] + ".png"
			}
			out.Buttons = append(out.Buttons, nb)
		}
		out.Orphans = nil
		for name, seed := range pg.Orphans {
			if out.Orphans == nil {
				out.Orphans = map[string]byte{}
			}
			out.Orphans["IMG"+strings.ToUpper(newID(salt, name))[:8]+".png"] = seed
		}
		return out
	}
	c.Pages = nil
	for _, pg := range p.Pages {
		c.Pages = append(c.Pages, page(pg))
	}
	c.Default = page(p.Default)
	if id, ok := ren[p.Current]; ok {
		c.Current = id
	}
	return c
}

// PNG returns a small, valid, deterministic PNG whose pixels depend on seed,
// so different seeds give different bytes.
func PNG(seed byte) []byte {
	sum := sha256.Sum256([]byte{seed})
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < 16; i++ {
		img.Set(i%4, i/4, color.RGBA{sum[(i*3)%32], sum[(i*3+1)%32], sum[(i*3+2)%32], 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// marshal encodes like the app: compact, keys sorted, no HTML escaping, no
// trailing newline.
func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func pageManifest(pg Page) []byte {
	actions := map[string]any{}
	for _, b := range pg.Buttons {
		state := map[string]any{"Title": b.Title}
		if b.Image != "" {
			state["Image"] = "Images/" + b.Image
		}
		actions[b.Slot] = map[string]any{
			"ActionID":    b.ActionID,
			"LinkedTitle": true,
			"Name":        path.Base(strings.ReplaceAll(b.Plugin, ".", "/")),
			"Plugin":      map[string]any{"Name": "Fixture", "UUID": b.Plugin, "Version": "1.0"},
			"Resources":   nil,
			"Settings":    json.RawMessage(b.Settings),
			"State":       b.State,
			"States":      []any{state},
			"UUID":        b.Plugin,
		}
	}
	typ := "Keypad"
	if pg.Encoder {
		typ = "Encoder"
	}
	return marshal(map[string]any{
		"Controllers": []any{map[string]any{"Actions": actions, "Type": typ}},
		"Icon":        "",
		"Name":        "",
	})
}

// FS returns the profile as a file system rooted like ProfilesV3.
func (p Profile) FS() fstest.MapFS {
	root := p.Folder()
	fsys := fstest.MapFS{}
	ids := []any{}
	for _, pg := range p.Pages {
		ids = append(ids, pg.ID)
	}
	top := map[string]any{
		"Device":  map[string]any{"Model": p.Model, "UUID": p.Device},
		"Name":    p.Name,
		"Pages":   map[string]any{"Current": p.Current, "Default": p.Default.ID, "Pages": ids},
		"Version": "3.0",
	}
	if p.AppIdentifier != nil {
		top["AppIdentifier"] = *p.AppIdentifier
	}
	fsys[root+"/manifest.json"] = &fstest.MapFile{Data: marshal(top)}
	fsys[root+"/Images"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
	for _, pg := range append(append([]Page{}, p.Pages...), p.Default) {
		dir := root + "/Profiles/" + strings.ToUpper(pg.ID)
		fsys[dir+"/manifest.json"] = &fstest.MapFile{Data: pageManifest(pg)}
		fsys[dir+"/Images"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
		for _, b := range pg.Buttons {
			if b.Image != "" && !b.MissingImage {
				fsys[dir+"/Images/"+b.Image] = &fstest.MapFile{Data: PNG(b.ImageSeed)}
			}
		}
		for name, seed := range pg.Orphans {
			fsys[dir+"/Images/"+name] = &fstest.MapFile{Data: PNG(seed)}
		}
	}
	for rel, data := range p.Extra {
		fsys[root+"/"+rel] = &fstest.MapFile{Data: data}
	}
	return fsys
}

// Merge combines file systems (later ones win on a clash).
func Merge(fss ...fstest.MapFS) fstest.MapFS {
	out := fstest.MapFS{}
	for _, f := range fss {
		for k, v := range f {
			out[k] = v
		}
	}
	return out
}

// WriteTo writes every regular file of fsys below dir.
func WriteTo(dir string, fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// PageFolder returns the on-disk folder name of the page with id.
func PageFolder(id string) string { return strings.ToUpper(id) }
```

- [ ] **Step 2: Write the failing loader tests**

`deckformat/profile/profile_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package profile

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
)

func load(t *testing.T, fp fixture.Profile) *Profile {
	t.Helper()
	p, err := Load(fp.FS(), fp.Folder())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadRoundTripIsByteIdentical(t *testing.T) {
	fp := fixture.XL()
	fsys := fp.FS()
	p := load(t, fp)
	files := p.Files()
	n := 0
	for name, f := range fsys {
		if f.Mode.IsDir() {
			continue
		}
		rel := strings.TrimPrefix(name, fp.Folder()+"/")
		got, ok := files[rel]
		if !ok {
			t.Errorf("Files() lacks %s", rel)
			continue
		}
		if !bytes.Equal(got, f.Data) {
			t.Errorf("%s changed in the round trip", rel)
		}
		n++
	}
	if n != len(files) {
		t.Errorf("Files() has %d entries, the fixture %d", len(files), n)
	}
}

func TestUnknownFieldSurvivesRoundTrip(t *testing.T) {
	fp := fixture.XL()
	fsys := fp.FS()
	key := fp.Folder() + "/manifest.json"
	injected := bytes.Replace(fsys[key].Data, []byte(`"Name":`), []byte(`"FutureField":{"x":[1.50,null]},"Name":`), 1)
	fsys[key] = &fstest.MapFile{Data: injected}
	p, err := Load(fsys, fp.Folder())
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Files()["manifest.json"]; !bytes.Equal(got, injected) {
		t.Fatalf("unknown field lost or rewritten:\n got %s\nwant %s", got, injected)
	}
}

func TestPagesAreKeyedCaseInsensitively(t *testing.T) {
	fp := fixture.XL()
	p := load(t, fp)
	order, err := p.PageOrder()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range order {
		pg := p.Pages[id]
		if pg == nil {
			t.Fatalf("page %s from Pages.Pages not found among folders", id)
		}
		if pg.Folder != strings.ToUpper(id) {
			t.Errorf("page folder %q, want the on-disk upper-case %q", pg.Folder, strings.ToUpper(id))
		}
	}
	if p.Pages[p.DefaultPage()] == nil {
		t.Fatal("default page not found")
	}
}

func TestAccessors(t *testing.T) {
	p := load(t, fixture.XL())
	if p.Version() != KnownVersion || p.Name() != "Fixture XL" || p.DeviceUUID() != fixture.Device || p.DeviceModel() != fixture.Model {
		t.Fatalf("accessors: %q %q %q %q", p.Version(), p.Name(), p.DeviceUUID(), p.DeviceModel())
	}
	if _, ok := p.AppIdentifier(); ok {
		t.Fatal("fixture has no AppIdentifier")
	}
}

func TestAllowList(t *testing.T) {
	cases := map[string]bool{ // extra file -> must fail
		"notes.txt":                 true,
		"Images/sub/x.png":          true,
		"Profiles/X/extra.json":     true,
		"Profiles/X/Images/a/b.png": true,
		".DS_Store":                 false,
		"Images/.DS_Store":          false,
		"manifest.json (conflicted copy 2026-10-02).json": false,
		"Images/x.png.icloud":                             false,
	}
	for extra, mustFail := range cases {
		fp := fixture.XL()
		fp.Extra = map[string][]byte{extra: []byte("x")}
		_, err := Load(fp.FS(), fp.Folder())
		var ue *UnexpectedFileError
		if got := errors.As(err, &ue); got != mustFail {
			t.Errorf("%s: unexpected-file error = %v (%v), want %v", extra, got, err, mustFail)
		}
	}
}

func TestJunkIsRecorded(t *testing.T) {
	fp := fixture.XL()
	fp.Extra = map[string][]byte{".DS_Store": []byte("x")}
	p := load(t, fp)
	if len(p.Junk) != 1 || p.Junk[0] != ".DS_Store" {
		t.Fatalf("Junk = %v", p.Junk)
	}
	if _, ok := p.Files()[".DS_Store"]; ok {
		t.Fatal("junk must not be part of the profile's files")
	}
}

func TestLoadFailsClosed(t *testing.T) {
	fp := fixture.XL()
	fsys := fp.FS()
	fsys[fp.Folder()+"/manifest.json"] = &fstest.MapFile{Data: []byte("{\"Name\": \"indented\"}")}
	if _, err := Load(fsys, fp.Folder()); !errors.Is(err, jsondoc.ErrNotRoundTrip) {
		t.Fatalf("non-round-trippable manifest: err = %v", err)
	}
	fsys = fp.FS()
	delete(fsys, fp.Folder()+"/manifest.json")
	if _, err := Load(fsys, fp.Folder()); err == nil {
		t.Fatal("profile without manifest.json accepted")
	}
}

func TestLoadAllIsolatesBrokenProfiles(t *testing.T) {
	good := fixture.XL()
	bad := fixture.CopyOf(fixture.XL(), "broken")
	bad.Extra = map[string][]byte{"stray.bin": []byte("x")}
	fsys := fixture.Merge(good.FS(), bad.FS(), fstest.MapFS{".DS_Store": {Data: []byte("x")}})
	res, err := LoadAll(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Profiles) != 1 || len(res.Errors) != 1 || len(res.Skipped) != 1 {
		t.Fatalf("profiles %d, errors %d, skipped %v", len(res.Profiles), len(res.Errors), res.Skipped)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `cd deckformat && go test ./profile/`
Expected: FAIL, `undefined: Load`.

- [ ] **Step 4: Implement the loader**

`deckformat/profile/profile.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package profile reads Stream Deck profiles (ProfilesV3/<UUID>.sdProfile)
// into memory: manifests as order- and literal-preserving JSON trees, images
// as bytes. It reads through fs.FS only, so it cannot write the app's files.
// Layout and allow-list: contract C (docs/contracts/profile-format.md), P1 and P7.
package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
)

// KnownVersion is the profile manifest Version this toolkit was verified
// against (contract C P2).
const KnownVersion = "3.0"

// Suffix is the extension of a profile folder.
const Suffix = ".sdProfile"

// Page is one page folder: Profiles/<Folder>/.
type Page struct {
	Folder   string // as on disk (observed upper-case)
	Manifest *jsondoc.Value
	Images   map[string][]byte // file name -> bytes
}

// Profile is one <UUID>.sdProfile folder.
type Profile struct {
	Folder   string // "<UUID>.sdProfile", as on disk
	Manifest *jsondoc.Value
	Images   map[string][]byte // top-level Images/
	Pages    map[string]*Page  // key: lower-case page folder name
	// Junk lists ignored files (relative paths), e.g. .DS_Store.
	Junk []string
}

// UnexpectedFileError reports a path outside the allow-list (contract C P7).
type UnexpectedFileError struct {
	Profile string
	Path    string
}

func (e *UnexpectedFileError) Error() string {
	return fmt.Sprintf("%s: unexpected path %q (not in contract C's allow-list)", e.Profile, e.Path)
}

// IsJunk reports whether a file name is known noise that is ignored for
// hashing and copying (contract C § file allow-list).
func IsJunk(name string) bool {
	switch {
	case name == ".DS_Store":
		return true
	case strings.Contains(name, " (conflicted copy"):
		return true
	case strings.HasSuffix(name, ".icloud"):
		return true
	case strings.HasPrefix(name, "~$"):
		return true
	case strings.HasSuffix(name, "~"):
		return true
	case strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".swp"):
		return true
	case strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#"):
		return true
	}
	return false
}

type entryKind int

const (
	allowedDir entryKind = iota
	topManifest
	topImage
	pageManifest
	pageImage
	unexpected
)

func classify(parts []string, isDir bool) entryKind {
	switch {
	case len(parts) == 1 && parts[0] == "manifest.json" && !isDir:
		return topManifest
	case len(parts) == 1 && (parts[0] == "Images" || parts[0] == "Profiles") && isDir:
		return allowedDir
	case len(parts) == 2 && parts[0] == "Images" && !isDir:
		return topImage
	case len(parts) == 2 && parts[0] == "Profiles" && isDir:
		return allowedDir
	case len(parts) == 3 && parts[0] == "Profiles" && parts[2] == "manifest.json" && !isDir:
		return pageManifest
	case len(parts) == 3 && parts[0] == "Profiles" && parts[2] == "Images" && isDir:
		return allowedDir
	case len(parts) == 4 && parts[0] == "Profiles" && parts[2] == "Images" && !isDir:
		return pageImage
	}
	return unexpected
}

// Load reads the profile folder named folder from fsys (fsys is rooted like
// ProfilesV3). It fails on a path outside the allow-list, a missing manifest
// or a manifest that does not round-trip (jsondoc.ErrNotRoundTrip).
func Load(fsys fs.FS, folder string) (*Profile, error) {
	p := &Profile{Folder: folder, Images: map[string][]byte{}, Pages: map[string]*Page{}}
	page := func(name string) *Page {
		key := strings.ToLower(name)
		if p.Pages[key] == nil {
			p.Pages[key] = &Page{Folder: name, Images: map[string][]byte{}}
		}
		return p.Pages[key]
	}
	err := fs.WalkDir(fsys, folder, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if full == folder {
			return nil
		}
		rel := strings.TrimPrefix(full, folder+"/")
		if IsJunk(d.Name()) {
			p.Junk = append(p.Junk, rel)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		parts := strings.Split(rel, "/")
		kind := classify(parts, d.IsDir())
		if kind == unexpected {
			return &UnexpectedFileError{Profile: folder, Path: rel}
		}
		if kind == allowedDir {
			return nil
		}
		data, err := fs.ReadFile(fsys, full)
		if err != nil {
			return err
		}
		switch kind {
		case topManifest, pageManifest:
			doc, err := jsondoc.Parse(data)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", folder, rel, err)
			}
			if kind == topManifest {
				p.Manifest = doc
			} else {
				page(parts[1]).Manifest = doc
			}
		case topImage:
			p.Images[parts[1]] = data
		case pageImage:
			page(parts[1]).Images[parts[3]] = data
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if p.Manifest == nil {
		return nil, fmt.Errorf("%s: no manifest.json", folder)
	}
	for _, pg := range p.Pages {
		if pg.Manifest == nil {
			return nil, fmt.Errorf("%s: page folder %s has no manifest.json", folder, pg.Folder)
		}
	}
	return p, nil
}

// LoadResult is every profile found in a ProfilesV3-shaped file system.
type LoadResult struct {
	Profiles []*Profile
	// Errors holds one error per profile that failed to load.
	Errors []error
	// Skipped lists top-level entries that are not profile folders.
	Skipped []string
}

// LoadAll loads every *.sdProfile folder at the root of fsys. One broken
// profile does not hide the others.
func LoadAll(fsys fs.FS) (LoadResult, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return LoadResult{}, err
	}
	var res LoadResult
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), Suffix) {
			res.Skipped = append(res.Skipped, e.Name())
			continue
		}
		p, err := Load(fsys, e.Name())
		if err != nil {
			res.Errors = append(res.Errors, err)
			continue
		}
		res.Profiles = append(res.Profiles, p)
	}
	return res, nil
}

func str(v *jsondoc.Value) string {
	s, _ := v.Str()
	return s
}

// Version is the manifest's Version.
func (p *Profile) Version() string { return str(p.Manifest.Get("Version")) }

// Name is the profile's display name.
func (p *Profile) Name() string { return str(p.Manifest.Get("Name")) }

// DeviceUUID is the top-level Device.UUID (it embeds the deck's serial, R9).
func (p *Profile) DeviceUUID() string { return str(p.Manifest.Lookup("Device", "UUID")) }

// DeviceModel is the top-level Device.Model.
func (p *Profile) DeviceModel() string { return str(p.Manifest.Lookup("Device", "Model")) }

// AppIdentifier returns the smart-profile field, if present (U3).
func (p *Profile) AppIdentifier() (string, bool) {
	v := p.Manifest.Get("AppIdentifier")
	if v == nil {
		return "", false
	}
	return v.Str()
}

// ErrMalformed marks a manifest whose structure is not what contract C says.
var ErrMalformed = errors.New("malformed manifest")

// PageOrder returns Pages.Pages, lower-cased, in order.
func (p *Profile) PageOrder() ([]string, error) {
	list := p.Manifest.Lookup("Pages", "Pages")
	if list == nil || list.Kind() != jsondoc.Array {
		return nil, fmt.Errorf("%s: Pages.Pages is not a list: %w", p.Folder, ErrMalformed)
	}
	var out []string
	for _, it := range list.Items() {
		s, ok := it.Str()
		if !ok {
			return nil, fmt.Errorf("%s: Pages.Pages holds a non-string: %w", p.Folder, ErrMalformed)
		}
		out = append(out, strings.ToLower(s))
	}
	return out, nil
}

// DefaultPage returns Pages.Default, lower-cased ("" if absent).
func (p *Profile) DefaultPage() string {
	return strings.ToLower(str(p.Manifest.Lookup("Pages", "Default")))
}

// Files returns every allow-listed file as relative path -> bytes. Manifests
// are re-encoded from their trees, which Load guarantees is byte-identical
// to what was read.
func (p *Profile) Files() map[string][]byte {
	out := map[string][]byte{"manifest.json": p.Manifest.Encode()}
	for name, b := range p.Images {
		out["Images/"+name] = b
	}
	for _, pg := range p.Pages {
		out[path.Join("Profiles", pg.Folder, "manifest.json")] = pg.Manifest.Encode()
		for name, b := range pg.Images {
			out[path.Join("Profiles", pg.Folder, "Images", name)] = b
		}
	}
	return out
}

// SortedPageKeys returns the page keys in a stable order.
func (p *Profile) SortedPageKeys() []string {
	keys := make([]string, 0, len(p.Pages))
	for k := range p.Pages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd deckformat && go test ./profile/ ./fixture/ && go vet ./...`
Expected: `ok` for `profile`, `[no test files]` for `fixture`.

- [ ] **Step 6: Record the compact-JSON assumption in contract C**

The loader refuses a manifest it cannot re-encode byte for byte, which includes indented JSON and a trailing newline. That is the right fail-closed behaviour, but it rests on one observation, so it becomes a named contract row: an app update that starts pretty-printing then fails as "P12", not as a puzzle. In `docs/contracts/profile-format.md`, add after the P11 row:

`| P12 | Manifests are compact JSON: no indentation or other insignificant whitespace, members in the order the app wrote them, no trailing newline | observed (one Mac, app 7.5.1, three profiles, 2026-10-02) | the loader (P1) re-encodes every manifest and refuses any that do not round-trip byte for byte, naming the file; a formatting change by the app therefore fails P1 with a not-round-trip error instead of being rewritten |`

- [ ] **Step 7: Commit**

```bash
git add deckformat/fixture deckformat/profile docs/contracts/profile-format.md
git commit -m "feat(#<n>): synthetic fixtures and the profile loader with contract C's allow-list (P1, P7)"
```

---

### Task 3: The format schema and fingerprint (ADR 0015)

**Files:**
- Create: `deckformat/profile/schema.go`, `deckformat/profile/schema_test.go`
- Modify: `docs/adr/0015-schema-guard.md` (Status line: one revision sentence)

**Interfaces:**
- Consumes: `profile.Profile`, `jsondoc`.
- Produces: `profile.Schema{Version string; KeyPaths []string; FilePatterns []string}` (JSON members `version`, `key_paths`, `file_patterns`); `profile.SchemaOf([]*Profile) (Schema, error)`; `(Schema) Digest() (string, error)` = hex sha256 of the JCS form.

Key-path rules (they make ADR 0015's example precise): paths are prefixed `manifest:` or `page:`; array indices collapse to `[]`; member names under `Actions` (key slots like `3,1`) collapse to `*`; paths stop at `Settings`, which is plugin-defined content rather than format structure.

- [ ] **Step 1: Write the failing tests**

`deckformat/profile/schema_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package profile

import (
	"testing"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
)

func schemaDigest(t *testing.T, fps ...fixture.Profile) string {
	t.Helper()
	var ps []*Profile
	for _, fp := range fps {
		ps = append(ps, load(t, fp))
	}
	s, err := SchemaOf(ps)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSchemaFingerprint(t *testing.T) {
	base := schemaDigest(t, fixture.XL())

	// Values are not structure: a changed title or setting value keeps it.
	edited := fixture.XL()
	edited.Pages[0].Buttons[0].Title = "Renamed"
	edited.Pages[0].Buttons[0].Settings = `{"openInBrowser":false,"path":"/tmp/x"}`
	if schemaDigest(t, edited) != base {
		t.Error("a value change altered the fingerprint")
	}
	// Plugin settings are opaque: a new settings key keeps it.
	newSetting := fixture.XL()
	newSetting.Pages[0].Buttons[0].Settings = `{"openInBrowser":true,"path":"/tmp/x","newKey":1}`
	if schemaDigest(t, newSetting) != base {
		t.Error("a new plugin setting altered the fingerprint")
	}
	// Known-bad inputs the guard exists to catch.
	v4 := load(t, fixture.XL())
	v4.Manifest.Set("Version", jsondoc.NewString("4.0"))
	s, _ := SchemaOf([]*Profile{v4})
	if d, _ := s.Digest(); d == base {
		t.Error("a changed Version kept the fingerprint")
	}
	unknown := load(t, fixture.XL())
	unknown.Manifest.Set("FutureField", jsondoc.NewString("x"))
	s, _ = SchemaOf([]*Profile{unknown})
	if d, _ := s.Digest(); d == base {
		t.Error("a new manifest key kept the fingerprint")
	}
}

// Documents a hazard to be measured in M1's observations (F1): a smart
// profile's AppIdentifier is a key that only some profiles have, so the
// fingerprint of a set of profiles depends on its content.
func TestSchemaDependsOnOptionalKeys(t *testing.T) {
	star := "*"
	smart := fixture.XL()
	smart.AppIdentifier = &star
	if schemaDigest(t, smart) == schemaDigest(t, fixture.XL()) {
		t.Fatal("expected AppIdentifier to change the key-path set")
	}
}

func TestSchemaRefusesMixedVersions(t *testing.T) {
	a := load(t, fixture.XL())
	b := load(t, fixture.CopyOf(fixture.XL(), "v"))
	b.Manifest.Set("Version", jsondoc.NewString("4.0"))
	if _, err := SchemaOf([]*Profile{a, b}); err == nil {
		t.Fatal("mixed Version values accepted")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd deckformat && go test ./profile/`
Expected: FAIL, `undefined: SchemaOf`.

- [ ] **Step 3: Implement**

`deckformat/profile/schema.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"sort"
	"strings"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
)

// Schema is the input of ADR 0015's format fingerprint: the profile Version,
// the structural key paths of every manifest and the file-name patterns
// present.
type Schema struct {
	Version      string   `json:"version"`
	KeyPaths     []string `json:"key_paths"`
	FilePatterns []string `json:"file_patterns"`
}

// opaqueMembers hold plugin-defined content, not format structure: key paths
// stop at them (ADR 0015's example path ends at ...Actions.*.Settings).
var opaqueMembers = map[string]bool{"Settings": true}

// wildcardMembers are objects whose member NAMES are data (key slots such as
// "3,1"); their names collapse to "*".
var wildcardMembers = map[string]bool{"Actions": true}

// SchemaOf computes the schema over a set of profiles. Profiles with
// different Version values are an error: one fingerprint cannot describe two
// formats.
func SchemaOf(ps []*Profile) (Schema, error) {
	keys := map[string]bool{}
	patterns := map[string]bool{}
	version := ""
	for _, p := range ps {
		if version == "" {
			version = p.Version()
		} else if p.Version() != version {
			return Schema{}, fmt.Errorf("profiles have different Version values (%q, %q)", version, p.Version())
		}
		keyPaths("manifest:", p.Manifest, keys)
		for _, pg := range p.Pages {
			keyPaths("page:", pg.Manifest, keys)
		}
		for rel := range p.Files() {
			patterns[pattern(rel)] = true
		}
	}
	return Schema{Version: version, KeyPaths: sorted(keys), FilePatterns: sorted(patterns)}, nil
}

// Digest is ADR 0015's fingerprint: sha256 of the JCS form of the schema.
func (s Schema) Digest() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	v := jsontext.Value(b)
	if err := v.Canonicalize(); err != nil {
		return "", err
	}
	sum := sha256.Sum256(v)
	return hex.EncodeToString(sum[:]), nil
}

func keyPaths(prefix string, v *jsondoc.Value, out map[string]bool) {
	var walk func(path string, v *jsondoc.Value, parent string)
	walk = func(path string, v *jsondoc.Value, parent string) {
		switch v.Kind() {
		case jsondoc.Object:
			for _, m := range v.Members() {
				name := m.Name
				if wildcardMembers[parent] {
					name = "*"
				}
				p := name
				if path != "" {
					p = path + "." + name
				}
				out[prefix+p] = true
				if !opaqueMembers[m.Name] {
					walk(p, m.Value, m.Name)
				}
			}
		case jsondoc.Array:
			for _, it := range v.Items() {
				walk(path+"[]", it, parent)
			}
		}
	}
	walk("", v, "")
}

func pattern(rel string) string {
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) == 2 && parts[0] == "Images":
		return "Images/*"
	case len(parts) == 3 && parts[0] == "Profiles":
		return "Profiles/*/" + parts[2]
	case len(parts) == 4 && parts[0] == "Profiles":
		return "Profiles/*/Images/*"
	}
	return rel
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd deckformat && go test ./profile/`
Expected: `ok`.

- [ ] **Step 5: Record the key-path rules in ADR 0015**

In `docs/adr/0015-schema-guard.md`, append to the end of the `Status:` paragraph:

` Revised 2026-10-02 (M1, implementation): key paths collapse array indices to \`[]\` and the key-slot member names under \`Actions\` to \`*\`, and stop at \`Settings\` (plugin-defined content); paths carry a \`manifest:\` or \`page:\` prefix. Whether the content-dependent key set (e.g. \`AppIdentifier\` only on some profiles) makes the fingerprint flap on ordinary edits is measured in M1 (observation F1) before M2 relies on it.`

- [ ] **Step 6: Commit, push, PR**

```bash
git add deckformat/profile docs/adr/0015-schema-guard.md
git commit -m "feat(#<n>): profile schema and ADR 0015 format fingerprint"
tools/ci/selftest.sh && tools/ci/check-headers.sh && tools/ci/check-gofmt.sh && tools/ci/leak-scan.sh
export $(cat ~/.ssh_agent_socket) && git push -u origin HEAD
gh pr create --fill --body "$(printf 'Closes #%s\n\nhttps://claude.ai/code/session_01BpNb9wCfEXosfKyRoBrsr4\n' <n>)"
```

The link is the attribution line of the session executing the plan (the planning session's is shown; a different executing session uses its own). Then: `gh pr checks --watch`, code-review subagent on `gh pr diff`, owner review, `gh pr merge --squash --delete-branch`, `git -C ~/work/personal/schrodeck pull`.

---

### Task 4: The normalized hash, norm_version 1

Issue: "deckformat: normalized hash, norm_version 1" (new issue and worktree `<n>-normhash`, as in Task 1).

**Files:**
- Create: `deckformat/normhash/normhash.go`, `deckformat/normhash/normhash_test.go`, `deckformat/normhash/testdata/refhash.py`
- Modify: `deckformat/go.mod`, `deckformat/go.sum` (add `golang.org/x/text`), `docs/contracts/profile-format.md` (name the editor temp-file patterns)

**Interfaces:**
- Consumes: `profile.*`, `jsondoc.*`, `fixture.*` (tests).
- Produces (package `normhash`): `NormVersion = 1`; `StripList` (`Top: {Device.UUID}, {Pages.Current}`; `Action: {State}, {ActionID}`); `ErrDanglingPage`; `Doc{Path string; Value *jsondoc.Value}`; `Normalize(*profile.Profile) ([]Doc, error)`; `Hash(*profile.Profile) (string, error)`; `HashDocs([]Doc) (string, error)`; `PageLabels(*profile.Profile) (map[string]string, error)`.

The golden value in the test comes from `testdata/refhash.py`, a second implementation written from contract C's text rather than from the Go code. A test that compared the Go hasher only with itself would prove consistency, not correctness. The Python reference was run on 2026-10-02 and agrees: `3224b89189a93d552a4c479bae1b722c185489a7d8597cc4c8f680f444737200`. What it cannot catch: a misreading of contract C shared by both implementations, and floats or non-ASCII member names, which the reference does not handle (the fixture has neither).

- [ ] **Step 1: Add the dependency**

```bash
cd deckformat && go get golang.org/x/text@v0.42.0 && cd ..
```

- [ ] **Step 2: Write the reference implementation**

`deckformat/normhash/testdata/refhash.py` (in `testdata/`, so the Go tool ignores it):

```python
#!/usr/bin/env python3
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
"""Independent reference implementation of contract C's normalized hash.

Written from docs/contracts/profile-format.md, not from the Go code, so the
golden value it prints is evidence that the Go implementation matches the
contract rather than merely agreeing with itself. It handles the subset the
fixtures use (no floats; ASCII member names), for which RFC 8785 equals
json.dumps(sort_keys=True, separators=(",", ":"), ensure_ascii=False).

Usage: refhash.py <profile-folder>
"""
import hashlib
import json
import os
import sys
import unicodedata


def jcs(obj):
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def walk_strings(obj, fn):
    """Replace every string value (not member names) with fn(value)."""
    if isinstance(obj, dict):
        return {k: walk_strings(v, fn) for k, v in obj.items()}
    if isinstance(obj, list):
        return [walk_strings(v, fn) for v in obj]
    if isinstance(obj, str):
        return fn(obj)
    return obj


def image_ref(images_dir):
    def fn(s):
        if not s.startswith("Images/"):
            return s
        path = os.path.join(images_dir, s[len("Images/"):])
        if not os.path.isfile(path):
            return "missing:" + s
        with open(path, "rb") as f:
            return "sha256:" + hashlib.sha256(f.read()).hexdigest()
    return fn


def main(folder):
    with open(os.path.join(folder, "manifest.json"), encoding="utf-8") as f:
        top = json.load(f)
    pages_dir = os.path.join(folder, "Profiles")
    folders = {name.lower(): name for name in os.listdir(pages_dir)}

    labels = {}
    for i, pid in enumerate(top["Pages"]["Pages"]):
        labels[pid.lower()] = "page/%d" % i
    labels[top["Pages"]["Default"].lower()] = "default"
    for low, name in folders.items():
        labels.setdefault(low, "other/" + name)

    # top-level manifest
    del top["Device"]["UUID"]
    del top["Pages"]["Current"]
    top["Pages"]["Pages"] = [labels[p.lower()] for p in top["Pages"]["Pages"]]
    top["Pages"]["Default"] = labels[top["Pages"]["Default"].lower()]
    top = walk_strings(top, image_ref(os.path.join(folder, "Images")))
    docs = {"manifest.json": top}

    for low, name in folders.items():
        with open(os.path.join(pages_dir, name, "manifest.json"), encoding="utf-8") as f:
            page = json.load(f)
        for controller in page.get("Controllers", []):
            for action in (controller.get("Actions") or {}).values():
                action.pop("State", None)
                action.pop("ActionID", None)
                if "Settings" in action:
                    action["Settings"] = walk_strings(
                        action["Settings"], lambda s: labels.get(s.lower(), s))
        page = walk_strings(page, image_ref(os.path.join(pages_dir, name, "Images")))
        docs[unicodedata.normalize("NFC", labels[low] + "/manifest.json")] = page

    lines = sorted(
        "%s\x00%s\n" % (path, hashlib.sha256(jcs(doc)).hexdigest()) for path, doc in docs.items())
    print(hashlib.sha256("".join(lines).encode("utf-8")).hexdigest())


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: refhash.py <profile-folder>")
    main(sys.argv[1])
```

- [ ] **Step 3: Write the failing tests**

`deckformat/normhash/normhash_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package normhash

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// goldenXL is the normalized hash of fixture.XL(), computed by the
// independent reference implementation testdata/refhash.py (see
// TestWriteFixtureForReference). If this test fails after a fixture change,
// recompute it with the reference, never by copying the Go output.
const goldenXL = "3224b89189a93d552a4c479bae1b722c185489a7d8597cc4c8f680f444737200"

func loadP(t *testing.T, fp fixture.Profile) *profile.Profile {
	t.Helper()
	p, err := profile.Load(fp.FS(), fp.Folder())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func hashWith(t *testing.T, fp fixture.Profile, o options) string {
	t.Helper()
	docs, err := normalize(loadP(t, fp), o)
	if err != nil {
		t.Fatal(err)
	}
	h, err := HashDocs(docs)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func hash(t *testing.T, fp fixture.Profile) string { return hashWith(t, fp, full) }

func TestGoldenMatchesReferenceImplementation(t *testing.T) {
	if got := hash(t, fixture.XL()); got != goldenXL {
		t.Fatalf("Hash(fixture.XL()) = %s, reference says %s", got, goldenXL)
	}
}

// Writes the fixture to disk for testdata/refhash.py:
//
//	NORMHASH_FIXTURE_OUT=/tmp/x go test ./normhash -run TestWriteFixtureForReference
//	python3 normhash/testdata/refhash.py /tmp/x/<folder>
func TestWriteFixtureForReference(t *testing.T) {
	out := os.Getenv("NORMHASH_FIXTURE_OUT")
	if out == "" {
		t.Skip("set NORMHASH_FIXTURE_OUT to write the fixture for the reference implementation")
	}
	if err := fixture.WriteTo(out, fixture.XL().FS()); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s/%s", out, fixture.XL().Folder())
}

// --- P9 / P11: what a copy changes must not change the hash ------------------

func onlyActionIDs(p fixture.Profile) fixture.Profile {
	c := fixture.CopyOf(p, "ids")
	for i := range c.Pages {
		c.Pages[i].ID = p.Pages[i].ID
		for j := range c.Pages[i].Buttons {
			c.Pages[i].Buttons[j].Image = p.Pages[i].Buttons[j].Image
		}
	}
	c.Default.ID, c.Current, c.ID = p.Default.ID, p.Current, p.ID
	return c
}

func onlyImageNames(p fixture.Profile) fixture.Profile {
	c := fixture.XL()
	for i := range c.Pages {
		for j := range c.Pages[i].Buttons {
			if c.Pages[i].Buttons[j].Image != "" {
				c.Pages[i].Buttons[j].Image = "RENAMED" + c.Pages[i].Buttons[j].Image
			}
		}
	}
	return c
}

func onlyPageIDs(p fixture.Profile) fixture.Profile {
	c := fixture.CopyOf(p, "pages")
	for i := range c.Pages {
		c.Pages[i].Buttons = p.Pages[i].Buttons
	}
	c.Default.Buttons = p.Default.Buttons
	return c
}

func TestCopyHashesEqual(t *testing.T) {
	base := fixture.XL()
	if hash(t, base) != hash(t, fixture.CopyOf(base, "copy")) {
		t.Fatal("a copy (new ActionIDs, page folders, image names) hashed differently")
	}
}

func TestEachCanonicalizationIsNeeded(t *testing.T) {
	base := fixture.XL()
	cases := []struct {
		name    string
		variant fixture.Profile
		off     options
	}{
		{"ActionID strip (P9)", onlyActionIDs(base), options{strip: false, relabelPages: true, hashImages: true}},
		{"image references by content (P9)", onlyImageNames(base), options{strip: true, relabelPages: true, hashImages: false}},
		{"page relabeling (P11)", onlyPageIDs(base), options{strip: true, relabelPages: false, hashImages: true}},
	}
	for _, c := range cases {
		if hash(t, base) != hash(t, c.variant) {
			t.Errorf("%s: full normalization should hash the pair equal", c.name)
		}
		if hashWith(t, base, c.off) == hashWith(t, c.variant, c.off) {
			t.Errorf("%s: with this step off the pair still hashes equal, so the test cannot see the step working", c.name)
		}
	}
}

// --- runtime fields (P4, R15) and the deck binding ---------------------------

func TestRuntimeFieldsAndDeviceDoNotCount(t *testing.T) {
	base := fixture.XL()
	rt := fixture.XL()
	rt.Current = rt.Pages[1].ID
	rt.Pages[0].Buttons[0].State = 1
	rt.Device = "@(1)[4057/143/<other-deck>]"
	if hash(t, base) != hash(t, rt) {
		t.Fatal("State, Pages.Current or Device.UUID changed the hash")
	}
	if hashWith(t, base, options{strip: false, relabelPages: true, hashImages: true}) ==
		hashWith(t, rt, options{strip: false, relabelPages: true, hashImages: true}) {
		t.Fatal("without stripping the pair still hashes equal; the test cannot see stripping work")
	}
}

// --- real edits must change the hash ----------------------------------------

func TestRealEditsChangeTheHash(t *testing.T) {
	base := hash(t, fixture.XL())
	edits := map[string]func(*fixture.Profile){
		"settings value": func(p *fixture.Profile) {
			p.Pages[0].Buttons[0].Settings = `{"openInBrowser":true,"path":"/tmp/other.sh"}`
		},
		"title":         func(p *fixture.Profile) { p.Pages[0].Buttons[1].Title = "Paste" },
		"moved key":     func(p *fixture.Profile) { p.Pages[0].Buttons[0].Slot = "2,0" },
		"page reorder":  func(p *fixture.Profile) { p.Pages[0], p.Pages[1] = p.Pages[1], p.Pages[0] },
		"image content": func(p *fixture.Profile) { p.Pages[0].Buttons[0].ImageSeed = 9 },
		"images swapped": func(p *fixture.Profile) {
			b := p.Pages[0].Buttons
			b[0].ImageSeed, b[1].ImageSeed = b[1].ImageSeed, b[0].ImageSeed
		},
		"profile renamed": func(p *fixture.Profile) { p.Name = "Other" },
		"image missing":   func(p *fixture.Profile) { p.Pages[0].Buttons[0].MissingImage = true },
	}
	for name, edit := range edits {
		p := fixture.XL()
		edit(&p)
		if hash(t, p) == base {
			t.Errorf("%s: hash unchanged", name)
		}
	}
}

func TestOrphanedImageDoesNotCount(t *testing.T) {
	p := fixture.XL()
	p.Pages[0].Orphans = map[string]byte{"ORPHAN.png": 42}
	if hash(t, p) != hash(t, fixture.XL()) {
		t.Fatal("an unreferenced image file changed the hash")
	}
}

func TestMissingImageIsLabelled(t *testing.T) {
	p := fixture.XL()
	p.Pages[0].Buttons[0].MissingImage = true
	docs, err := Normalize(loadP(t, p))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range docs {
		if strings.Contains(string(d.Value.Encode()), `"missing:Images/`+p.Pages[0].Buttons[0].Image+`"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("a reference to a missing image is not hashed as missing:<reference>")
	}
}

func TestSettingsPageReferencesAreRelabeled(t *testing.T) {
	withRef := func(p fixture.Profile) fixture.Profile {
		p.Pages[0].Buttons[1].Settings = `{"page":"` + strings.ToUpper(p.Pages[1].ID) + `"}`
		return p
	}
	a := withRef(fixture.XL())
	b := withRef(fixture.CopyOf(fixture.XL(), "copy"))
	if hash(t, a) != hash(t, b) {
		t.Fatal("a settings value naming a page of the same profile was not relabeled")
	}
}

func TestDanglingPageIsAnError(t *testing.T) {
	p := fixture.XL()
	fsys := p.FS()
	for name := range fsys {
		if strings.Contains(name, "/Profiles/"+fixture.PageFolder(p.Pages[1].ID)) {
			delete(fsys, name)
		}
	}
	lp, err := profile.Load(fsys, p.Folder())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Hash(lp); !errors.Is(err, ErrDanglingPage) {
		t.Fatalf("err = %v, want ErrDanglingPage", err)
	}
}

func TestPathsAreCanonical(t *testing.T) {
	docs, err := Normalize(loadP(t, fixture.XL()))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, d := range docs {
		paths = append(paths, d.Path)
	}
	if got := strings.Join(paths, " "); got != "default/manifest.json manifest.json page/0/manifest.json page/1/manifest.json" {
		t.Fatalf("paths = %s", got)
	}
}
```

- [ ] **Step 4: Run to verify failure**

Run: `cd deckformat && go test ./normhash/`
Expected: FAIL, `undefined: normalize` (and the other identifiers).

- [ ] **Step 5: Implement**

`deckformat/normhash/normhash.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package normhash computes the normalized profile hash defined in contract C
// § normalized hash (docs/contracts/profile-format.md), norm_version 1. The
// hash must change on a real user edit and on nothing else. The definition
// lives in contract C; this package implements it and nothing more.
package normhash

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// NormVersion is the contract C definition this package implements. Any
// change to the steps below, including StripList, increments it (and ships
// as a store FORMAT migration, ADR 0027).
const NormVersion = 1

// StripList is contract C step 3's strip list: runtime and per-instance
// fields removed before hashing. Paths are member-name paths; Top paths are
// relative to the top-level manifest, Action paths to each action object
// (Controllers[].Actions.<slot>).
var StripList = struct {
	Top    [][]string
	Action [][]string
}{
	Top:    [][]string{{"Device", "UUID"}, {"Pages", "Current"}},
	Action: [][]string{{"State"}, {"ActionID"}},
}

// ErrDanglingPage means Pages.Pages or Pages.Default names a page that has no
// folder.
var ErrDanglingPage = errors.New("page reference without a page folder")

// Doc is one normalized manifest: its canonical path and its tree after
// stripping and relabeling (before JCS).
type Doc struct {
	Path  string
	Value *jsondoc.Value
}

// options exist so tests can switch a step off and show the hash then fails
// its job (known-bad). Production code always uses all steps.
type options struct {
	strip, relabelPages, hashImages bool
}

var full = options{strip: true, relabelPages: true, hashImages: true}

// Normalize returns the profile's normalized manifests, sorted by path.
func Normalize(p *profile.Profile) ([]Doc, error) { return normalize(p, full) }

// Hash returns the normalized hash (hex sha256).
func Hash(p *profile.Profile) (string, error) {
	docs, err := Normalize(p)
	if err != nil {
		return "", err
	}
	return HashDocs(docs)
}

// HashDocs hashes normalized docs: sha256 over "<path>\x00<hex sha256 of the
// JCS bytes>\n" lines sorted bytewise by path (contract C step 4).
func HashDocs(docs []Doc) (string, error) {
	lines := make([]string, 0, len(docs))
	for _, d := range docs {
		c, err := d.Value.Canonical()
		if err != nil {
			return "", fmt.Errorf("%s: %w", d.Path, err)
		}
		sum := sha256.Sum256(c)
		lines = append(lines, d.Path+"\x00"+hex.EncodeToString(sum[:])+"\n")
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PageLabels maps each lower-case page id to its canonical label (contract C
// step 2): "page/<i>" for the i-th entry of Pages.Pages, "default" for
// Pages.Default, "other/<folder>" for any other page folder.
func PageLabels(p *profile.Profile) (map[string]string, error) {
	order, err := p.PageOrder()
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	for i, id := range order {
		if _, dup := labels[id]; dup {
			return nil, fmt.Errorf("%s: page %s listed twice in Pages.Pages: %w", p.Folder, id, profile.ErrMalformed)
		}
		labels[id] = fmt.Sprintf("page/%d", i)
	}
	if def := p.DefaultPage(); def != "" {
		if _, clash := labels[def]; clash {
			return nil, fmt.Errorf("%s: Pages.Default is also in Pages.Pages: %w", p.Folder, profile.ErrMalformed)
		}
		labels[def] = "default"
	}
	for id := range labels {
		if p.Pages[id] == nil {
			return nil, fmt.Errorf("%s: %s: %w", p.Folder, id, ErrDanglingPage)
		}
	}
	for id, pg := range p.Pages {
		if _, ok := labels[id]; !ok {
			labels[id] = "other/" + pg.Folder
		}
	}
	return labels, nil
}

func normalize(p *profile.Profile, o options) ([]Doc, error) {
	labels, err := PageLabels(p)
	if err != nil {
		return nil, err
	}
	label := func(id string) string {
		if !o.relabelPages {
			return id
		}
		return labels[strings.ToLower(id)]
	}

	top := p.Manifest.Clone()
	if o.strip {
		for _, path := range StripList.Top {
			deletePath(top, path)
		}
	}
	if o.relabelPages {
		if list := top.Lookup("Pages", "Pages"); list != nil {
			for _, it := range list.Items() {
				if s, ok := it.Str(); ok {
					it.SetString(label(s))
				}
			}
		}
		if def := top.Lookup("Pages", "Default"); def != nil {
			if s, ok := def.Str(); ok && s != "" {
				def.SetString(label(s))
			}
		}
	}
	if o.hashImages {
		replaceImageRefs(top, p.Images)
	}
	docs := []Doc{{Path: "manifest.json", Value: top}}

	for id, pg := range p.Pages {
		m := pg.Manifest.Clone()
		forEachAction(m, func(action *jsondoc.Value) {
			if o.strip {
				for _, path := range StripList.Action {
					deletePath(action, path)
				}
			}
			if o.relabelPages {
				if settings := action.Get("Settings"); settings != nil {
					settings.Walk(func(_ []string, v *jsondoc.Value) {
						if s, ok := v.Str(); ok {
							if l, isPage := labels[strings.ToLower(s)]; isPage {
								v.SetString(l)
							}
						}
					})
				}
			}
		})
		if o.hashImages {
			replaceImageRefs(m, pg.Images)
		}
		dir := pg.Folder
		if o.relabelPages {
			dir = labels[id]
		}
		docs = append(docs, Doc{Path: norm.NFC.String(dir + "/manifest.json"), Value: m})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, nil
}

func deletePath(v *jsondoc.Value, path []string) {
	parent := v.Lookup(path[:len(path)-1]...)
	if parent != nil {
		parent.Delete(path[len(path)-1])
	}
}

// forEachAction calls fn for every action object: Controllers[].Actions.<slot>.
func forEachAction(page *jsondoc.Value, fn func(*jsondoc.Value)) {
	controllers := page.Get("Controllers")
	for _, c := range controllers.Items() {
		for _, m := range c.Get("Actions").Members() {
			if m.Value.Kind() == jsondoc.Object {
				fn(m.Value)
			}
		}
	}
}

// replaceImageRefs turns every "Images/<file>" string into the referenced
// file's content hash, or "missing:<reference>" when the file is absent
// (contract C step 3).
func replaceImageRefs(m *jsondoc.Value, images map[string][]byte) {
	m.Walk(func(_ []string, v *jsondoc.Value) {
		s, ok := v.Str()
		if !ok || !strings.HasPrefix(s, "Images/") {
			return
		}
		data, found := images[strings.TrimPrefix(s, "Images/")]
		if !found {
			v.SetString("missing:" + s)
			return
		}
		sum := sha256.Sum256(data)
		v.SetString("sha256:" + hex.EncodeToString(sum[:]))
	})
}
```

- [ ] **Step 6: Re-derive the golden value with the reference and run the tests**

```bash
cd deckformat
out=$(mktemp -d)
NORMHASH_FIXTURE_OUT="$out" go test ./normhash -run TestWriteFixtureForReference -v
python3 normhash/testdata/refhash.py "$out/AAAAAAAA-0000-4000-8000-000000000001.sdProfile"
go mod tidy && go test ./... && go vet ./...
```

Expected: the Python line prints `3224b89189a93d552a4c479bae1b722c185489a7d8597cc4c8f680f444737200` (the value in `goldenXL`); every package `ok`. If the Python value differs from `goldenXL`, stop: either the fixture changed or one implementation misreads contract C; find out which before touching the constant.

- [ ] **Step 7: Name the editor temp-file patterns in contract C**

In `docs/contracts/profile-format.md` § File allow-list, replace `and editor temp files.` with ``and editor temp files (`*~`, `.*.swp`, `#*#`).``

- [ ] **Step 8: Commit, push, PR, merge** (as in Task 3 step 6)

```bash
git add deckformat docs/contracts/profile-format.md
git commit -m "feat(#<n>): contract C normalized hash (norm_version 1) with an independent Python reference"
```

---

### Task 5: Semantic diff

Issue: "deckformat: semantic diff, redaction and the write guard" (Tasks 5 and 6; new worktree `<n>-diff-redact`).

**Files:**
- Create: `deckformat/semdiff/semdiff.go`, `deckformat/semdiff/semdiff_test.go`

**Interfaces:**
- Consumes: `jsondoc`, `profile`, `normhash.Normalize`, `normhash.PageLabels`.
- Produces (package `semdiff`): `Kind` (`Added`, `Removed`, `Modified`); `Mode` (`Raw`, `Semantic`); `Change{Profile, Where, Path string; Kind Kind; Before, After string}` with JSON members `profile, where, path, kind, before, after`, and `(Change) String()`; `Values(profileName, where string, a, b *jsondoc.Value) []Change`; `Profiles(before, after *profile.Profile, mode Mode) ([]Change, error)`; `Sets(before, after map[string]*profile.Profile, mode Mode) ([]Change, error)`.

`Where` reads like the app: `page 1 › key 0,0`, `page 2 › dial 7,3`, `default page`, `sub-page <folder>`, `profile`, `files`, `profiles`. Raw-mode page names also carry the page id in parentheses, because during an observation the id is part of what is being observed; observation reports then replace every id with a stable pseudonym (Task 7), so the relation survives and the real id does not.

- [ ] **Step 1: Write the failing tests**

`deckformat/semdiff/semdiff_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package semdiff

import (
	"strings"
	"testing"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

func loadP(t *testing.T, fp fixture.Profile) *profile.Profile {
	t.Helper()
	p, err := profile.Load(fp.FS(), fp.Folder())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func render(cs []Change) string {
	var lines []string
	for _, c := range cs {
		lines = append(lines, c.String())
	}
	return strings.Join(lines, "\n")
}

func TestValues(t *testing.T) {
	a, _ := jsondoc.Parse([]byte(`{"a":1,"b":{"c":[1,2]},"d":"x"}`))
	b, _ := jsondoc.Parse([]byte(`{"a":2,"b":{"c":[1]},"e":true}`))
	got := render(Values("P", "profile", a, b))
	want := strings.Join([]string{
		"P: profile › a changed: 1 → 2",
		"P: profile › b.c[1] removed (was 2)",
		`P: profile › d removed (was "x")`,
		"P: profile › e added: true",
	}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSemanticDiffNamesKeysAndSettings(t *testing.T) {
	before := fixture.XL()
	after := fixture.XL()
	after.Pages[0].Buttons[0].Settings = `{"openInBrowser":true,"path":"/tmp/other.sh"}`
	after.Pages[1].Buttons[0].Title = "Next"
	cs, err := Profiles(loadP(t, before), loadP(t, after), Semantic)
	if err != nil {
		t.Fatal(err)
	}
	got := render(cs)
	for _, want := range []string{
		`Fixture XL: page 1 › key 0,0 › Settings.path changed: "/Users/<user>/bin/demo.sh" → "/tmp/other.sh"`,
		`Fixture XL: page 2 › key 7,3 › States[0].Title changed: "" → "Next"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if len(cs) != 2 {
		t.Errorf("want exactly 2 changes, got\n%s", got)
	}
}

func TestSemanticDiffIgnoresWhatTheHashIgnores(t *testing.T) {
	cs, err := Profiles(loadP(t, fixture.XL()), loadP(t, fixture.CopyOf(fixture.XL(), "copy")), Semantic)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 0 {
		t.Fatalf("a copy should show no semantic change, got\n%s", render(cs))
	}
}

func TestRawDiffShowsRuntimeAndFiles(t *testing.T) {
	before := fixture.XL()
	after := fixture.XL()
	after.Pages[0].Buttons[0].State = 1
	after.Pages[0].Orphans = map[string]byte{"NEW.png": 7}
	cs, err := Profiles(loadP(t, before), loadP(t, after), Raw)
	if err != nil {
		t.Fatal(err)
	}
	got := render(cs)
	for _, want := range []string{"key 0,0 › State changed: 0 → 1", "files › Profiles/" + fixture.PageFolder(after.Pages[0].ID) + "/Images/NEW.png added"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestDialsAreNamed(t *testing.T) {
	before := fixture.XL()
	before.Pages[1].Encoder = true
	after := before
	after.Pages = append([]fixture.Page(nil), before.Pages...)
	after.Pages[1].Buttons = []fixture.Button{after.Pages[1].Buttons[0]}
	after.Pages[1].Buttons[0].Title = "Vol"
	cs, err := Profiles(loadP(t, before), loadP(t, after), Semantic)
	if err != nil {
		t.Fatal(err)
	}
	if got := render(cs); !strings.Contains(got, "page 2 › dial 7,3") {
		t.Fatalf("encoder change not named as a dial:\n%s", got)
	}
}

func TestSets(t *testing.T) {
	a := loadP(t, fixture.XL())
	b := loadP(t, fixture.CopyOf(fixture.XL(), "other"))
	cs, err := Sets(map[string]*profile.Profile{a.Folder: a}, map[string]*profile.Profile{a.Folder: a, b.Folder: b}, Raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Kind != Added || cs[0].Where != "profiles" {
		t.Fatalf("Sets = %s", render(cs))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd deckformat && go test ./semdiff/`
Expected: FAIL, `undefined: Values`.

- [ ] **Step 3: Implement**

`deckformat/semdiff/semdiff.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package semdiff reports differences between profiles in Stream Deck terms
// ("page 2 › key 3,1", "Settings.entity") instead of file paths. Raw mode
// compares manifests as stored, runtime fields and ids included, which is
// what observing the app needs. Semantic mode compares the normalized forms
// of contract C, which is what "did the user change anything" needs.
package semdiff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/normhash"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// Kind of change.
type Kind string

// Change kinds.
const (
	Added    Kind = "added"
	Removed  Kind = "removed"
	Modified Kind = "modified"
)

// Mode selects what is compared.
type Mode int

// Modes.
const (
	Raw      Mode = iota // manifests and files as stored
	Semantic             // contract C normalized forms
)

// Change is one difference.
type Change struct {
	Profile string `json:"profile"` // profile name, or folder when unnamed
	Where   string `json:"where"`   // "profile", "page 2 › key 3,1", "files", ...
	Path    string `json:"path"`    // key path inside Where
	Kind    Kind   `json:"kind"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
}

// String renders a change on one line.
func (c Change) String() string {
	loc := c.Where
	if c.Path != "" {
		loc += " › " + c.Path
	}
	switch c.Kind {
	case Added:
		return fmt.Sprintf("%s: %s added: %s", c.Profile, loc, c.After)
	case Removed:
		return fmt.Sprintf("%s: %s removed (was %s)", c.Profile, loc, c.Before)
	}
	return fmt.Sprintf("%s: %s changed: %s → %s", c.Profile, loc, c.Before, c.After)
}

// Values compares two JSON trees and returns one change per differing leaf.
// where and profileName label every change.
func Values(profileName, where string, a, b *jsondoc.Value) []Change {
	var out []Change
	emit := func(path []string, kind Kind, x, y *jsondoc.Value) {
		c := Change{Profile: profileName, Where: where, Path: renderPath(path), Kind: kind}
		if x != nil {
			c.Before = string(x.Encode())
		}
		if y != nil {
			c.After = string(y.Encode())
		}
		out = append(out, c)
	}
	diffValues(nil, a, b, emit)
	return out
}

func diffValues(path []string, a, b *jsondoc.Value, emit func([]string, Kind, *jsondoc.Value, *jsondoc.Value)) {
	switch {
	case a == nil && b == nil:
		return
	case a == nil:
		emit(path, Added, nil, b)
		return
	case b == nil:
		emit(path, Removed, a, nil)
		return
	case a.Kind() != b.Kind():
		emit(path, Modified, a, b)
		return
	}
	switch a.Kind() {
	case jsondoc.Object:
		seen := map[string]bool{}
		for _, m := range a.Members() {
			seen[m.Name] = true
			diffValues(append(path[:len(path):len(path)], m.Name), m.Value, b.Get(m.Name), emit)
		}
		for _, m := range b.Members() {
			if !seen[m.Name] {
				emit(append(path[:len(path):len(path)], m.Name), Added, nil, m.Value)
			}
		}
	case jsondoc.Array:
		ai, bi := a.Items(), b.Items()
		for i := 0; i < len(ai) || i < len(bi); i++ {
			var x, y *jsondoc.Value
			if i < len(ai) {
				x = ai[i]
			}
			if i < len(bi) {
				y = bi[i]
			}
			diffValues(append(path[:len(path):len(path)], "["+strconv.Itoa(i)+"]"), x, y, emit)
		}
	default:
		if !bytes.Equal(a.Raw(), b.Raw()) {
			emit(path, Modified, a, b)
		}
	}
}

func renderPath(path []string) string {
	var b strings.Builder
	for _, p := range path {
		if strings.HasPrefix(p, "[") {
			b.WriteString(p)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(p)
	}
	return b.String()
}

// humanPage turns a canonical page label into words.
func humanPage(label string) string {
	switch {
	case label == "default":
		return "default page"
	case strings.HasPrefix(label, "page/"):
		n, err := strconv.Atoi(strings.TrimPrefix(label, "page/"))
		if err == nil {
			return fmt.Sprintf("page %d", n+1)
		}
	case strings.HasPrefix(label, "other/"):
		return "sub-page " + strings.TrimPrefix(label, "other/")
	}
	return "page " + label
}

// pageChanges compares two page manifests and moves the key slot out of the
// path into Where: "page 1 › key 3,1" + "Settings.path".
func pageChanges(profileName, page string, a, b *jsondoc.Value) []Change {
	var out []Change
	for _, c := range Values(profileName, page, a, b) {
		parts := strings.SplitN(c.Path, ".", 3)
		// Controllers[i].Actions.<slot>[.rest]
		if len(parts) >= 2 && strings.HasPrefix(parts[0], "Controllers[") && strings.HasPrefix(parts[1], "Actions") {
			rest := ""
			if len(parts) == 3 {
				rest = parts[2]
			}
			slot, tail, _ := strings.Cut(rest, ".")
			if slot != "" {
				control := "key"
				if ctl := controllerType(a, b, parts[0]); ctl == "Encoder" {
					control = "dial"
				}
				c.Where = page + " › " + control + " " + slot
				c.Path = tail
			}
		}
		out = append(out, c)
	}
	return out
}

// controllerType returns the Type of the controller named by "Controllers[i]".
func controllerType(a, b *jsondoc.Value, elem string) string {
	i, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(elem, "Controllers["), "]"))
	if err != nil {
		return ""
	}
	for _, m := range []*jsondoc.Value{b, a} {
		items := m.Get("Controllers").Items()
		if i < len(items) {
			if s, ok := items[i].Get("Type").Str(); ok {
				return s
			}
		}
	}
	return ""
}

func displayName(p *profile.Profile) string {
	if n := p.Name(); n != "" {
		return n
	}
	return p.Folder
}

// Profiles compares two versions of one profile.
func Profiles(before, after *profile.Profile, mode Mode) ([]Change, error) {
	name := displayName(after)
	if mode == Semantic {
		a, err := normhash.Normalize(before)
		if err != nil {
			return nil, err
		}
		b, err := normhash.Normalize(after)
		if err != nil {
			return nil, err
		}
		return docChanges(name, docMap(a), docMap(b)), nil
	}
	return rawChanges(name, before, after)
}

func docMap(docs []normhash.Doc) map[string]*jsondoc.Value {
	m := map[string]*jsondoc.Value{}
	for _, d := range docs {
		m[d.Path] = d.Value
	}
	return m
}

func docChanges(name string, a, b map[string]*jsondoc.Value) []Change {
	var out []Change
	for _, path := range unionKeys(a, b) {
		if path == "manifest.json" {
			out = append(out, Values(name, "profile", a[path], b[path])...)
			continue
		}
		label := strings.TrimSuffix(path, "/manifest.json")
		out = append(out, pageChanges(name, humanPage(label), a[path], b[path])...)
	}
	return out
}

func rawChanges(name string, before, after *profile.Profile) ([]Change, error) {
	out := Values(name, "profile", before.Manifest, after.Manifest)
	labels := func(p *profile.Profile) map[string]string {
		l, err := normhash.PageLabels(p)
		if err != nil {
			l = map[string]string{}
		}
		return l
	}
	la, lb := labels(before), labels(after)
	keys := map[string]bool{}
	for k := range before.Pages {
		keys[k] = true
	}
	for k := range after.Pages {
		keys[k] = true
	}
	var sortedKeys []string
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	for _, k := range sortedKeys {
		label := lb[k]
		if label == "" {
			label = la[k]
		}
		if label == "" {
			label = "other/" + k
		}
		var a, b *jsondoc.Value
		if pg := before.Pages[k]; pg != nil {
			a = pg.Manifest
		}
		if pg := after.Pages[k]; pg != nil {
			b = pg.Manifest
		}
		out = append(out, pageChanges(name, humanPage(label)+" ("+k+")", a, b)...)
	}
	out = append(out, fileChanges(name, before.Files(), after.Files())...)
	return out, nil
}

// fileChanges reports added, removed and changed non-manifest files by
// content digest.
func fileChanges(name string, a, b map[string][]byte) []Change {
	digest := func(data []byte) string {
		sum := sha256.Sum256(data)
		return "sha256:" + hex.EncodeToString(sum[:8])
	}
	var out []Change
	for _, path := range unionKeys(a, b) {
		if strings.HasSuffix(path, "manifest.json") {
			continue
		}
		x, inA := a[path]
		y, inB := b[path]
		switch {
		case !inA:
			out = append(out, Change{Profile: name, Where: "files", Path: path, Kind: Added, After: digest(y)})
		case !inB:
			out = append(out, Change{Profile: name, Where: "files", Path: path, Kind: Removed, Before: digest(x)})
		case !bytes.Equal(x, y):
			out = append(out, Change{Profile: name, Where: "files", Path: path, Kind: Modified, Before: digest(x), After: digest(y)})
		}
	}
	return out
}

func unionKeys[V any](a, b map[string]V) []string {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Sets compares two sets of profiles keyed by folder name: whole profiles
// added or removed, and changes inside profiles present in both.
func Sets(before, after map[string]*profile.Profile, mode Mode) ([]Change, error) {
	var out []Change
	for _, folder := range unionKeys(before, after) {
		a, b := before[folder], after[folder]
		switch {
		case a == nil:
			out = append(out, Change{Profile: displayName(b), Where: "profiles", Path: folder, Kind: Added, After: displayName(b)})
		case b == nil:
			out = append(out, Change{Profile: displayName(a), Where: "profiles", Path: folder, Kind: Removed, Before: displayName(a)})
		default:
			cs, err := Profiles(a, b, mode)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", folder, err)
			}
			out = append(out, cs...)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd deckformat && go test ./semdiff/ && go vet ./semdiff/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add deckformat/semdiff
git commit -m "feat(#<n>): semantic diff in Stream Deck terms, raw and normalized"
```

---

### Task 6: Redaction, the write guard and fixture export

**Files:**
- Create: `deckformat/pathguard/pathguard.go`, `deckformat/pathguard/pathguard_test.go`, `deckformat/redact/redact.go`, `deckformat/redact/redact_test.go`

**Interfaces:**
- Consumes: `jsondoc`, `profile`, `normhash` (tests), `fixture` (tests).
- Produces (package `pathguard`): `ErrInside`, `ErrNotName`; `RefuseInside(target string, roots ...string) error` (resolves both sides through symlinks, including a target that does not exist yet, ignores letter case, and treats `../` correctly); `SingleName(name string) error`.
- Produces (package `redact`): placeholders `User = "<user>"`, `Host = "<host>"`, `Deck = "<deck>"`, `Redacted = "<redacted>"`; `Options{UserNames, HostNames, Serials []string}`; `New(Options) (*Redactor, error)` (refuses names under 3 characters); `SerialsFrom(ids ...string) []string`; `(*Redactor) String(string) string`; `(*Redactor) Value(*jsondoc.Value) *jsondoc.Value` (deep copy); `Strings(*profile.Profile) []string`; `SyntheticPNG([]byte) []byte`; `ErrOutputExists`; `ExportFixture(p *profile.Profile, r *Redactor, sourceRoot, outDir, folder string) error`.

What is removed: the serial part of every device id (vendor and product are public model ids and stay); **every serial the caller has seen, wherever it appears** (`Options.Serials`, collected with `SerialsFrom` from the prefs device keys and the manifests' `Device.UUID`s, so a bare serial in a plugin setting or a prefs field is caught too); any home directory name (macOS, Linux, Windows); the user's and the Mac's names wherever they appear; and the value of any member whose name looks like a secret (`token`, `secret`, `passw…`, `api_key`, `auth…`, `cookie`, `session`). Images are replaced by a synthetic 4×4 PNG derived from the original's hash, so distinct images stay distinct and hash tests keep their meaning. Redaction is a first pass; a human reviews `Strings(...)` and the leak scan runs before anything is committed.

`pathguard` exists because a text comparison of paths is not a guard: a symlinked output directory, a `../` in a folder name, or a different letter case on a case-insensitive disk each walk past it. `ExportFixture` checks its **final** target (`outDir/folder`) and requires `folder` to be one path element; the CLI (Task 11) applies the same guard to `observe stop --out` and to `fixture export` before anything is read.

- [ ] **Step 1: Write the failing guard tests**

`deckformat/pathguard/pathguard_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefuseInside(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	if err := os.MkdirAll(filepath.Join(root, "ProfilesV3"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "out")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink outside the root that points into it.
	link := filepath.Join(base, "innocent")
	if err := os.Symlink(filepath.Join(root, "ProfilesV3"), link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		inside bool
	}{
		{root, true},
		{filepath.Join(root, "ProfilesV3", "x.md"), true},
		{filepath.Join(root, "ProfilesV3", "new", "deeper", "x.md"), true}, // does not exist yet
		{filepath.Join(outside, "..", "app", "ProfilesV3", "x.md"), true},  // "../" walk back in
		{filepath.Join(link, "x.md"), true},                                // symlinked directory
		{filepath.Join(link, "new", "x.md"), true},                         // symlink, then a missing part
		{strings.ToUpper(filepath.Join(root, "PROFILESV3", "x.md")), true}, // letter case
		{filepath.Join(outside, "x.md"), false},
		{filepath.Join(base, "app-other", "x.md"), false}, // shares a prefix, not a parent
		{filepath.Join(base, "..app", "x.md"), false},
	}
	for _, c := range cases {
		err := RefuseInside(c.target, "", root)
		if got := errors.Is(err, ErrInside); got != c.inside {
			t.Errorf("RefuseInside(%s) = %v, want inside=%v", c.target, err, c.inside)
		}
	}
}

func TestSingleName(t *testing.T) {
	for _, ok := range []string{"A.sdProfile", "x", "..x.sdProfile"} {
		if err := SingleName(ok); err != nil {
			t.Errorf("SingleName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "../A.sdProfile", "a/b.sdProfile", `a\b.sdProfile`} {
		if err := SingleName(bad); !errors.Is(err, ErrNotName) {
			t.Errorf("SingleName(%q) accepted", bad)
		}
	}
}
```

The cases a text-only check gets wrong are the known-bad inputs here: the symlinked directory, the symlink followed by a missing part, the `../` walk back in, the upper-cased path, and the sibling that only shares a prefix (`app-other`, `..app`).

- [ ] **Step 2: Run to verify failure, then implement the guard**

Run: `cd deckformat && go test ./pathguard/` → FAIL, `undefined: RefuseInside`.

`deckformat/pathguard/pathguard.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package pathguard keeps writes out of protected directories, such as the
// Stream Deck app's data root. Every command that writes to a path a person
// typed checks the final target here first.
package pathguard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrInside means a write target lies inside a protected root.
var ErrInside = errors.New("pathguard: target is inside a protected directory")

// ErrNotName means a name that must be a single path element is not one.
var ErrNotName = errors.New("pathguard: not a single path element")

// RefuseInside returns ErrInside if target is, or would be created, inside any
// of roots. Both sides are made absolute and resolved through symlinks (the
// deepest part of each path that exists is resolved, the rest is appended), so
// neither a symlinked output directory nor a "../" segment can reach a root.
// The comparison ignores letter case: macOS and Windows file systems usually
// do, and refusing a few more paths than necessary is the safe direction.
func RefuseInside(target string, roots ...string) error {
	t, err := resolve(target)
	if err != nil {
		return err
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		r, err := resolve(root)
		if err != nil {
			return err
		}
		if within(strings.ToLower(r), strings.ToLower(t)) {
			return fmt.Errorf("%w: %s is inside %s", ErrInside, target, root)
		}
	}
	return nil
}

// SingleName returns ErrNotName unless name is one path element: not empty,
// not "." or "..", and without a separator.
func SingleName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: %q", ErrNotName, name)
	}
	return nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolve returns p made absolute, with its deepest existing ancestor (or p
// itself) resolved through symlinks.
func resolve(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	cur, rest := abs, ""
	for {
		if _, err := os.Lstat(cur); err == nil {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
```

Run: `cd deckformat && go test ./pathguard/` → `ok`.

- [ ] **Step 3: Write the failing redaction tests**

`deckformat/redact/redact_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package redact

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/normhash"
	"github.com/csmarshall/schrodeck/deckformat/pathguard"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// Personal-looking test inputs are assembled from pieces so this file never
// matches the repository's leak scan.
var (
	realHome   = "/Users/" + "alice"
	realSerial = "AB12" + "CD34EF"
	realDevice = "@(1)[4057/143/" + realSerial + "]"
)

// leakPattern mirrors the generic patterns of tools/ci/leak-scan.sh plus the
// names used below; output must never match it.
var leakPattern = regexp.MustCompile(`(?i)/Users/[a-z]|@\([0-9]+\)\[[0-9]+/[0-9]+/[A-Za-z0-9]{6,}\]|alice|mbp-77`)

func newR(t *testing.T) *Redactor {
	t.Helper()
	r, err := New(Options{UserNames: []string{"alice"}, HostNames: []string{"alice-mbp-77"}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestString(t *testing.T) {
	r := newR(t)
	cases := map[string]string{
		realHome + "/bin/x.sh":               "/Users/<user>/bin/x.sh",
		"file://" + realHome + "/Docs":       "file:///Users/<user>/Docs",
		`C:\Users\` + "alice" + `\x`:         `C:\Users\<user>\x`,
		"/home/" + "alice" + "/x":            "/home/<user>/x",
		realDevice:                           "@(1)[4057/143/<deck>]",
		"see " + realDevice + " and more":    "see @(1)[4057/143/<deck>] and more",
		"http://alice-mbp-77.local:8123/api": "http://<host>.local:8123/api",
		"Alice's buttons":                    "<user>'s buttons",
		"/Users/<user>/already/redacted":     "/Users/<user>/already/redacted",
		"@(0)[]":                             "@(0)[]",
	}
	for in, want := range cases {
		if got := r.String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortNamesAreRefused(t *testing.T) {
	if _, err := New(Options{UserNames: []string{"al"}}); err == nil {
		t.Fatal("a two-letter user name would redact every 'al' in every title")
	}
}

func TestValueRedactsNamesValuesAndSecrets(t *testing.T) {
	r := newR(t)
	in := `{"Devices":{"` + realDevice + `":{"DeviceName":"alice desk"}},"Settings":{"accessToken":"abc123","apiKey":42,"path":"` + realHome + `/x","nested":{"sessionCookie":"zzz"}}}`
	v, err := jsondoc.Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out := string(r.Value(v).Encode())
	want := `{"Devices":{"@(1)[4057/143/<deck>]":{"DeviceName":"<user> desk"}},"Settings":{"accessToken":"<redacted>","apiKey":"<redacted>","path":"/Users/<user>/x","nested":{"sessionCookie":"<redacted>"}}}`
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	if string(v.Encode()) != in {
		t.Fatal("Value modified its input")
	}
}

func personal() fixture.Profile {
	p := fixture.XL()
	p.Name = "alice's XL"
	p.Device = realDevice
	p.Pages[0].Buttons[0].Settings = `{"path":"` + realHome + `/bin/demo.sh","token":"s3cret"}`
	p.Pages[0].Buttons[1].Title = "alice-mbp-77"
	return p
}

func TestExportFixtureLeavesNothingPersonal(t *testing.T) {
	src := personal()
	p, err := profile.Load(src.FS(), src.Folder())
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := ExportFixture(p, newR(t), "", out, "FIXTURE.sdProfile"); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if m := leakPattern.Find(data); m != nil {
			t.Errorf("%s still contains %q", path, m)
		}
		if bytes.Contains(data, []byte("s3cret")) {
			t.Errorf("%s still contains the token", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The exported fixture must still load and hash.
	exported, err := profile.Load(os.DirFS(out), "FIXTURE.sdProfile")
	if err != nil {
		t.Fatalf("exported fixture does not load: %v", err)
	}
	if _, err := normhash.Hash(exported); err != nil {
		t.Fatalf("exported fixture does not hash: %v", err)
	}
}

// Known-bad control: without redaction the same scan finds the leaks, so the
// test above is able to fail.
func TestLeakPatternSeesUnredactedFixture(t *testing.T) {
	src := personal()
	found := false
	for _, f := range src.FS() {
		if leakPattern.Match(f.Data) {
			found = true
		}
	}
	if !found {
		t.Fatal("leak pattern does not match the unredacted fixture")
	}
}

func TestExportKeepsDistinctImagesDistinct(t *testing.T) {
	a, b := SyntheticPNG(fixture.PNG(1)), SyntheticPNG(fixture.PNG(2))
	if bytes.Equal(a, b) {
		t.Fatal("two different images became the same synthetic image")
	}
	if !bytes.Equal(a, SyntheticPNG(fixture.PNG(1))) {
		t.Fatal("SyntheticPNG is not deterministic")
	}
}

func TestExportRefusesUnsafeTargets(t *testing.T) {
	p, _ := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	r := newR(t)
	src := t.TempDir()
	if err := ExportFixture(p, r, src, filepath.Join(src, "inside"), "F.sdProfile"); err == nil {
		t.Fatal("export into the source tree was allowed")
	}
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ExportFixture(p, r, src, full, "F.sdProfile"); !errors.Is(err, ErrOutputExists) {
		t.Fatalf("export into a non-empty dir: %v", err)
	}
	// Known-bad: a folder name that walks out of outDir and back into the source.
	out := filepath.Join(filepath.Dir(src), "out")
	escape := filepath.Join("..", filepath.Base(src), "F.sdProfile")
	if err := ExportFixture(p, r, src, out, escape); !errors.Is(err, pathguard.ErrNotName) {
		t.Fatalf("a ../ folder name was accepted: %v", err)
	}
	// Known-bad: an output directory that is a symlink into the source.
	link := filepath.Join(t.TempDir(), "looks-safe")
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}
	if err := ExportFixture(p, r, src, link, "F.sdProfile"); !errors.Is(err, pathguard.ErrInside) {
		t.Fatalf("export through a symlink into the source tree: %v", err)
	}
	if entries, _ := os.ReadDir(src); len(entries) != 0 {
		t.Fatalf("a refused export still wrote into the source tree: %v", entries)
	}
}

func TestBareSerialsAreRedacted(t *testing.T) {
	r, err := New(Options{Serials: SerialsFrom(realDevice, "@(0)[]", "not an id")})
	if err != nil {
		t.Fatal(err)
	}
	// A serial on its own, outside any device id: a plugin setting or a prefs field.
	in := `{"deviceSerial":"` + realSerial + `","note":"deck ` + strings.ToLower(realSerial) + ` on desk"}`
	got := r.String(in)
	if strings.Contains(strings.ToLower(got), strings.ToLower(realSerial)) {
		t.Fatalf("bare serial survived: %s", got)
	}
	if !strings.Contains(got, Deck) {
		t.Fatalf("serial not replaced by %s: %s", Deck, got)
	}
	// Known-bad control: without the collected serials the same input leaks.
	plain, _ := New(Options{})
	if !strings.Contains(plain.String(in), realSerial) {
		t.Fatal("control: a redactor without serials should not catch a bare serial; the test above proves nothing")
	}
}

func TestStringsListsEverythingForReview(t *testing.T) {
	p, _ := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	got := strings.Join(Strings(p), "\n")
	for _, want := range []string{"Fixture XL", "/Users/<user>/bin/demo.sh", "Copy", "openInBrowser"} {
		if !strings.Contains(got, want) {
			t.Errorf("Strings lacks %q", want)
		}
	}
}
```

`TestBareSerialsAreRedacted` carries its own known-bad control (a redactor without the collected serials must leak the same input), and `TestExportRefusesUnsafeTargets` covers a `../` folder name and a symlinked output directory, and checks that a refused export wrote nothing.

- [ ] **Step 4: Run to verify failure**

Run: `cd deckformat && go test ./redact/`
Expected: FAIL, `undefined: New`.

- [ ] **Step 5: Implement**

`deckformat/redact/redact.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package redact scrubs personal identifiers from profile data, so that
// observations and fixtures from real machines can be shared (ADR 0031).
// Redaction errs toward removing too much. It is a first pass: anything that
// will be published still gets a human review and the repository leak scan.
package redact

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/pathguard"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// Placeholders written in place of what was removed.
const (
	User     = "<user>"
	Host     = "<host>"
	Deck     = "<deck>"
	Redacted = "<redacted>"
)

var (
	// A Stream Deck device id embeds the USB serial: @(1)[vendor/product/serial].
	// Vendor and product are public model ids and are kept.
	deviceID     = regexp.MustCompile(`@\((\d+)\)\[(\d+)/(\d+)/[^\]\s"]+\]`)
	deviceSerial = regexp.MustCompile(`@\(\d+\)\[\d+/\d+/([^\]\s"]+)\]`)
	// Home directories: macOS, Linux, Windows (with either separator).
	homeDir = regexp.MustCompile(`(/Users/|/home/|[A-Za-z]:\\Users\\|[A-Za-z]:/Users/)[^/\\\s"'<>]+`)
	// Member names whose values are secrets whatever they contain.
	secretName = regexp.MustCompile(`(?i)(token|secret|passw|api[_-]?key|auth|cookie|session)`)
)

// Options lists identifiers known to the caller (from the host connector).
// Serials are the serial parts of every device id the caller has seen (prefs
// keys and manifests, see SerialsFrom): they are removed wherever they appear,
// not only inside a device id.
type Options struct {
	UserNames []string
	HostNames []string
	Serials   []string
}

// Redactor applies the rules.
type Redactor struct {
	literals []literal
}

type literal struct {
	re          *regexp.Regexp
	placeholder string
}

// New builds a redactor. Names shorter than 3 characters are refused rather
// than redacted everywhere they occur as a substring.
func New(o Options) (*Redactor, error) {
	r := &Redactor{}
	add := func(names []string, placeholder string) error {
		for _, n := range names {
			if n == "" {
				continue
			}
			if len(n) < 3 {
				return fmt.Errorf("redact: %q is too short to redact safely", n)
			}
			r.literals = append(r.literals, literal{regexp.MustCompile(`(?i)` + regexp.QuoteMeta(n)), placeholder})
		}
		return nil
	}
	if err := add(o.HostNames, Host); err != nil {
		return nil, err
	}
	if err := add(o.UserNames, User); err != nil {
		return nil, err
	}
	if err := add(o.Serials, Deck); err != nil {
		return nil, err
	}
	// Longest first, so "alice-mbp" is replaced before "alice".
	sort.SliceStable(r.literals, func(i, j int) bool {
		return len(r.literals[i].re.String()) > len(r.literals[j].re.String())
	})
	return r, nil
}

// SerialsFrom returns the serial part of every device id among ids (an
// "@(n)[vendor/product/serial]" string); other strings and serial-less ids
// such as a virtual deck's "@(0)[]" contribute nothing.
func SerialsFrom(ids ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		for _, m := range deviceSerial.FindAllStringSubmatch(id, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	sort.Strings(out)
	return out
}

// String redacts one string.
func (r *Redactor) String(s string) string {
	s = deviceID.ReplaceAllString(s, "@($1)[$2/$3/"+Deck+"]")
	s = homeDir.ReplaceAllString(s, "${1}"+User)
	for _, l := range r.literals {
		s = l.re.ReplaceAllString(s, l.placeholder)
	}
	return s
}

// Value returns a redacted deep copy: every string and member name is
// redacted, and the values of secret-looking members become "<redacted>".
func (r *Redactor) Value(v *jsondoc.Value) *jsondoc.Value {
	c := v.Clone()
	r.redactInPlace(c)
	return c
}

func (r *Redactor) redactInPlace(v *jsondoc.Value) {
	switch v.Kind() {
	case jsondoc.String:
		s, _ := v.Str()
		if red := r.String(s); red != s {
			v.SetString(red)
		}
	case jsondoc.Object:
		for i, m := range v.Members() {
			if red := r.String(m.Name); red != m.Name {
				v.RenameMember(i, red)
			}
			if secretName.MatchString(m.Name) && m.Value.Kind() != jsondoc.Object && m.Value.Kind() != jsondoc.Array {
				m.Value.SetString(Redacted)
				continue
			}
			r.redactInPlace(m.Value)
		}
	case jsondoc.Array:
		for _, it := range v.Items() {
			r.redactInPlace(it)
		}
	}
}

// Strings lists every distinct string value and member name in a profile's
// manifests, for the human review that must precede publishing a fixture.
func Strings(p *profile.Profile) []string {
	set := map[string]bool{}
	collect := func(v *jsondoc.Value) {
		v.Walk(func(path []string, x *jsondoc.Value) {
			if len(path) > 0 && !strings.HasPrefix(path[len(path)-1], "[") {
				set[path[len(path)-1]] = true
			}
			if s, ok := x.Str(); ok && s != "" {
				set[s] = true
			}
		})
	}
	collect(p.Manifest)
	for _, pg := range p.Pages {
		collect(pg.Manifest)
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// SyntheticPNG replaces an image: a 4×4 PNG whose pixels are derived from the
// original's sha256, so distinct images stay distinct (hash tests keep their
// meaning) while the picture itself is gone.
func SyntheticPNG(original []byte) []byte {
	sum := sha256.Sum256(original)
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < 16; i++ {
		img.Set(i%4, i/4, color.RGBA{sum[(i*2)%32], sum[(i*2+1)%32], sum[(i*5)%32], 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ErrOutputExists means the export target already has content.
var ErrOutputExists = errors.New("redact: output directory is not empty")

// ExportFixture writes a redacted copy of p to outDir/<folder>: manifests
// redacted, images replaced by SyntheticPNG. folder must be a single path
// element, outDir must be empty or absent, and the final target must not lie
// inside sourceRoot (the folder p was read from), so an export can never write
// into the app's own data (pathguard resolves symlinks and "../").
func ExportFixture(p *profile.Profile, r *Redactor, sourceRoot, outDir, folder string) error {
	if err := pathguard.SingleName(folder); err != nil {
		return err
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	if err := pathguard.RefuseInside(filepath.Join(absOut, folder), sourceRoot); err != nil {
		return err
	}
	if entries, err := os.ReadDir(absOut); err == nil && len(entries) > 0 {
		return ErrOutputExists
	}
	for rel, data := range p.Files() {
		if strings.HasSuffix(rel, "manifest.json") {
			doc, err := jsondoc.Parse(data)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			data = r.Value(doc).Encode()
		} else {
			data = SyntheticPNG(data)
		}
		target := filepath.Join(absOut, folder, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 6: Run to verify it passes, and run the repository leak scan over the new tests**

Run: `cd deckformat && go test ./redact/ ./pathguard/ && cd .. && git add deckformat/redact deckformat/pathguard && tools/ci/leak-scan.sh`
Expected: `ok` twice; `leak-scan: clean`. The test inputs build personal-looking strings by concatenation precisely so this scan stays clean.

- [ ] **Step 7: Commit, push, PR, merge** (as in Task 3 step 6)

```bash
git add deckformat/redact deckformat/pathguard
git commit -m "feat(#<n>): redaction (including bare serials), the write guard, and redacted fixture export"
```

---

### Task 7: The observation harness

Issue: "deckformat: observation harness and probe runner" (Tasks 7 and 8; new worktree `<n>-observe-probe`).

**Files:**
- Create: `deckformat/observe/observe.go`, `deckformat/observe/observe_test.go`

**Interfaces:**
- Consumes: `profile.LoadAll`, `jsondoc`, `normhash.NormVersion`, `semdiff.Sets`, `semdiff.Values`, `redact.Redactor`.
- Produces (package `observe`): `Snapshot{TakenAt time.Time; AppVersion string; Profiles map[string]*profile.Profile; LoadErrors []string; Prefs *jsondoc.Value}`; `Take(profiles fs.FS, prefs *jsondoc.Value, appVersion string, now time.Time) (*Snapshot, error)`; `(*Snapshot) Digest() string`; `Settle(take func() (*Snapshot, error), wait func(), maxTries int) (*Snapshot, error)`; `ErrExists`; `(*Snapshot) Save(dir string) error`; `Load(dir string) (*Snapshot, error)`; `Report{Name string; Before, After time.Time; AppVersion string; Changes []semdiff.Change}`; `Compare(name string, before, after *Snapshot, r *redact.Redactor) (Report, error)`; `(Report) Markdown() string`; `(Report) EvidenceRow() string`.

Snapshots hold unredacted data and are written with `0600`/`0700` permissions into schrodeck's state dir; reports are redacted and **pseudonymized**: every UUID in a report (profile folders, page folders, `ActionID`s, references) is replaced by a stable readable name. Profile folders become `profile-1`, `profile-2`, … in folder-name order; pages become `profile-1/page/0`, `profile-1/default` or `profile-1/sub-page-1` by their position before the change (after it, for a new page); anything else becomes `uuid-1`, `uuid-2`, … in order of first appearance. The same id always gets the same name within a report, so what an observation is for (did an id change, which page moved where) stays visible, while a committed report carries no real id. Table cells are cut at 120 characters, not bytes, so a cut never splits a multi-byte character. The draft evidence row has the column layout of `docs/references.md` (`| id | Topic | Status | Source | What it says / what we saw |`); a person fills in the id and topic and rewrites the summary as a claim.

- [ ] **Step 1: Write the failing tests**

`deckformat/observe/observe_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package observe

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/redact"
	"github.com/csmarshall/schrodeck/deckformat/semdiff"
)

var (
	t0         = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	realSerial = "AB12" + "CD34EF"
	realDevice = "@(1)[4057/143/" + realSerial + "]"
)

func prefs(t *testing.T, preferred string) *jsondoc.Value {
	t.Helper()
	v, err := jsondoc.FromAny(map[string]any{"Devices": map[string]any{
		realDevice: map[string]any{"ESDProfilesInfo": map[string]any{"ESDProfilesPreferred": preferred}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func take(t *testing.T, fp fixture.Profile, pr *jsondoc.Value, at time.Time) *Snapshot {
	t.Helper()
	s, err := Take(fp.FS(), pr, "7.5.1", at)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func redactor(t *testing.T) *redact.Redactor {
	t.Helper()
	r, err := redact.New(redact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := take(t, fixture.XL(), prefs(t, "x"), t0)
	dir := filepath.Join(t.TempDir(), "before")
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	back, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if back.Digest() != s.Digest() || !back.TakenAt.Equal(t0) || back.AppVersion != "7.5.1" {
		t.Fatalf("round trip changed the snapshot")
	}
	if err := s.Save(dir); !errors.Is(err, ErrExists) {
		t.Fatalf("second Save into the same dir: %v", err)
	}
}

func TestSettle(t *testing.T) {
	a := take(t, fixture.XL(), nil, t0)
	edited := fixture.XL()
	edited.Name = "changed"
	b := take(t, edited, nil, t0)
	seq := []*Snapshot{a, b, b}
	i := 0
	next := func() (*Snapshot, error) { s := seq[i]; i++; return s, nil }
	got, err := Settle(next, func() {}, 5)
	if err != nil || got.Digest() != b.Digest() {
		t.Fatalf("Settle = %v, %v", got, err)
	}
	i = 0
	alternating := []*Snapshot{a, b, a, b}
	next = func() (*Snapshot, error) { s := alternating[i]; i++; return s, nil }
	if _, err := Settle(next, func() {}, 4); err == nil {
		t.Fatal("Settle accepted files that never stopped changing")
	}
}

func TestCompareReportsAndRedacts(t *testing.T) {
	before := take(t, fixture.XL(), prefs(t, "aaaa"), t0)
	edited := fixture.XL()
	edited.Pages[0].Buttons[1].Title = "Paste"
	after := take(t, edited, prefs(t, "bbbb"), t0.Add(time.Minute))
	rep, err := Compare("u0-title", before, after, redactor(t))
	if err != nil {
		t.Fatal(err)
	}
	md := rep.Markdown()
	for _, want := range []string{
		"# Observation: u0-title",
		"page 1 (profile-1/page/0) › key 1,0",
		`"Paste"`,
		"@(1)[4057/143/<deck>]",
		"| R?? | <topic> | **Observed** | `schrodeck observe u0-title`, app 7.5.1, 2026-10-02 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, realSerial) {
		t.Fatalf("report leaks the serial:\n%s", md)
	}
}

func TestReportsCarryNoRealIDs(t *testing.T) {
	before := take(t, fixture.XL(), nil, t0)
	edited := fixture.XL()
	edited.Pages[0].Buttons[1].Title = "Paste"
	edited.Pages[0].Buttons[1].ActionID = "11111111-2222-4333-8444-555555555555"
	after := take(t, edited, nil, t0.Add(time.Minute))
	rep, err := Compare("ids", before, after, redactor(t))
	if err != nil {
		t.Fatal(err)
	}
	md := rep.Markdown()
	if uuidPattern.MatchString(md) {
		t.Fatalf("report contains a raw UUID:\n%s", md)
	}
	// Equalities survive: the changed ActionID became uuid-N on both sides of
	// one row, and the page is named by its position.
	if !strings.Contains(md, "uuid-") || !strings.Contains(md, "profile-1/page/0") {
		t.Fatalf("pseudonyms missing:\n%s", md)
	}
	// Known-bad control: the same comparison without pseudonyms carries UUIDs,
	// so the check above can fail.
	raw, err := semdiff.Sets(before.Profiles, after.Profiles, semdiff.Raw)
	if err != nil {
		t.Fatal(err)
	}
	var rawText strings.Builder
	for _, c := range raw {
		rawText.WriteString(c.String() + "\n")
	}
	if !uuidPattern.MatchString(rawText.String()) {
		t.Fatal("control: the raw diff has no UUID, so this test proves nothing")
	}
}

func TestPseudonymsAreStableAndDistinct(t *testing.T) {
	ps := newPseudonyms()
	a := ps.String("x AAAAAAAA-0000-4000-8000-000000000001 y aaaaaaaa-0000-4000-8000-000000000001")
	if a != "x uuid-1 y uuid-1" {
		t.Fatalf("same id in two letter cases: %q", a)
	}
	if b := ps.String("BBBBBBBB-0000-4000-8000-000000000002"); b != "uuid-2" {
		t.Fatalf("second id: %q", b)
	}
}

func TestCellCutsCharactersNotBytes(t *testing.T) {
	long := strings.Repeat("é", 200) // two bytes each
	got := cell(long)
	if !utf8.ValidString(got) {
		t.Fatal("cell split a multi-byte character")
	}
	if n := utf8.RuneCountInString(got); n != cellRunes {
		t.Fatalf("cell kept %d characters, want %d", n, cellRunes)
	}
	// Known-bad control: a byte cut at the old position splits the character.
	if utf8.ValidString(long[:117]) {
		t.Fatal("control: a byte cut at 117 should split a two-byte character")
	}
}

func TestNoChangeIsReportedAsSuch(t *testing.T) {
	s := take(t, fixture.XL(), nil, t0)
	rep, err := Compare("nothing", s, s, redactor(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Changes) != 0 || !strings.Contains(rep.EvidenceRow(), "no change on disk") {
		t.Fatalf("got %v / %s", rep.Changes, rep.EvidenceRow())
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd deckformat && go test ./observe/`
Expected: FAIL, `undefined: Take`.

- [ ] **Step 3: Implement**

`deckformat/observe/observe.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package observe is the observation harness of ADR 0031: snapshot the app's
// profiles (and optionally its preferences), let a person do one thing in the
// app, snapshot again, and produce a redacted report plus a draft evidence row
// for docs/references.md. It is how unknowns about the format become
// documented observations. It only reads the app's data; snapshots are
// written to a directory the caller chooses (schrodeck's own state dir).
package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/normhash"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/deckformat/redact"
	"github.com/csmarshall/schrodeck/deckformat/semdiff"
)

// Snapshot is the app's profile data at one moment.
type Snapshot struct {
	TakenAt    time.Time
	AppVersion string
	Profiles   map[string]*profile.Profile // by folder name
	LoadErrors []string
	Prefs      *jsondoc.Value // may be nil
}

type meta struct {
	TakenAt     time.Time `json:"taken_at"`
	AppVersion  string    `json:"app_version"`
	NormVersion int       `json:"norm_version"`
	LoadErrors  []string  `json:"load_errors,omitempty"`
}

// Take reads every profile in profiles (a ProfilesV3-shaped fs.FS).
func Take(profiles fs.FS, prefs *jsondoc.Value, appVersion string, now time.Time) (*Snapshot, error) {
	res, err := profile.LoadAll(profiles)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{TakenAt: now.UTC(), AppVersion: appVersion, Profiles: map[string]*profile.Profile{}, Prefs: prefs}
	for _, p := range res.Profiles {
		s.Profiles[p.Folder] = p
	}
	for _, e := range res.Errors {
		s.LoadErrors = append(s.LoadErrors, e.Error())
	}
	return s, nil
}

// Digest identifies the snapshot's content (not its time).
func (s *Snapshot) Digest() string {
	var lines []string
	for folder, p := range s.Profiles {
		for rel, data := range p.Files() {
			sum := sha256.Sum256(data)
			lines = append(lines, folder+"/"+rel+"\x00"+hex.EncodeToString(sum[:]))
		}
	}
	if s.Prefs != nil {
		sum := sha256.Sum256(s.Prefs.Encode())
		lines = append(lines, "prefs\x00"+hex.EncodeToString(sum[:]))
	}
	lines = append(lines, s.LoadErrors...)
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

// Settle takes snapshots until two consecutive ones are identical, waiting
// between them, so a snapshot is not taken while the app is mid-write. It
// gives up after maxTries snapshots.
func Settle(take func() (*Snapshot, error), wait func(), maxTries int) (*Snapshot, error) {
	prev, err := take()
	if err != nil {
		return nil, err
	}
	for i := 1; i < maxTries; i++ {
		wait()
		cur, err := take()
		if err != nil {
			return nil, err
		}
		if cur.Digest() == prev.Digest() {
			return cur, nil
		}
		prev = cur
	}
	return nil, fmt.Errorf("observe: the app's files kept changing across %d snapshots", maxTries)
}

// ErrExists means a snapshot directory is already in use.
var ErrExists = errors.New("observe: snapshot directory already exists")

// Save writes the snapshot to dir, which must not exist yet.
func (s *Snapshot) Save(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return ErrExists
	}
	for folder, p := range s.Profiles {
		for rel, data := range p.Files() {
			target := filepath.Join(dir, "profiles", folder, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(target, data, 0o600); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o700); err != nil {
		return err
	}
	if s.Prefs != nil {
		if err := os.WriteFile(filepath.Join(dir, "prefs.json"), s.Prefs.Encode(), 0o600); err != nil {
			return err
		}
	}
	m, err := json.Marshal(meta{TakenAt: s.TakenAt, AppVersion: s.AppVersion, NormVersion: normhash.NormVersion, LoadErrors: s.LoadErrors})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), m, 0o600)
}

// Load reads a snapshot written by Save.
func Load(dir string) (*Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	var prefs *jsondoc.Value
	if pb, err := os.ReadFile(filepath.Join(dir, "prefs.json")); err == nil {
		if prefs, err = jsondoc.Parse(pb); err != nil {
			return nil, fmt.Errorf("prefs.json: %w", err)
		}
	}
	s, err := Take(os.DirFS(filepath.Join(dir, "profiles")), prefs, m.AppVersion, m.TakenAt)
	if err != nil {
		return nil, err
	}
	s.LoadErrors = append(s.LoadErrors, m.LoadErrors...)
	return s, nil
}

// Report is the outcome of one observation.
type Report struct {
	Name       string           `json:"name"`
	Before     time.Time        `json:"before"`
	After      time.Time        `json:"after"`
	AppVersion string           `json:"app_version"`
	Changes    []semdiff.Change `json:"changes"`
}

// Compare diffs two snapshots (raw mode: runtime fields and ids included,
// since that is what an observation is for) and redacts the result.
func Compare(name string, before, after *Snapshot, r *redact.Redactor) (Report, error) {
	changes, err := semdiff.Sets(before.Profiles, after.Profiles, semdiff.Raw)
	if err != nil {
		return Report{}, err
	}
	if before.Prefs != nil || after.Prefs != nil {
		changes = append(changes, semdiff.Values("app preferences", "prefs", before.Prefs, after.Prefs)...)
	}
	ps := newPseudonyms(before, after)
	for i := range changes {
		c := &changes[i]
		c.Profile, c.Where, c.Path = r.String(c.Profile), r.String(c.Where), r.String(c.Path)
		c.Before, c.After = redactJSON(r, c.Before), redactJSON(r, c.After)
		c.Profile, c.Where, c.Path = ps.String(c.Profile), ps.String(c.Where), ps.String(c.Path)
		c.Before, c.After = ps.String(c.Before), ps.String(c.After)
	}
	return Report{Name: name, Before: before.TakenAt, After: after.TakenAt, AppVersion: after.AppVersion, Changes: changes}, nil
}

// uuidPattern matches a UUID in any letter case.
var uuidPattern = regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)

// pseudonyms replaces every UUID in a report with a stable, readable name, so
// a committed report carries no real profile, page or action ids while every
// equality between ids stays visible (the same id always gets the same name).
// Profile folders become "profile-1", "profile-2", … in folder-name order;
// pages become "profile-1/page/0", "profile-1/default" or
// "profile-1/sub-page-1" by their position before the change (after it, for a
// page that is new); any other UUID (an ActionID, an unknown reference)
// becomes "uuid-1", "uuid-2", … in order of first appearance.
type pseudonyms struct {
	names map[string]string // lower-case UUID → pseudonym
	next  int
}

func newPseudonyms(snaps ...*Snapshot) *pseudonyms {
	ps := &pseudonyms{names: map[string]string{}}
	folders := map[string]*profile.Profile{}
	for _, s := range snaps {
		for folder, p := range s.Profiles {
			if _, seen := folders[folder]; !seen {
				folders[folder] = p
			}
		}
	}
	var sorted []string
	for f := range folders {
		sorted = append(sorted, f)
	}
	sort.Strings(sorted)
	for i, folder := range sorted {
		prof := fmt.Sprintf("profile-%d", i+1)
		ps.names[strings.ToLower(strings.TrimSuffix(folder, profile.Suffix))] = prof
		subPages := 0
		for _, s := range snaps {
			p := s.Profiles[folder]
			if p == nil {
				continue
			}
			labels, err := normhash.PageLabels(p)
			if err != nil {
				continue // a malformed profile: its page ids fall back to uuid-N
			}
			var ids []string
			for id := range labels {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				if _, named := ps.names[id]; named {
					continue
				}
				label := labels[id]
				if strings.HasPrefix(label, "other/") {
					subPages++
					label = fmt.Sprintf("sub-page-%d", subPages)
				}
				ps.names[id] = prof + "/" + label
			}
		}
	}
	return ps
}

// String replaces every UUID in s.
func (ps *pseudonyms) String(s string) string {
	return uuidPattern.ReplaceAllStringFunc(s, func(u string) string {
		key := strings.ToLower(u)
		if name, ok := ps.names[key]; ok {
			return name
		}
		ps.next++
		name := fmt.Sprintf("uuid-%d", ps.next)
		ps.names[key] = name
		return name
	})
}

// redactJSON redacts a JSON fragment structurally when it parses (so secret
// member names are honored), else as text.
func redactJSON(r *redact.Redactor, s string) string {
	if s == "" {
		return s
	}
	if v, err := jsondoc.Parse([]byte(s)); err == nil {
		return string(r.Value(v).Encode())
	}
	return r.String(s)
}

// escape makes s safe inside a Markdown table cell.
func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

// cellRunes is the longest table cell, in characters (not bytes, so a cut
// never splits a multi-byte character).
const cellRunes = 120

// cell escapes and shortens long JSON fragments for the change table.
func cell(s string) string {
	s = escape(s)
	if r := []rune(s); len(r) > cellRunes {
		s = string(r[:cellRunes-3]) + "..."
	}
	return s
}

// Markdown renders the report for docs/observations/<name>.md.
func (rep Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Observation: %s\n\n", rep.Name)
	fmt.Fprintf(&b, "- Before: %s · After: %s · App: %s · norm_version: %d\n", rep.Before.Format(time.RFC3339), rep.After.Format(time.RFC3339), rep.AppVersion, normhash.NormVersion)
	fmt.Fprintf(&b, "- Changes (raw, redacted): %d\n\n", len(rep.Changes))
	if len(rep.Changes) > 0 {
		b.WriteString("| Profile | Where | Path | Change | Before | After |\n|---|---|---|---|---|---|\n")
		for _, c := range rep.Changes {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", cell(c.Profile), cell(c.Where), cell(c.Path), c.Kind, cell(c.Before), cell(c.After))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Draft evidence row for docs/references.md\n\n")
	b.WriteString(rep.EvidenceRow() + "\n")
	return b.String()
}

// EvidenceRow is a draft row for docs/references.md. A person fills in the id
// and topic and rewrites the summary as a claim.
func (rep Report) EvidenceRow() string {
	var summary []string
	for i, c := range rep.Changes {
		if i == 3 {
			summary = append(summary, fmt.Sprintf("and %d more", len(rep.Changes)-3))
			break
		}
		summary = append(summary, c.String())
	}
	if len(summary) == 0 {
		summary = []string{"no change on disk"}
	}
	return fmt.Sprintf("| R?? | <topic> | **Observed** | `schrodeck observe %s`, app %s, %s | %s. Full report: [observations/%s.md](observations/%s.md) |",
		rep.Name, rep.AppVersion, rep.After.Format("2006-01-02"), escape(strings.Join(summary, "; ")), rep.Name, rep.Name)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd deckformat && go test ./observe/`
Expected: `ok`. `TestReportsCarryNoRealIDs` and `TestCellCutsCharactersNotBytes` each carry a known-bad control (the raw diff does contain UUIDs; a byte cut does split a character), so neither can pass vacuously.

- [ ] **Step 5: Commit**

```bash
git add deckformat/observe
git commit -m "feat(#<n>): observation harness: settle, snapshot, compare, redacted report and evidence row"
```

---

### Task 8: The probe runner and contract C's read-only probes

**Files:**
- Create: `deckformat/probe/probe.go`, `deckformat/probe/contractc.go`, `deckformat/probe/probe_test.go`

**Interfaces:**
- Consumes: `profile`, `normhash`, `fixture`, `jsondoc`.
- Produces (package `probe`): `Tier` (`ReadOnly`, `Restart`; `String()` = `read-only` / `restart`); `Status` (`Pass`, `Fail`, `Skip`, `Info`); `Result{ID, Contract, Tier string; Status Status; Detail string; Evidence []string}` (JSON `id, contract, tier, status, detail, evidence`); `Probe{ID, Contract string; Tier Tier; Run func(ctx context.Context) (Status, string, []string)}`; `RunAll(ctx, []Probe, max Tier) []Result`; `Failed([]Result) bool`; `Install{Load profile.LoadResult; Home string}`; `OpenAction`; `ContractC(Install) []Probe` with ids `P1, P2, P3, P6, P7, P8, P9+P11`.

P6 and P8 report `info`, not `fail`: they describe what M2/M3 will have to handle (variables, DETACHED(foreign-device-id)), not a broken install. `P9+P11` checks this build's hasher against contract C's own claim on the synthetic fixture pair. P4 and P10 are restart-tier and arrive with M3.

- [ ] **Step 1: Write the failing tests**

`deckformat/probe/probe_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package probe

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

func install(t *testing.T, home string, fss ...fstest.MapFS) Install {
	t.Helper()
	res, err := profile.LoadAll(fixture.Merge(fss...))
	if err != nil {
		t.Fatal(err)
	}
	return Install{Load: res, Home: home}
}

func byID(rs []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range rs {
		m[r.ID] = r
	}
	return m
}

func run(in Install) map[string]Result {
	return byID(RunAll(context.Background(), ContractC(in), ReadOnly))
}

func TestHealthyInstallPasses(t *testing.T) {
	rs := run(install(t, "/Users/<user>", fixture.XL().FS()))
	for _, id := range []string{"P1", "P2", "P3", "P6", "P7", "P8", "P9+P11"} {
		if rs[id].Status != Pass {
			t.Errorf("%s = %s (%s) %v", id, rs[id].Status, rs[id].Detail, rs[id].Evidence)
		}
	}
}

func TestEmptyInstallSkipsP1(t *testing.T) {
	if r := run(install(t, "/Users/<user>"))["P1"]; r.Status != Skip {
		t.Fatalf("P1 on no profiles = %s", r.Status)
	}
}

func TestEachProbeFailsOnItsKnownBad(t *testing.T) {
	v4 := fixture.XL()
	v4fs := v4.FS()
	key := v4.Folder() + "/manifest.json"
	v4fs[key] = &fstest.MapFile{Data: []byte(strings.Replace(string(v4fs[key].Data), `"Version":"3.0"`, `"Version":"4.0"`, 1))}

	stray := fixture.CopyOf(fixture.XL(), "stray")
	stray.Extra = map[string][]byte{"cache.db": []byte("x")}

	noDevice := fixture.CopyOf(fixture.XL(), "nodev")
	noDevice.Device = ""

	broken := fixture.CopyOf(fixture.XL(), "broken")
	bfs := broken.FS()
	bfs[broken.Folder()+"/manifest.json"] = &fstest.MapFile{Data: []byte("{\"Name\": 1}")}

	cases := []struct {
		id  string
		fs  fstest.MapFS
		bad Status
	}{
		{"P2", v4fs, Fail},
		{"P7", stray.FS(), Fail},
		{"P3", noDevice.FS(), Fail},
		{"P1", bfs, Fail},
	}
	for _, c := range cases {
		if r := run(install(t, "/Users/<user>", c.fs))[c.id]; r.Status != c.bad {
			t.Errorf("%s on its known-bad input = %s (%s)", c.id, r.Status, r.Detail)
		}
	}
}

func TestP6ReportsPathsOutsideHome(t *testing.T) {
	r := run(install(t, "/Users/<other>", fixture.XL().FS()))["P6"]
	if r.Status != Info || len(r.Evidence) != 1 || !strings.Contains(r.Evidence[0], "/Users/<user>/bin/demo.sh") {
		t.Fatalf("P6 = %s %v", r.Status, r.Evidence)
	}
}

func TestP8FindsReferences(t *testing.T) {
	target := fixture.CopyOf(fixture.XL(), "target")
	src := fixture.XL()
	src.Pages[0].Buttons[1].Settings = `{"profile":"` + target.ID + `","device":"@(1)[4057/99/<other-deck>]"}`
	r := run(install(t, "/Users/<user>", src.FS(), target.FS()))["P8"]
	if r.Status != Info || len(r.Evidence) != 2 {
		t.Fatalf("P8 = %s %v", r.Status, r.Evidence)
	}
	joined := strings.Join(r.Evidence, "\n")
	if !strings.Contains(joined, "references profile "+target.Folder()) || !strings.Contains(joined, "not this profile's own") {
		t.Fatalf("P8 evidence = %s", joined)
	}
}

func TestRunAllSkipsHigherTiers(t *testing.T) {
	ran := false
	ps := []Probe{{ID: "X", Contract: "C", Tier: Restart, Run: func(context.Context) (Status, string, []string) {
		ran = true
		return Pass, "", nil
	}}}
	rs := RunAll(context.Background(), ps, ReadOnly)
	if ran || rs[0].Status != Skip || rs[0].Tier != "restart" {
		t.Fatalf("restart-tier probe ran or was not skipped: %+v", rs[0])
	}
	if Failed(rs) {
		t.Fatal("a skip is not a failure")
	}
	if !Failed([]Result{{Status: Fail}}) {
		t.Fatal("Failed missed a failure")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd deckformat && go test ./probe/`
Expected: FAIL, `undefined: RunAll`.

- [ ] **Step 3: Implement the runner**

`deckformat/probe/probe.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package probe runs the assertions of contracts B and C against a real
// install (ADR 0031's probe runner; `schrodeck doctor` is built on it). Each
// probe has a tier: read-only probes never write anything; restart-tier
// probes restart the app and arrive with M3.
package probe

import "context"

// Tier says what a probe may do.
type Tier int

// Tiers.
const (
	ReadOnly Tier = iota // reads files and prefs only
	Restart              // quits and relaunches the app (M3)
)

func (t Tier) String() string {
	if t == Restart {
		return "restart"
	}
	return "read-only"
}

// Status of one probe run.
type Status string

// Statuses. Info means "nothing is wrong, but here is something to know"
// (e.g. which buttons reference another profile).
const (
	Pass Status = "pass"
	Fail Status = "fail"
	Skip Status = "skip"
	Info Status = "info"
)

// Result is one probe's outcome.
type Result struct {
	ID       string   `json:"id"`
	Contract string   `json:"contract"`
	Tier     string   `json:"tier"`
	Status   Status   `json:"status"`
	Detail   string   `json:"detail,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

// Probe is one assertion check. Run returns status, a one-line detail and
// optional evidence lines.
type Probe struct {
	ID       string
	Contract string // "B" or "C"
	Tier     Tier
	Run      func(ctx context.Context) (Status, string, []string)
}

// RunAll runs every probe whose tier is at most max, in order; the others are
// reported as skipped.
func RunAll(ctx context.Context, probes []Probe, max Tier) []Result {
	out := make([]Result, 0, len(probes))
	for _, p := range probes {
		r := Result{ID: p.ID, Contract: p.Contract, Tier: p.Tier.String()}
		if p.Tier > max {
			r.Status, r.Detail = Skip, "needs the "+p.Tier.String()+" tier"
		} else {
			r.Status, r.Detail, r.Evidence = p.Run(ctx)
		}
		out = append(out, r)
	}
	return out
}

// Failed reports whether any result failed.
func Failed(rs []Result) bool {
	for _, r := range rs {
		if r.Status == Fail {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Implement contract C's probes**

`deckformat/probe/contractc.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package probe

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/normhash"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// Install is what the contract C probes look at.
type Install struct {
	Load profile.LoadResult
	Home string // value of {{HOME}} on this host (P6)
}

// OpenAction is the built-in Open action's UUID (P6).
const OpenAction = "com.elgato.streamdeck.system.open"

var uuidShape = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// ContractC returns the read-only probes of contract C.
func ContractC(in Install) []Probe {
	c := func(id string, run func() (Status, string, []string)) Probe {
		return Probe{ID: id, Contract: "C", Tier: ReadOnly, Run: func(context.Context) (Status, string, []string) { return run() }}
	}
	return []Probe{
		c("P1", func() (Status, string, []string) { return p1(in) }),
		c("P2", func() (Status, string, []string) { return p2(in) }),
		c("P3", func() (Status, string, []string) { return p3(in) }),
		c("P6", func() (Status, string, []string) { return p6(in) }),
		c("P7", func() (Status, string, []string) { return p7(in) }),
		c("P8", func() (Status, string, []string) { return p8(in) }),
		c("P9+P11", p9p11),
	}
}

func p1(in Install) (Status, string, []string) {
	var ev []string
	for _, err := range in.Load.Errors {
		var ue *profile.UnexpectedFileError
		if !errors.As(err, &ue) {
			ev = append(ev, err.Error())
		}
	}
	if len(ev) > 0 {
		return Fail, fmt.Sprintf("%d profile(s) do not have the expected layout", len(ev)), ev
	}
	if len(in.Load.Profiles) == 0 && len(in.Load.Errors) == 0 {
		return Skip, "no profiles found", nil
	}
	return Pass, fmt.Sprintf("%d profile(s) load", len(in.Load.Profiles)), nil
}

func p2(in Install) (Status, string, []string) {
	var ev []string
	for _, p := range in.Load.Profiles {
		if p.Version() != profile.KnownVersion {
			ev = append(ev, fmt.Sprintf("%s: Version %q", p.Folder, p.Version()))
		}
	}
	if len(ev) > 0 {
		return Fail, "profile Version differs from " + profile.KnownVersion, ev
	}
	return Pass, "every profile has Version " + profile.KnownVersion, nil
}

func p3(in Install) (Status, string, []string) {
	var ev []string
	for _, p := range in.Load.Profiles {
		if p.DeviceUUID() == "" || p.DeviceModel() == "" {
			ev = append(ev, p.Folder+": Device.UUID or Device.Model missing")
		}
	}
	if len(ev) > 0 {
		return Fail, "profiles not bound to a device", ev
	}
	return Pass, "every profile names its device", nil
}

func p7(in Install) (Status, string, []string) {
	var ev []string
	for _, err := range in.Load.Errors {
		var ue *profile.UnexpectedFileError
		if errors.As(err, &ue) {
			ev = append(ev, ue.Error())
		}
	}
	if len(ev) > 0 {
		return Fail, "files outside the allow-list (the format may have changed)", ev
	}
	return Pass, "every file is allow-listed", nil
}

// forEachSetting calls fn for every string inside every action's Settings.
func forEachSetting(p *profile.Profile, fn func(where, value string)) {
	for _, key := range p.SortedPageKeys() {
		pg := p.Pages[key]
		for _, c := range pg.Manifest.Get("Controllers").Items() {
			for _, m := range c.Get("Actions").Members() {
				where := fmt.Sprintf("%s › page %s › key %s", p.Folder, pg.Folder, m.Name)
				m.Value.Get("Settings").Walk(func(path []string, v *jsondoc.Value) {
					if s, ok := v.Str(); ok {
						fn(where+" › Settings."+strings.Join(path, "."), s)
					}
				})
			}
		}
	}
}

func p6(in Install) (Status, string, []string) {
	var ev []string
	home := filepath.Clean(in.Home)
	for _, p := range in.Load.Profiles {
		for _, key := range p.SortedPageKeys() {
			for _, c := range p.Pages[key].Manifest.Get("Controllers").Items() {
				for _, m := range c.Get("Actions").Members() {
					if uuid, _ := m.Value.Get("UUID").Str(); uuid != OpenAction {
						continue
					}
					path, ok := m.Value.Lookup("Settings", "path").Str()
					if !ok || !filepath.IsAbs(path) {
						continue
					}
					if home == "." || (path != home && !strings.HasPrefix(path, home+string(filepath.Separator))) {
						ev = append(ev, fmt.Sprintf("%s › key %s: %s", p.Folder, m.Name, path))
					}
				}
			}
		}
	}
	if len(ev) > 0 {
		return Info, fmt.Sprintf("%d Open action(s) use an absolute path outside {{HOME}}", len(ev)), ev
	}
	return Pass, "every absolute Open path is under {{HOME}}", nil
}

func p8(in Install) (Status, string, []string) {
	folders := map[string]string{}
	for _, p := range in.Load.Profiles {
		folders[strings.ToLower(strings.TrimSuffix(p.Folder, profile.Suffix))] = p.Folder
	}
	var ev []string
	for _, p := range in.Load.Profiles {
		own := p.DeviceUUID()
		self := strings.ToLower(strings.TrimSuffix(p.Folder, profile.Suffix))
		forEachSetting(p, func(where, s string) {
			if uuidShape.MatchString(s) {
				if target, ok := folders[strings.ToLower(s)]; ok && strings.ToLower(s) != self {
					ev = append(ev, where+": references profile "+target)
				}
			}
			if strings.Contains(s, "@(") && !strings.Contains(s, own) {
				ev = append(ev, where+": embeds a device id that is not this profile's own")
			}
		})
	}
	sort.Strings(ev)
	if len(ev) > 0 {
		return Info, fmt.Sprintf("%d cross-profile or foreign-device reference(s)", len(ev)), ev
	}
	return Pass, "no cross-profile or foreign-device references", nil
}

// p9p11 checks this build's hasher against contract C's own claim: a copy
// (new ActionIDs, page folders and image names) hashes equal to its original.
func p9p11() (Status, string, []string) {
	a, b := fixture.XL(), fixture.CopyOf(fixture.XL(), "doctor")
	pa, err := profile.Load(a.FS(), a.Folder())
	if err != nil {
		return Fail, err.Error(), nil
	}
	pb, err := profile.Load(b.FS(), b.Folder())
	if err != nil {
		return Fail, err.Error(), nil
	}
	ha, err := normhash.Hash(pa)
	if err != nil {
		return Fail, err.Error(), nil
	}
	hb, err := normhash.Hash(pb)
	if err != nil {
		return Fail, err.Error(), nil
	}
	if ha != hb {
		return Fail, "a copy of the reference fixture hashes differently", nil
	}
	return Pass, fmt.Sprintf("copies hash equal under norm_version %d", normhash.NormVersion), nil
}
```

- [ ] **Step 5: Run every deckformat check**

Run: `cd deckformat && go mod tidy && go vet ./... && go test -race ./... && GOWORK=off go test ./... && cd .. && go run ./tools/archcheck/cmd/archcheck -mode boundary -dir deckformat -forbid .`
Expected: every package `ok`; `archcheck boundary: ok`.

- [ ] **Step 6: Commit, push, PR, merge** (as in Task 3 step 6)

```bash
git add deckformat/probe
git commit -m "feat(#<n>): probe runner and contract C read-only probes"
```

---

### Task 9: Identity: host_id, NAMESPACE_SCHRODECK, copy_id, canonical folder

Issue: "schrodeck: identity, read-only macOS connector, inventory/status/doctor/observe/fixture" (Tasks 9 to 11; new worktree `<n>-m1-cli`).

**Files:**
- Create: `internal/identity/identity.go`, `internal/identity/identity_test.go`
- Modify: `docs/contracts/store-format.md` (define the namespace and the folder's letter case), `docs/adr/0026-profile-identity.md` (link to that definition)

**Interfaces:**
- Consumes: `ports.HostIdentity`, `fake.HostIdentity` (tests).
- Produces (package `github.com/csmarshall/schrodeck/internal/identity`): `NamespaceURLName = "https://github.com/csmarshall/schrodeck/ns/v1"`; `NamespaceSchrodeck [16]byte`; `UUID5(ns [16]byte, name string) [16]byte`; `String([16]byte) string`; `HostIDLength = 12`; `HostID(ports.HostIdentity) (string, error)`; `CopyID(hostID, profileID, deckKey string) string`; `CanonicalFolder(profileID, deckKey string) string` (upper-case + `.sdProfile`).

NAMESPACE_SCHRODECK was named in ADR 0026 and contract D but never given a value. It is derived here, from a URL, instead of being a random constant: `uuid5(NameSpace_URL, "https://github.com/csmarshall/schrodeck/ns/v1")` = `5a7d742c-c29c-52c8-b996-ed8ccdcb83f8`. The golden values in the test were computed with Python's `uuid` and `hashlib`, independently of this Go code.

- [ ] **Step 1: Write the failing tests**

`internal/identity/identity_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package identity

import (
	"testing"

	"github.com/csmarshall/schrodeck/internal/ports/fake"
)

// Golden values were computed independently with Python's uuid and hashlib
// modules (not with this package):
//
//	ns = uuid.uuid5(uuid.NAMESPACE_URL, "https://github.com/csmarshall/schrodeck/ns/v1")
//	uuid.uuid5(ns, "0123456789ab:" + PROFILE + ":" + DECK)
//	uuid.uuid5(ns, PROFILE + ":" + DECK)
//	hashlib.sha256(b"HW-TEST-0001:alice").hexdigest()[:12]
const (
	profileID = "11111111-1111-4111-8111-111111111111"
	deckKey   = "@(1)[4057/143/<deck>]"
)

func TestNamespaceIsDerivedFromItsURL(t *testing.T) {
	if got := String(NamespaceSchrodeck); got != "5a7d742c-c29c-52c8-b996-ed8ccdcb83f8" {
		t.Fatalf("NAMESPACE_SCHRODECK = %s", got)
	}
}

func TestCopyID(t *testing.T) {
	if got := CopyID("0123456789ab", profileID, deckKey); got != "f0686204-f3d4-53e0-bf6b-f16dd1fd2232" {
		t.Fatalf("CopyID = %s", got)
	}
	// Each part of the key matters (setup, Mac, deck).
	base := CopyID("0123456789ab", profileID, deckKey)
	for _, other := range []string{
		CopyID("0123456789ac", profileID, deckKey),
		CopyID("0123456789ab", "21111111-1111-4111-8111-111111111111", deckKey),
		CopyID("0123456789ab", profileID, "@(1)[4057/99/<deck>]"),
	} {
		if other == base {
			t.Fatal("changing one part of (host, profile, deck) kept the copy_id")
		}
	}
}

func TestCanonicalFolder(t *testing.T) {
	if got := CanonicalFolder(profileID, deckKey); got != "BA653531-BDCB-5CA9-907A-EB0FF57A17DF.sdProfile" {
		t.Fatalf("CanonicalFolder = %s", got)
	}
}

func TestHostID(t *testing.T) {
	got, err := HostID(fake.HostIdentity{Hardware: "HW-TEST-0001", User: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "7db4c9ba0faa" {
		t.Fatalf("HostID = %s", got)
	}
	other, _ := HostID(fake.HostIdentity{Hardware: "HW-TEST-0001", User: "bob"})
	if other == got {
		t.Fatal("two users on one Mac must be two hosts (ADR 0010)")
	}
	if _, err := HostID(fake.HostIdentity{User: "alice"}); err == nil {
		t.Fatal("missing hardware id accepted")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/identity/`
Expected: FAIL, `undefined: NamespaceSchrodeck`.

- [ ] **Step 3: Implement**

`internal/identity/identity.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package identity derives schrodeck's identifiers: host_id (ADR 0010) and a
// member copy's copy_id and canonical folder (ADR 0026, contract D). Every
// value here is derived, never configured or stored as a magic constant.
package identity

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/csmarshall/schrodeck/internal/ports"
)

// namespaceURL is RFC 9562's predefined NameSpace_URL.
var namespaceURL = [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

// NamespaceURLName is the URL from which NAMESPACE_SCHRODECK is derived
// (contract D § identifiers). Changing it changes every copy_id and canonical
// folder, so it is versioned in its last path element.
const NamespaceURLName = "https://github.com/csmarshall/schrodeck/ns/v1"

// NamespaceSchrodeck is NAMESPACE_SCHRODECK = uuid5(NameSpace_URL, NamespaceURLName).
var NamespaceSchrodeck = UUID5(namespaceURL, NamespaceURLName)

// UUID5 returns the RFC 9562 name-based (SHA-1) UUID of name in namespace ns.
func UUID5(ns [16]byte, name string) [16]byte {
	h := sha1.New()
	h.Write(ns[:])
	h.Write([]byte(name))
	var u [16]byte
	copy(u[:], h.Sum(nil))
	u[6] = (u[6] & 0x0f) | 0x50 // version 5
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 9562 variant
	return u
}

// String renders a UUID in the canonical lower-case 8-4-4-4-12 form.
func String(u [16]byte) string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// HostIDLength is the number of hex characters of host_id (ADR 0010).
const HostIDLength = 12

// HostID derives host_id = sha256(HardwareID + ":" + UserName)[:12]. The
// hardware id itself is never returned, logged or stored.
func HostID(h ports.HostIdentity) (string, error) {
	hw, err := h.HardwareID()
	if err != nil {
		return "", fmt.Errorf("host identity: %w", err)
	}
	user := h.UserName()
	if hw == "" || user == "" {
		return "", fmt.Errorf("host identity: hardware id or user name is empty")
	}
	sum := sha256.Sum256([]byte(hw + ":" + user))
	return hex.EncodeToString(sum[:])[:HostIDLength], nil
}

// CopyID names one member copy in the store:
// uuid5(NAMESPACE_SCHRODECK, host_id + ":" + profile_id + ":" + deck_key).
func CopyID(hostID, profileID, deckKey string) string {
	return String(UUID5(NamespaceSchrodeck, hostID+":"+profileID+":"+deckKey))
}

// CanonicalFolder is the member copy's folder in ProfilesV3:
// uuid5(NAMESPACE_SCHRODECK, profile_id + ":" + deck_key), upper-case as the
// app names its folders, plus ".sdProfile".
func CanonicalFolder(profileID, deckKey string) string {
	return strings.ToUpper(String(UUID5(NamespaceSchrodeck, profileID+":"+deckKey))) + ".sdProfile"
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/identity/`
Expected: `ok`.

- [ ] **Step 5: Define the namespace in contract D**

In `docs/contracts/store-format.md`, directly after the paragraph that starts ``` `copy_id` names one **member copy** ```, insert a new paragraph:

`` `NAMESPACE_SCHRODECK` = `uuid5(NameSpace_URL, "https://github.com/csmarshall/schrodeck/ns/v1")` = `5a7d742c-c29c-52c8-b996-ed8ccdcb83f8` (RFC 9562 name-based UUIDs). It is derived from that URL rather than chosen at random, and the URL carries a version: a different namespace would change every `copy_id` and canonical folder, so it can only change with a store `FORMAT` migration. The canonical folder name is that UUID in **upper case** plus `.sdProfile`, matching how the app names profile folders. Implemented in `internal/identity`. ``

- [ ] **Step 6: Point ADR 0026 at the definition**

ADR 0026 uses `NAMESPACE_SCHRODECK` and the canonical folder, so it links to where both are now defined. In `docs/adr/0026-profile-identity.md`, after the sentence `The name is deterministic, so a host that loses its local state can rebuild the mapping by recomputing the names.` add: `` `NAMESPACE_SCHRODECK`'s value (derived from a versioned URL) and the rule that the folder name is the UUID in upper case plus `.sdProfile` are defined in [contract D](../contracts/store-format.md) and implemented in `internal/identity`. `` and append to the end of the `Status:` paragraph: `` Revised 2026-10-02 (M1, implementation): `NAMESPACE_SCHRODECK` is derived, not random, and the canonical folder name is upper case; both are defined in contract D. ``

- [ ] **Step 7: Commit**

```bash
git add internal/identity docs/contracts/store-format.md docs/adr/0026-profile-identity.md
git commit -m "feat(#<n>): identity: host_id, derived NAMESPACE_SCHRODECK, copy_id, canonical folder"
```

---

### Task 10: Decks, the host bundle and the read-only macOS connector

**Files:**
- Create: `internal/decks/decks.go`, `internal/decks/decks_test.go`, `internal/host/host.go`, `internal/connector/macos/parse.go`, `internal/connector/macos/parse_test.go`, `internal/connector/macos/macos_darwin.go`, `internal/connector/macos/macos_darwin_test.go`, `internal/connector/connector_darwin.go`, `internal/connector/connector_other.go`
- Modify: `go.mod`, `go.sum` (require `deckformat` through a `replace`, add `howett.net/plist`), `docs/contracts/os-connector.md` (`DeviceRecords` record shape, geometry keying), `docs/adr/0003-decks-are-local-geometry-compatibility.md` (geometry keying), `docs/references.md` (R8's wording)

**Interfaces:**
- Consumes: `ports.*`, `profile.LoadAll`, `conformance.HostIdentityStable`, `identity.HostID`.
- Produces:
  - package `decks`: `RecordKey = "_key"`, `RecordRaw = "_raw"`; `TypeInfo{Name string; Keys, Dials int; Variable bool; Columns, Rows int}`; `DeviceTypes map[int]TypeInfo` (Elgato's DeviceType enumeration with the documented key and dial counts, R8; `Columns`/`Rows` zero until observed); `ProductTypes map[[2]int]int` (USB vendor/product → DeviceType, empty until Task 12 adds observed rows); `KnownGeometry() map[[2]int]ports.Geometry` (derived from the two); `Validate(types map[int]TypeInfo, products map[[2]int]int) error`; `ParseKey(key) (virtual bool, vendor, product int, serial string, ok bool)`; `Enumerate(records []map[string]any, profiles []*profile.Profile, known map[[2]int]ports.Geometry) []ports.Deck`; `Unmatched(records []map[string]any, profiles []*profile.Profile) (profilesWithoutDeck, decksWithoutProfile []string)`; `Status{ports.Deck; GeometryKnown, KeyUnique bool}` with `Destination() bool`; `Annotate([]ports.Deck) []Status`.
  - package `host`: `AppPresence interface{ Installed() (bool, error); Running() (bool, error) }`; `Host{Paths ports.Paths; Identity ports.HostIdentity; Prefs ports.AppPrefs; Decks ports.DeviceEnumerator; App AppPresence; HostNames []string; Now func() time.Time; Sleep func(time.Duration)}`.
  - package `macos`: pure helpers `ParseIOPlatformUUID`, `DropboxPaths`, `Plain`, `DeviceRecords(prefs map[string]any)`, `Preferred(prefs, id)`; on darwin `New() (*Connector, error)` implementing `ports.Paths`, `ports.HostIdentity`, `ports.AppPrefs`, `ports.DeviceEnumerator`, `host.AppPresence`, plus `HostNames() []string`.
  - package `connector`: `New() (*host.Host, error)` (darwin: the macOS connector; elsewhere an error saying there is no connector yet).

**Geometry follows ADR 0003: it comes from Elgato's DeviceType table (R8), not from counting keys by eye.** R8 documents each DeviceType's **key count and dial count** (`DeviceTypes` carries them verbatim). Two things are not on disk and not in R8, so they are observed in Task 12, each with an evidence row: which DeviceType a USB product is (`ProductTypes`; neither the prefs nor the manifests record it), and how a type's keys are arranged into columns × rows (R8 gives the count; the SDK reports a connected device's `columns` and `rows` at runtime, but the docs do not tabulate them). `Validate` ties the observed part to the documented part: a grid must multiply to the documented key count, and a mapped product's type must have a grid. Until Task 12 adds rows, every deck is reported as "geometry not verified", which is the fail-closed answer. Virtual decks stay in scope (ADR 0003): their grid is user-chosen and where the app stores it is unknown (U8), so they are "geometry not verified" until U8 is settled, not excluded. `AppControl.Quit`/`Launch` are not implemented: M1 never restarts the app, so the macOS connector implements only the read-only half that `host.AppPresence` names.

Prefs are read with `defaults export com.elgato.StreamDeck -`, which goes through `cfprefsd` and so sees what the running app last wrote; reading the `.plist` file directly can be stale while the app runs.

- [ ] **Step 1: Wire the root module to `deckformat` and add the plist library**

Append to `go.mod`:

```
require github.com/csmarshall/schrodeck/deckformat v0.0.0

replace github.com/csmarshall/schrodeck/deckformat => ./deckformat
```

Then `go get howett.net/plist@v1.0.1`. (`go mod tidy` runs once the code exists, in step 9.)

- [ ] **Step 2: Write the failing deck tests**

`internal/decks/decks_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package decks

import (
	"testing"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/ports"
)

func TestParseKey(t *testing.T) {
	v, vendor, product, serial, ok := ParseKey("@(1)[4057/143/<deck>]")
	if !ok || v || vendor != 4057 || product != 143 || serial != "<deck>" {
		t.Fatalf("physical: %v %d %d %q %v", v, vendor, product, serial, ok)
	}
	if v, _, _, _, ok := ParseKey("@(0)[]"); !ok || !v {
		t.Fatal("virtual deck not recognized")
	}
	for _, bad := range []string{"", "Devices", "@(1)[4057/143]", "x@(1)[1/2/3]"} {
		if _, _, _, _, ok := ParseKey(bad); ok {
			t.Errorf("ParseKey(%q) accepted", bad)
		}
	}
}

func TestEnumerateAndAnnotate(t *testing.T) {
	p, err := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	if err != nil {
		t.Fatal(err)
	}
	known := map[[2]int]ports.Geometry{{4057, 143}: {Columns: 8, Rows: 4}}
	records := []map[string]any{
		{RecordKey: fixture.Device},                       // the XL the fixture is bound to
		{RecordKey: "@(1)[4057/99/<deck>]"},               // unknown product: no geometry
		{RecordKey: "@(0)[]"},                             // virtual
		{RecordKey: "@(0)[]"},                             // a second virtual deck (U5): same key
		{RecordKey: "SomethingElse", RecordRaw: "opaque"}, // the observed non-record entry
		{RecordKey: "not a device key"},                   // unparseable
	}
	st := Annotate(Enumerate(records, []*profile.Profile{p}, known))
	if len(st) != 4 {
		t.Fatalf("got %d decks, want 4: %+v", len(st), st)
	}
	xl := st[0]
	if !xl.Destination() || xl.Model != fixture.Model || xl.ManifestDeviceID != fixture.Device || xl.SerialHash == "" {
		t.Errorf("XL: %+v", xl)
	}
	if st[1].Destination() || st[1].GeometryKnown {
		t.Errorf("unknown product must not be a destination: %+v", st[1])
	}
	if st[2].Destination() || st[2].KeyUnique || st[3].KeyUnique {
		t.Errorf("two decks sharing @(0)[] must not be destinations: %+v %+v", st[2], st[3])
	}
}

// ADR 0003's check: a DeviceType the tables use without columns/rows fails.
func TestGeometryTablesAreConsistent(t *testing.T) {
	if err := Validate(DeviceTypes, ProductTypes); err != nil {
		t.Fatal(err)
	}
	// Known-bad: a product mapped to a type that has no observed grid.
	if err := Validate(DeviceTypes, map[[2]int]int{{4057, 143}: 2}); err == nil {
		t.Fatal("a mapped DeviceType without columns/rows was accepted")
	}
	// Known-bad: a grid that contradicts R8's documented key count.
	bad := map[int]TypeInfo{2: {Name: "Stream Deck XL", Keys: 32, Columns: 8, Rows: 3}}
	if err := Validate(bad, nil); err == nil {
		t.Fatal("an 8×3 grid for a 32-key type was accepted")
	}
	// Known-bad: a product mapped to a type R8 does not list.
	if err := Validate(DeviceTypes, map[[2]int]int{{4057, 1}: 99}); err == nil {
		t.Fatal("an unknown DeviceType was accepted")
	}
	// Known-good: a consistent pair derives the expected geometry.
	good := map[int]TypeInfo{2: {Name: "Stream Deck XL", Keys: 32, Columns: 8, Rows: 4}}
	if err := Validate(good, map[[2]int]int{{4057, 143}: 2}); err != nil {
		t.Fatal(err)
	}
}

func TestKnownGeometryIsDerived(t *testing.T) {
	saveT, saveP := DeviceTypes, ProductTypes
	t.Cleanup(func() { DeviceTypes, ProductTypes = saveT, saveP })
	DeviceTypes = map[int]TypeInfo{7: {Name: "Stream Deck +", Keys: 8, Dials: 4, Columns: 4, Rows: 2}, 2: {Name: "Stream Deck XL", Keys: 32}}
	ProductTypes = map[[2]int]int{{4057, 1}: 7, {4057, 2}: 2}
	got := KnownGeometry()
	if g := got[[2]int{4057, 1}]; g != (ports.Geometry{Columns: 4, Rows: 2, Dials: 4}) {
		t.Errorf("+ geometry = %+v", g)
	}
	if _, ok := got[[2]int{4057, 2}]; ok {
		t.Error("a type without an observed grid produced a geometry")
	}
}

func TestUnmatched(t *testing.T) {
	p, err := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	if err != nil {
		t.Fatal(err)
	}
	records := []map[string]any{
		{RecordKey: fixture.Device},
		{RecordKey: "@(1)[4057/99/<other>]"},
		{RecordKey: "SomethingElse", RecordRaw: "opaque"},
	}
	profs, decks := Unmatched(records, []*profile.Profile{p})
	if len(profs) != 0 || len(decks) != 1 || decks[0] != "@(1)[4057/99/<other>]" {
		t.Fatalf("matched case: %v %v", profs, decks)
	}
	// Known-bad for U9: a profile bound to an id that differs from every prefs
	// key (here only in letter case) must be reported, not silently skipped.
	profs, _ = Unmatched([]map[string]any{{RecordKey: "@(1)[4057/143/<DECK>]"}}, []*profile.Profile{p})
	if len(profs) != 1 || profs[0] != p.Folder {
		t.Fatalf("a profile whose Device.UUID matches no key was not reported: %v", profs)
	}
}
```

- [ ] **Step 3: Run to verify failure, then implement**

Run: `go test ./internal/decks/` → FAIL, `undefined: ParseKey`.

`internal/decks/decks.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package decks turns the app's device records and profiles into this
// host's deck list (ADR 0003): which decks exist, their geometry, and whether
// each can be a destination. It is platform-neutral; connectors call it.
package decks

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/ports"
)

// RecordKey is the member every device record carries with its key in the
// app's prefs Devices dictionary (contract A, AppPrefs.DeviceRecords).
const RecordKey = "_key"

// RecordRaw marks a Devices entry that is not a device record (observed: one
// string-valued entry); its value is kept under this member for observation.
const RecordRaw = "_raw"

// TypeInfo is one row of Elgato's DeviceType table (R8, documented): the
// device's name, its key count (action slots, excluding dials) and its dial
// count. Variable marks a type whose key count the user or the host chooses
// (Mobile, Voyager, the virtual deck). R8 does not document how the keys are
// arranged, so Columns and Rows stay zero until an observation supplies them
// (docs/references.md), and Validate checks that Columns × Rows equals the
// documented Keys: the grid split is observed, its product is documented.
type TypeInfo struct {
	Name          string
	Keys, Dials   int
	Variable      bool
	Columns, Rows int
}

// DeviceTypes is Elgato's DeviceType enumeration with the documented key and
// dial counts (R8). Only the Columns/Rows split is ever added here, and only
// with an evidence row.
var DeviceTypes = map[int]TypeInfo{
	0:  {Name: "Stream Deck", Keys: 15},
	1:  {Name: "Stream Deck Mini", Keys: 6},
	2:  {Name: "Stream Deck XL", Keys: 32},
	3:  {Name: "Stream Deck Mobile", Variable: true},
	4:  {Name: "Corsair GKeys", Keys: 6},
	5:  {Name: "Stream Deck Pedal", Keys: 3},
	6:  {Name: "Corsair Voyager", Variable: true},
	7:  {Name: "Stream Deck +", Keys: 8, Dials: 4},
	8:  {Name: "SCUF Controller", Keys: 5},
	9:  {Name: "Stream Deck Neo", Keys: 8},
	10: {Name: "Stream Deck Studio", Keys: 32, Dials: 2},
	11: {Name: "Virtual Stream Deck", Variable: true},
	12: {Name: "Galleon 100 SD", Keys: 12, Dials: 2},
	13: {Name: "Stream Deck + XL", Keys: 36, Dials: 6},
}

// ProductTypes maps a physical deck's USB (vendor, product), read from its
// device key, to its DeviceType. Nothing on disk records the DeviceType, so
// each row is an observation (M1 Task 12) with an evidence row; an unmapped
// product has no geometry, so its deck cannot be a destination (fail closed,
// ADRs 0003 and 0030). Virtual decks are in scope (ADR 0003) but have a
// user-chosen grid whose stored location is unknown (U8); until U8 is settled
// they have no geometry either.
var ProductTypes = map[[2]int]int{}

// KnownGeometry derives the (vendor, product) → geometry table that
// Enumerate uses from ProductTypes and DeviceTypes. Products whose type has
// no observed grid are left out.
func KnownGeometry() map[[2]int]ports.Geometry {
	out := map[[2]int]ports.Geometry{}
	for product, typ := range ProductTypes {
		ti, ok := DeviceTypes[typ]
		if !ok || ti.Columns <= 0 || ti.Rows <= 0 {
			continue
		}
		out[product] = ports.Geometry{Columns: ti.Columns, Rows: ti.Rows, Dials: ti.Dials}
	}
	return out
}

// Validate checks the two tables against each other and against R8: every
// mapped product names a known DeviceType that has a grid, and every grid
// multiplies to the documented key count of a fixed-size type.
func Validate(types map[int]TypeInfo, products map[[2]int]int) error {
	for id, ti := range types {
		if ti.Columns == 0 && ti.Rows == 0 {
			continue
		}
		if ti.Columns <= 0 || ti.Rows <= 0 {
			return fmt.Errorf("DeviceType %d (%s): grid %d×%d is incomplete", id, ti.Name, ti.Columns, ti.Rows)
		}
		if !ti.Variable && ti.Columns*ti.Rows != ti.Keys {
			return fmt.Errorf("DeviceType %d (%s): grid %d×%d does not match the documented %d keys", id, ti.Name, ti.Columns, ti.Rows, ti.Keys)
		}
	}
	for product, typ := range products {
		ti, ok := types[typ]
		if !ok {
			return fmt.Errorf("product %v: DeviceType %d is not in R8's table", product, typ)
		}
		if ti.Columns <= 0 || ti.Rows <= 0 {
			return fmt.Errorf("product %v: DeviceType %d (%s) lacks columns/rows", product, typ, ti.Name)
		}
	}
	return nil
}

var physicalKey = regexp.MustCompile(`^@\((\d+)\)\[(\d+)/(\d+)/([^\]]*)\]$`)

// ParseKey splits a device key. Virtual decks are "@(0)[]" (R12).
func ParseKey(key string) (virtual bool, vendor, product int, serial string, ok bool) {
	if key == "@(0)[]" {
		return true, 0, 0, "", true
	}
	m := physicalKey.FindStringSubmatch(key)
	if m == nil {
		return false, 0, 0, "", false
	}
	vendor, _ = strconv.Atoi(m[2])
	product, _ = strconv.Atoi(m[3])
	return false, vendor, product, m[4], true
}

// Enumerate builds the deck list from device records and loaded profiles.
// Records without a parseable key or marked RecordRaw are skipped.
func Enumerate(records []map[string]any, profiles []*profile.Profile, known map[[2]int]ports.Geometry) []ports.Deck {
	var out []ports.Deck
	for _, rec := range records {
		if _, raw := rec[RecordRaw]; raw {
			continue
		}
		key, _ := rec[RecordKey].(string)
		virtual, vendor, product, serial, ok := ParseKey(key)
		if !ok {
			continue
		}
		d := ports.Deck{AppDeviceID: key, Virtual: virtual}
		if !virtual {
			d.Geometry = known[[2]int{vendor, product}]
		}
		if serial != "" {
			sum := sha256.Sum256([]byte(serial))
			d.SerialHash = hex.EncodeToString(sum[:])[:12]
		}
		for _, p := range profiles {
			if p.DeviceUUID() == key {
				d.ManifestDeviceID = key
				d.Model = p.DeviceModel()
				break
			}
		}
		out = append(out, d)
	}
	return out
}

// Status is a deck plus what schrodeck concludes about it.
type Status struct {
	ports.Deck
	GeometryKnown bool
	// KeyUnique: only a deck whose key is unique on this host can be a
	// destination (ADR 0026, review F31).
	KeyUnique bool
}

// Destination reports whether a member copy may be installed on the deck.
func (s Status) Destination() bool { return s.GeometryKnown && s.KeyUnique }

// Annotate adds GeometryKnown and KeyUnique.
func Annotate(ds []ports.Deck) []Status {
	count := map[string]int{}
	for _, d := range ds {
		count[d.AppDeviceID]++
	}
	out := make([]Status, 0, len(ds))
	for _, d := range ds {
		out = append(out, Status{Deck: d, GeometryKnown: d.Geometry.Columns > 0 && d.Geometry.Rows > 0, KeyUnique: count[d.AppDeviceID] == 1})
	}
	return out
}

// Unmatched lists what the deck list cannot tie together: profile folders
// whose Device.UUID equals no device key, and device keys no profile is bound
// to (records marked RecordRaw are not devices and are ignored). Both are
// counted by the U9 observation (is AppDeviceID always ManifestDeviceID?);
// "equal for N of N" means both lists are empty.
func Unmatched(records []map[string]any, profiles []*profile.Profile) (profilesWithoutDeck, decksWithoutProfile []string) {
	keys := map[string]bool{}
	for _, rec := range records {
		if _, raw := rec[RecordRaw]; raw {
			continue
		}
		if key, ok := rec[RecordKey].(string); ok {
			keys[key] = true
		}
	}
	bound := map[string]bool{}
	for _, p := range profiles {
		id := p.DeviceUUID()
		bound[id] = true
		if !keys[id] {
			profilesWithoutDeck = append(profilesWithoutDeck, p.Folder)
		}
	}
	for key := range keys {
		if !bound[key] {
			decksWithoutProfile = append(decksWithoutProfile, key)
		}
	}
	sort.Strings(profilesWithoutDeck)
	sort.Strings(decksWithoutProfile)
	return profilesWithoutDeck, decksWithoutProfile
}
```

Run: `go test ./internal/decks/` → `ok`.

- [ ] **Step 4: Write the host bundle**

`internal/host/host.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package host bundles the connector ports one command run needs. The
// command line builds a Host from the OS connector (cmd/schrodeck); tests
// build one from fakes.
package host

import (
	"time"

	"github.com/csmarshall/schrodeck/internal/ports"
)

// AppPresence is the read-only part of ports.AppControl. M1 never quits or
// launches the app, so it asks for no more than this.
type AppPresence interface {
	Installed() (bool, error)
	Running() (bool, error)
}

// Host is this computer as seen through its connector.
type Host struct {
	Paths    ports.Paths
	Identity ports.HostIdentity
	Prefs    ports.AppPrefs
	Decks    ports.DeviceEnumerator
	App      AppPresence
	// HostNames are this computer's names (friendly name, network name), for
	// redaction.
	HostNames []string
	Now       func() time.Time
	Sleep     func(time.Duration)
}
```

- [ ] **Step 5: Write the failing parser tests**

`internal/connector/macos/parse_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package macos

import (
	"testing"
	"time"

	"github.com/csmarshall/schrodeck/internal/decks"
)

func TestParseIOPlatformUUID(t *testing.T) {
	out := "+-o J000AP  <class IOPlatformExpertDevice>\n    {\n      \"IOPlatformSerialNumber\" = \"<serial>\"\n      \"IOPlatformUUID\" = \"00000000-1111-2222-3333-444444444444\"\n    }\n"
	got, err := ParseIOPlatformUUID(out)
	if err != nil || got != "00000000-1111-2222-3333-444444444444" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ParseIOPlatformUUID("no such key"); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestDropboxPaths(t *testing.T) {
	info := []byte(`{"business":{"path":"/Users/<user>/Work Dropbox"},"personal":{"path":"/Users/<user>/Dropbox","host":1}}`)
	got := DropboxPaths(info)
	if len(got) != 2 || got[0] != "/Users/<user>/Dropbox" || got[1] != "/Users/<user>/Work Dropbox" {
		t.Fatalf("DropboxPaths = %v", got)
	}
	if DropboxPaths([]byte("not json")) != nil {
		t.Fatal("garbage parsed")
	}
}

func TestDeviceRecordsKeepsOddEntriesVisible(t *testing.T) {
	prefs := map[string]any{"Devices": map[string]any{
		"@(1)[4057/143/<deck>]": map[string]any{"DeviceName": "", "map_dev_brightness": uint64(80), "blob": []byte{1, 2}},
		"@(0)[]":                map[string]any{"ESDProfilesInfo": map[string]any{"ESDProfilesPreferred": "abc"}},
		"OpaqueEntry":           "some string",
	}}
	recs, err := DeviceRecords(prefs)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records", len(recs))
	}
	if recs[0][decks.RecordKey] != "@(0)[]" || recs[2][decks.RecordRaw] != "some string" {
		t.Fatalf("records not sorted or odd entry lost: %v", recs)
	}
	if recs[1]["blob"] != "base64:AQI=" {
		t.Fatalf("data not converted: %v", recs[1]["blob"])
	}
	if got, err := Preferred(prefs, "@(0)[]"); err != nil || got != "abc" {
		t.Fatalf("Preferred = %q, %v", got, err)
	}
	if _, err := Preferred(prefs, "@(1)[4057/143/<deck>]"); err == nil {
		t.Fatal("missing preferred profile accepted")
	}
	if _, err := DeviceRecords(map[string]any{}); err == nil {
		t.Fatal("prefs without Devices accepted")
	}
}

func TestPlainDates(t *testing.T) {
	d := time.Date(2026, 10, 2, 1, 2, 3, 0, time.UTC)
	if got := Plain(map[string]any{"d": []any{d}}); got.(map[string]any)["d"].([]any)[0] != "2026-10-02T01:02:03Z" {
		t.Fatalf("Plain = %v", got)
	}
}
```

- [ ] **Step 6: Run to verify failure, then implement the parsers**

Run: `go test ./internal/connector/macos/` → FAIL, `undefined: ParseIOPlatformUUID`.

`internal/connector/macos/parse.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package macos is the macOS connector (contract A). This file holds the
// pure parsing helpers; they build on every OS so Linux CI tests them too.
// The adapters that call macOS tools live in macos_darwin.go.
package macos

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/csmarshall/schrodeck/internal/decks"
)

var ioPlatformUUID = regexp.MustCompile(`"IOPlatformUUID" = "([^"]+)"`)

// ParseIOPlatformUUID extracts IOPlatformUUID from the output of
// `ioreg -rd1 -c IOPlatformExpertDevice`.
func ParseIOPlatformUUID(out string) (string, error) {
	m := ioPlatformUUID.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("ioreg output has no IOPlatformUUID")
	}
	return m[1], nil
}

// DropboxPaths returns the sync folders listed in ~/.dropbox/info.json,
// personal first.
func DropboxPaths(info []byte) []string {
	var accounts map[string]struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(info, &accounts); err != nil {
		return nil
	}
	var out []string
	for _, name := range []string{"personal", "business"} {
		if a, ok := accounts[name]; ok && a.Path != "" {
			out = append(out, a.Path)
		}
	}
	return out
}

// Plain converts plist-decoded values into JSON-friendly ones: data becomes
// "base64:<…>", dates become RFC 3339 strings, containers are converted
// recursively. Everything else is returned as is.
func Plain(x any) any {
	switch t := x.(type) {
	case []byte:
		return "base64:" + base64.StdEncoding.EncodeToString(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = Plain(v)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, v := range t {
			out[i] = Plain(v)
		}
		return out
	}
	return x
}

// DeviceRecords turns prefs["Devices"] into records sorted by key. Each
// record carries its key under decks.RecordKey. An entry that is not a
// dictionary is kept as {RecordKey: key, RecordRaw: value} so observations
// can see it, and the deck list skips it.
func DeviceRecords(prefs map[string]any) ([]map[string]any, error) {
	devs, ok := prefs["Devices"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the Stream Deck prefs have no Devices dictionary")
	}
	keys := make([]string, 0, len(devs))
	for k := range devs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		rec, ok := Plain(devs[k]).(map[string]any)
		if !ok {
			out = append(out, map[string]any{decks.RecordKey: k, decks.RecordRaw: Plain(devs[k])})
			continue
		}
		rec[decks.RecordKey] = k
		out = append(out, rec)
	}
	return out, nil
}

// Preferred returns Devices[id].ESDProfilesInfo.ESDProfilesPreferred (R14).
// Read-only; schrodeck never writes it (ADR 0019).
func Preferred(prefs map[string]any, id string) (string, error) {
	devs, _ := prefs["Devices"].(map[string]any)
	rec, _ := devs[id].(map[string]any)
	info, _ := rec["ESDProfilesInfo"].(map[string]any)
	s, ok := info["ESDProfilesPreferred"].(string)
	if !ok {
		return "", fmt.Errorf("no selected profile recorded for this deck")
	}
	return s, nil
}
```

Run: `go test ./internal/connector/macos/` → `ok` (on Linux too: this file has no build tag).

- [ ] **Step 7: Write the darwin adapter's tests**

`internal/connector/macos/macos_darwin_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build darwin

package macos

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmarshall/schrodeck/internal/conformance"
	"github.com/csmarshall/schrodeck/internal/identity"
)

func connector(t *testing.T) *Connector {
	t.Helper()
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHostIdentityConformance(t *testing.T) {
	conformance.HostIdentityStable(t, connector(t))
}

// Contract A: host_id is stable across two processes. The test re-runs its
// own binary as a helper that prints the host_id (never the hardware id).
func TestHostIDStableAcrossProcesses(t *testing.T) {
	if os.Getenv("SCHRODECK_HOSTID_HELPER") == "1" {
		id, err := identity.HostID(connector(t))
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		fmt.Println("HOSTID", id)
		os.Exit(0)
	}
	mine, err := identity.HostID(connector(t))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestHostIDStableAcrossProcesses")
	cmd.Env = append(os.Environ(), "SCHRODECK_HOSTID_HELPER=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper: %v %s", err, out)
	}
	if !strings.Contains(string(out), "HOSTID "+mine) {
		t.Fatal("host_id differs between two processes")
	}
}

func TestPaths(t *testing.T) {
	c := connector(t)
	if filepath.Base(c.ProfilesDir()) != "ProfilesV3" || !strings.HasPrefix(c.StateDir(), c.Home()) {
		t.Fatalf("paths: %s %s", c.ProfilesDir(), c.StateDir())
	}
}

// Read-only checks against the real app; skipped where it is not installed
// (CI runners).
// liveEnv opts in to tests that read this Mac's real Stream Deck install
// (read-only). They are off by default so `go test ./...` on a development
// Mac never touches a personal install unless asked to.
const liveEnv = "SCHRODECK_LIVE"

func TestLiveReadOnly(t *testing.T) {
	if os.Getenv(liveEnv) != "1" {
		t.Skip("reads the real Stream Deck install; set " + liveEnv + "=1 to run")
	}
	c := connector(t)
	installed, err := c.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Skip("Stream Deck app not installed")
	}
	if _, err := c.Running(); err != nil {
		t.Fatal(err)
	}
	if v, err := c.AppVersion(); err != nil || v == "" {
		t.Fatalf("AppVersion = %q, %v", v, err)
	}
	if _, err := c.Decks(); err != nil {
		t.Fatalf("Decks: %v", err)
	}
}
```

- [ ] **Step 8: Implement the darwin adapter and the connector selection**

`internal/connector/macos/macos_darwin.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build darwin

package macos

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"howett.net/plist"

	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/ports"
)

// Facts about the macOS Stream Deck app (contract B, macOS section).
const (
	appBundle   = "/Applications/Elgato Stream Deck.app" // M5: version in its Info.plist
	appProcess  = "Stream Deck"                          // observed process name (`Stream Deck --runinbk`)
	prefsDomain = "com.elgato.StreamDeck"                // M2
	dataRel     = "Library/Application Support/com.elgato.StreamDeck"
)

// Connector implements the read-only ports M1 needs: Paths, HostIdentity,
// AppPrefs, DeviceEnumerator and the presence half of AppControl. Quit and
// Launch arrive with M3.
type Connector struct {
	home string
}

var (
	_ ports.Paths            = (*Connector)(nil)
	_ ports.HostIdentity     = (*Connector)(nil)
	_ ports.AppPrefs         = (*Connector)(nil)
	_ ports.DeviceEnumerator = (*Connector)(nil)
)

// New returns the connector for the current user.
func New() (*Connector, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Connector{home: home}, nil
}

func (c *Connector) AppDataRoot() string  { return filepath.Join(c.home, dataRel) }
func (c *Connector) ProfilesDir() string  { return filepath.Join(c.AppDataRoot(), "ProfilesV3") }
func (c *Connector) PluginsDir() string   { return filepath.Join(c.AppDataRoot(), "Plugins") }
func (c *Connector) IconPacksDir() string { return filepath.Join(c.AppDataRoot(), "IconPacks") }
func (c *Connector) StateDir() string {
	return filepath.Join(c.home, "Library", "Application Support", "schrodeck")
}
func (c *Connector) LogDir() string { return filepath.Join(c.home, "Library", "Logs", "schrodeck") }
func (c *Connector) ConfigPointer() string {
	return filepath.Join(c.home, ".config", "schrodeck", "config.toml")
}
func (c *Connector) Home() string { return c.home }

// StoreCandidates lists Dropbox folders, then iCloud Drive, that exist.
func (c *Connector) StoreCandidates() []string {
	var out []string
	if info, err := os.ReadFile(filepath.Join(c.home, ".dropbox", "info.json")); err == nil {
		out = append(out, DropboxPaths(info)...)
	}
	icloud := filepath.Join(c.home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if st, err := os.Stat(icloud); err == nil && st.IsDir() {
		out = append(out, icloud)
	}
	return out
}

func (c *Connector) HardwareID() (string, error) {
	out, err := exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", fmt.Errorf("ioreg: %w", err)
	}
	return ParseIOPlatformUUID(string(out))
}

func (c *Connector) UserName() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

func (c *Connector) FriendlyName() string {
	out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// HostNames returns the names this Mac is known by, for redaction.
func (c *Connector) HostNames() []string {
	var names []string
	if n := c.FriendlyName(); n != "" {
		names = append(names, n)
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		names = append(names, strings.TrimSuffix(h, ".local"))
	}
	return names
}

func (c *Connector) Installed() (bool, error) {
	_, err := os.Stat(appBundle)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (c *Connector) Running() (bool, error) {
	err := exec.Command("/usr/bin/pgrep", "-x", appProcess).Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, fmt.Errorf("pgrep: %w", err)
}

func (c *Connector) AppVersion() (string, error) {
	data, err := os.ReadFile(filepath.Join(appBundle, "Contents", "Info.plist"))
	if err != nil {
		return "", err
	}
	var info struct {
		Version string `plist:"CFBundleShortVersionString"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return "", err
	}
	if info.Version == "" {
		return "", fmt.Errorf("the app's Info.plist has no CFBundleShortVersionString")
	}
	return info.Version, nil
}

// prefs reads the app's preferences through `defaults export`, which goes
// through cfprefsd and so sees what the running app last wrote, unlike a
// direct read of the .plist file. Read-only.
func (c *Connector) prefs() (map[string]any, error) {
	out, err := exec.Command("/usr/bin/defaults", "export", prefsDomain, "-").Output()
	if err != nil {
		return nil, fmt.Errorf("defaults export %s: %w", prefsDomain, err)
	}
	var m map[string]any
	if _, err := plist.Unmarshal(out, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *Connector) SelectedProfile(appDeviceID string) (string, error) {
	p, err := c.prefs()
	if err != nil {
		return "", err
	}
	return Preferred(p, appDeviceID)
}

func (c *Connector) DeviceRecords() ([]map[string]any, error) {
	p, err := c.prefs()
	if err != nil {
		return nil, err
	}
	return DeviceRecords(p)
}

func (c *Connector) Decks() ([]ports.Deck, error) {
	recs, err := c.DeviceRecords()
	if err != nil {
		return nil, err
	}
	res, err := profile.LoadAll(os.DirFS(c.ProfilesDir()))
	if err != nil {
		return nil, err
	}
	return decks.Enumerate(recs, res.Profiles, decks.KnownGeometry()), nil
}
```

`internal/connector/connector_darwin.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build darwin

// Package connector picks the OS connector for the running system.
package connector

import (
	"time"

	"github.com/csmarshall/schrodeck/internal/connector/macos"
	"github.com/csmarshall/schrodeck/internal/host"
)

// New returns this computer's Host.
func New() (*host.Host, error) {
	c, err := macos.New()
	if err != nil {
		return nil, err
	}
	return &host.Host{Paths: c, Identity: c, Prefs: c, Decks: c, App: c, HostNames: c.HostNames(), Now: time.Now, Sleep: time.Sleep}, nil
}
```

`internal/connector/connector_other.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !darwin

// Package connector picks the OS connector for the running system.
package connector

import (
	"fmt"
	"runtime"

	"github.com/csmarshall/schrodeck/internal/host"
)

// New reports that no connector exists for this OS yet (contract A: a new
// OS is a new connector).
func New() (*host.Host, error) {
	return nil, fmt.Errorf("no schrodeck connector for %s yet", runtime.GOOS)
}
```

- [ ] **Step 9: Tidy, test, and check the architecture rules**

Run (on a Mac): `go mod tidy && go vet ./... && go test ./internal/... && go run ./tools/archcheck/cmd/archcheck -mode core -dir . && for g in linux windows; do GOOS=$g go build ./...; done`
Expected: every package `ok`; `TestLiveReadOnly` is skipped (it reads the real install only with `SCHRODECK_LIVE=1`; run `SCHRODECK_LIVE=1 go test -run TestLiveReadOnly ./internal/connector/macos/` once by hand on a Mac with the app installed, and it must pass); `archcheck core: ok`; the cross-builds succeed (the darwin-only files are build-tagged).

- [ ] **Step 10: Document the record shape in contract A**

In `docs/contracts/os-connector.md` § 5 `AppPrefs`, replace the line

`    DeviceRecords() ([]map[string]any, error) // raw device entries, for DeviceEnumerator`

with

`    DeviceRecords() ([]map[string]any, error) // raw device entries, for DeviceEnumerator; each carries its prefs key under "_key", and an entry that is not a dictionary is returned as {"_key": key, "_raw": value}`

and add after the table: `Observed on macOS (2026-10-02): the \`Devices\` dictionary also holds one entry whose value is a string, not a device record. The connector keeps it visible as \`_raw\` (for observations) and the deck list skips it.`

In the same file, replace the `Deck` field comment `// columns, rows, dials (from Elgato's DeviceType table [R8](../references.md))` with `// columns, rows, dials: the DeviceType's documented key and dial counts (R8) with an observed grid split; see ADR 0003` and add after the `DeviceEnumerator` table: `Geometry keying (2026-10-02, M1): a physical deck's DeviceType comes from its USB (vendor, product), read from its device key, through an observed product → DeviceType map; its key and dial counts come from R8, and its columns × rows split is observed and must multiply to R8's key count. A product without an observed mapping, or a type without an observed grid, has no geometry, so the deck is not a destination.`

Run `go run ./tools/portcheck/cmd/portcheck` → `portcheck: 9 ports and 2 structs match contract A` (comment changes do not change any signature or field).

- [ ] **Step 11: Record the geometry keying in ADR 0003 and correct R8's wording**

In `docs/adr/0003-decks-are-local-geometry-compatibility.md`, append to the `Status:` line: `` Revised 2026-10-02 (M1, implementation): R8 documents key and dial counts per DeviceType but not the columns × rows split; a physical deck's DeviceType is found through an observed USB product → DeviceType map, and the split is observed and checked against R8's key count. Virtual decks remain in scope; their grid source (U8) is settled in M1. `` Then, in its `## Verified by` list, replace `- A geometry-table test that fails if a known DeviceType lacks columns/rows.` with `- A geometry-table test that fails if a mapped DeviceType lacks columns/rows, or if a grid does not multiply to R8's key count: \`TestGeometryTablesAreConsistent\` (M1 Task 10), with known-bad tables.`

In `docs/references.md`, R8's last column currently says the enum comes "with columns × rows". Replace that cell with: `DeviceType enum (Stream Deck, Mini, XL, +, Neo, + XL, Virtual, …) with each model's key count and dial count. Columns × rows are not tabulated; the SDK reports a connected device's \`columns\` and \`rows\` at runtime. Model compatibility is derived from this plus an observed grid split (ADR 0003).`

- [ ] **Step 12: Commit**

```bash
git add go.mod go.sum internal/decks internal/host internal/connector docs/contracts/os-connector.md docs/adr/0003-decks-are-local-geometry-compatibility.md docs/references.md
git commit -m "feat(#<n>): deck enumeration and the read-only macOS connector"
```

---

### Task 11: `doctor`, `inventory`, `status`, `observe`, `fixture export`

**Files:**
- Create: `internal/doctor/doctor.go`, `internal/doctor/doctor_test.go`, `internal/cli/observe.go`, `internal/cli/testdata/golden/{doctor-nohost,status-host,inventory,doctor-unaccepted,doctor-accepted}.json`
- Modify: `internal/cli/cli.go`, `internal/cli/commands.go`, `internal/cli/cli_test.go` (full replacements below), `cmd/schrodeck/main.go`, `docs/contracts/cli-json.md`
- Delete: `internal/cli/testdata/golden/doctor-empty.json`, `internal/cli/testdata/golden/doctor-fail.json` (M0's placeholder `Checks` wiring is replaced by real probes)

**Interfaces:**
- Consumes: everything above.
- Produces:
  - package `doctor`: `KnownFile = "known-fingerprints.json"`; `Accepted{Digest, AppVersion string; AcceptedAt time.Time; Schema profile.Schema}`; `Known{Accepted []Accepted}` with `Contains(digest) bool`; `LoadKnown(stateDir) (Known, error)`; `SaveKnown(stateDir, Known) error` (stage + rename); `ContractB(*host.Host, profile.LoadResult) []probe.Probe` (ids `M1, M2, M5`); `Fingerprint(schema profile.Schema, digest string, known Known) probe.Probe` (id `FP`).
  - package `cli`: `Env{Stdout, Stderr io.Writer; Version string; Logger *slog.Logger; Host *host.Host}` (M0's `Checks`, `Check`, `CheckResult` and `Status*` are removed); commands `version`, `status`, `inventory [--show-ids]`, `doctor [--accept-fingerprint]`, `observe start|stop <name> [--out FILE]`, `fixture export --profile <folder> --out <dir> [--name <folder>]`; `--json` may appear anywhere after the command name, and flags may follow positional arguments.

`doctor` fails until the user accepts this host's fingerprint: that is ADR 0015's "the user confirms the new fingerprint as known-good", and the only write `doctor` makes (to schrodeck's state dir). When a fingerprint is not known-good, the evidence lists the key paths that appeared or disappeared since the last accepted one. **`--accept-fingerprint` is refused** (reported as `accept_refused`, nothing written) unless every other check passes and every profile loaded: a profile that failed to load is missing from the fingerprint, so accepting then would bless a partial view (`TestAcceptFingerprintRefusedWhileOtherChecksFail`, with a P7 failure as the known-bad and the clean install as the control).

`inventory` also reports what the deck list cannot tie together, under `unmatched`: profiles whose `Device.UUID` equals no prefs device key, and device keys no profile is bound to. That is what Task 12's U9 observation counts.

The redactor every command uses is built with the serials of every device id the host knows (prefs keys and `Device.UUID`s, through `redact.SerialsFrom`), and every `--out` (and `fixture export`'s final folder) passes `pathguard.RefuseInside` against the app data root and the profiles directory before anything is read; `--name` must be a single folder name. `TestFixtureExport` and `TestObserveOutRefusesAppData` are the known-bad cases: `--out` inside `ProfilesV3`, a `../` name, a symlinked output directory, each refused with exit 2 and the profiles directory left untouched.

- [ ] **Step 1: Write the doctor tests**

`internal/doctor/doctor_test.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/probe"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/host"
	"github.com/csmarshall/schrodeck/internal/ports/fake"
)

func TestKnownRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	k, err := LoadKnown(dir)
	if err != nil || len(k.Accepted) != 0 {
		t.Fatalf("missing file: %v %v", k, err)
	}
	k.Accepted = append(k.Accepted, Accepted{Digest: "abc", AppVersion: "7.5.1", AcceptedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	if err := SaveKnown(dir, k); err != nil {
		t.Fatal(err)
	}
	back, err := LoadKnown(dir)
	if err != nil || !back.Contains("abc") || back.Contains("abd") {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("stage file left behind: %v", entries)
	}
}

func run(p probe.Probe) (probe.Status, string, []string) { return p.Run(context.Background()) }

func TestFingerprintProbe(t *testing.T) {
	old := profile.Schema{KeyPaths: []string{"manifest:Name", "manifest:Gone"}}
	cur := profile.Schema{KeyPaths: []string{"manifest:Name", "manifest:New"}}
	known := Known{Accepted: []Accepted{{Digest: "0123456789abcdef", Schema: old}}}
	if st, _, _ := run(Fingerprint(cur, "0123456789abcdef", known)); st != probe.Pass {
		t.Fatalf("known digest: %s", st)
	}
	st, detail, ev := run(Fingerprint(cur, "fedcba9876543210", known))
	if st != probe.Fail || !strings.Contains(detail, "--accept-fingerprint") {
		t.Fatalf("unknown digest: %s %s", st, detail)
	}
	if strings.Join(ev, "|") != "new key path: manifest:New|key path gone: manifest:Gone" {
		t.Fatalf("evidence = %v", ev)
	}
}

func hostWith(t *testing.T, selected map[string]string, records []map[string]any) (*host.Host, profile.LoadResult) {
	t.Helper()
	paths := fake.Paths{Root: t.TempDir()}
	if err := fixture.WriteTo(paths.ProfilesDir(), fixture.XL().FS()); err != nil {
		t.Fatal(err)
	}
	res, err := profile.LoadAll(os.DirFS(paths.ProfilesDir()))
	if err != nil {
		t.Fatal(err)
	}
	return &host.Host{Paths: paths, Prefs: fake.Prefs{Version: "7.5.1", Selected: selected, Records: records}}, res
}

func probeByID(ps []probe.Probe, id string) probe.Probe {
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	return probe.Probe{}
}

func TestM2(t *testing.T) {
	virtual := "@(0)[]"
	records := []map[string]any{{decks.RecordKey: fixture.Device}, {decks.RecordKey: virtual}, {decks.RecordKey: "x", decks.RecordRaw: "s"}}

	h, res := hostWith(t, map[string]string{fixture.Device: fixture.XL().ID}, records)
	if st, _, _ := run(probeByID(ContractB(h, res), "M2")); st != probe.Pass {
		t.Fatalf("matching selection: %s", st)
	}
	h, res = hostWith(t, map[string]string{fixture.Device: fixture.XL().ID, virtual: "11111111-2222-4333-8444-555555555555"}, records)
	if st, _, ev := run(probeByID(ContractB(h, res), "M2")); st != probe.Info || len(ev) != 1 {
		t.Fatalf("virtual deck pointing nowhere (U4) should be info: %s %v", st, ev)
	}
	h, res = hostWith(t, map[string]string{fixture.Device: "11111111-2222-4333-8444-555555555555"}, records)
	if st, _, _ := run(probeByID(ContractB(h, res), "M2")); st != probe.Fail {
		t.Fatalf("physical deck pointing nowhere must fail: %s", st)
	}
}

func TestM1AndM5(t *testing.T) {
	h, res := hostWith(t, nil, nil)
	if st, _, _ := run(probeByID(ContractB(h, res), "M1")); st != probe.Pass {
		t.Fatalf("M1 on a readable data root: %s", st)
	}
	if st, _, _ := run(probeByID(ContractB(h, res), "M5")); st != probe.Pass {
		t.Fatalf("M5 with a version: %s", st)
	}
	h.Paths = fake.Paths{Root: filepath.Join(t.TempDir(), "absent")}
	h.Prefs = fake.Prefs{}
	if st, _, _ := run(probeByID(ContractB(h, res), "M1")); st != probe.Fail {
		t.Fatalf("M1 on a missing data root: %s", st)
	}
	if st, _, _ := run(probeByID(ContractB(h, res), "M5")); st != probe.Fail {
		t.Fatalf("M5 without a version: %s", st)
	}
}
```

- [ ] **Step 2: Run to verify failure, then implement**

Run: `go test ./internal/doctor/` → FAIL, `undefined: LoadKnown`.

`internal/doctor/doctor.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package doctor assembles `schrodeck doctor`'s read-only tier: contract B's
// probes for this OS, contract C's probes (deckformat/probe), and the format
// fingerprint check of ADR 0015 against this host's known-good set.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/probe"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/host"
)

// KnownFile holds this host's accepted fingerprints, in its state dir.
const KnownFile = "known-fingerprints.json"

// Accepted is one fingerprint the user confirmed as known-good.
type Accepted struct {
	Digest     string         `json:"digest"`
	AppVersion string         `json:"app_version"`
	AcceptedAt time.Time      `json:"accepted_at"`
	Schema     profile.Schema `json:"schema"`
}

// Known is the known-good set.
type Known struct {
	Accepted []Accepted `json:"accepted"`
}

// Contains reports whether digest was accepted.
func (k Known) Contains(digest string) bool {
	for _, a := range k.Accepted {
		if a.Digest == digest {
			return true
		}
	}
	return false
}

// LoadKnown reads the known-good set; a missing file is an empty set.
func LoadKnown(stateDir string) (Known, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, KnownFile))
	if errors.Is(err, os.ErrNotExist) {
		return Known{}, nil
	}
	if err != nil {
		return Known{}, err
	}
	var k Known
	if err := json.Unmarshal(b, &k); err != nil {
		return Known{}, fmt.Errorf("%s: %w", KnownFile, err)
	}
	return k, nil
}

// SaveKnown writes the set by stage + rename, so a crash leaves the old file.
func SaveKnown(stateDir string, k Known) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir, KnownFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(stateDir, KnownFile))
}

func readOnly(id, contract string, run func() (probe.Status, string, []string)) probe.Probe {
	return probe.Probe{ID: id, Contract: contract, Tier: probe.ReadOnly, Run: func(context.Context) (probe.Status, string, []string) { return run() }}
}

// ContractB returns contract B's read-only probes for macOS (M1, M2, M5).
// M3 and M4 restart the app and arrive with M3.
func ContractB(h *host.Host, load profile.LoadResult) []probe.Probe {
	return []probe.Probe{
		readOnly("M1", "B", func() (probe.Status, string, []string) {
			entries, err := os.ReadDir(h.Paths.AppDataRoot())
			if err != nil {
				return probe.Fail, "the Stream Deck data root is not readable", []string{err.Error()}
			}
			return probe.Pass, fmt.Sprintf("data root readable (%d entries)", len(entries)), nil
		}),
		readOnly("M2", "B", func() (probe.Status, string, []string) { return m2(h, load) }),
		readOnly("M5", "B", func() (probe.Status, string, []string) {
			v, err := h.Prefs.AppVersion()
			if err != nil || v == "" {
				return probe.Fail, "app version unreadable", []string{fmt.Sprint(err)}
			}
			return probe.Pass, "app " + v, nil
		}),
	}
}

func m2(h *host.Host, load profile.LoadResult) (probe.Status, string, []string) {
	recs, err := h.Prefs.DeviceRecords()
	if err != nil {
		return probe.Fail, "prefs unreadable", []string{err.Error()}
	}
	folders := map[string]bool{}
	for _, p := range load.Profiles {
		folders[strings.ToLower(strings.TrimSuffix(p.Folder, profile.Suffix))] = true
	}
	status := probe.Pass
	var ev []string
	for _, rec := range recs {
		if _, raw := rec[decks.RecordRaw]; raw {
			continue
		}
		key, _ := rec[decks.RecordKey].(string)
		virtual, _, _, _, ok := decks.ParseKey(key)
		if !ok {
			continue
		}
		sel, err := h.Prefs.SelectedProfile(key)
		if err != nil {
			continue // a deck with no selection recorded is fine
		}
		if folders[strings.ToLower(sel)] {
			continue
		}
		if virtual {
			ev = append(ev, key+": selected profile has no folder on disk (known for virtual decks, U4)")
			if status == probe.Pass {
				status = probe.Info
			}
			continue
		}
		ev = append(ev, key+": selected profile has no folder on disk")
		status = probe.Fail
	}
	detail := map[probe.Status]string{
		probe.Pass: "every deck's selected profile exists",
		probe.Info: "only virtual decks point at missing profiles",
		probe.Fail: "a physical deck's selected profile is missing",
	}[status]
	return status, detail, ev
}

// Fingerprint is ADR 0015's check: is this host's current format
// fingerprint in its known-good set? When not, the evidence lists the key
// paths that differ from the most recently accepted schema.
func Fingerprint(schema profile.Schema, digest string, known Known) probe.Probe {
	return readOnly("FP", "C", func() (probe.Status, string, []string) {
		if known.Contains(digest) {
			return probe.Pass, "format fingerprint " + digest[:12] + " is known-good", nil
		}
		var ev []string
		if n := len(known.Accepted); n > 0 {
			added, removed := diffKeys(known.Accepted[n-1].Schema.KeyPaths, schema.KeyPaths)
			for _, a := range added {
				ev = append(ev, "new key path: "+a)
			}
			for _, r := range removed {
				ev = append(ev, "key path gone: "+r)
			}
		}
		return probe.Fail, "format fingerprint " + digest[:12] + " is not known-good: review, then run `schrodeck doctor --accept-fingerprint`", ev
	})
}

func diffKeys(old, cur []string) (added, removed []string) {
	o, c := map[string]bool{}, map[string]bool{}
	for _, k := range old {
		o[k] = true
	}
	for _, k := range cur {
		c[k] = true
		if !o[k] {
			added = append(added, k)
		}
	}
	for _, k := range old {
		if !c[k] {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
```

Run: `go test ./internal/doctor/` → `ok`.

- [ ] **Step 3: Replace the CLI tests**

`internal/cli/cli_test.go` (full replacement):

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/host"
	"github.com/csmarshall/schrodeck/internal/ports"
	"github.com/csmarshall/schrodeck/internal/ports/fake"
)

var update = flag.Bool("update", false, "rewrite golden files from current output")

// enumerated is a DeviceEnumerator over a fake host's records and profiles,
// using the same decks.Enumerate the macOS connector uses.
type enumerated struct{ h *host.Host }

func (e enumerated) Decks() ([]ports.Deck, error) {
	recs, err := e.h.Prefs.DeviceRecords()
	if err != nil {
		return nil, err
	}
	res, err := profile.LoadAll(os.DirFS(e.h.Paths.ProfilesDir()))
	if err != nil {
		return nil, err
	}
	return decks.Enumerate(recs, res.Profiles, map[[2]int]ports.Geometry{{4057, 143}: {Columns: 8, Rows: 4}}), nil
}

// fakeHost is a computer with the fixture XL profile installed.
func fakeHost(t *testing.T) *host.Host {
	t.Helper()
	paths := fake.Paths{Root: t.TempDir()}
	if err := fixture.WriteTo(paths.ProfilesDir(), fixture.XL().FS()); err != nil {
		t.Fatal(err)
	}
	h := &host.Host{
		Paths:    paths,
		Identity: fake.HostIdentity{Hardware: "HW-TEST-0001", User: "alice", Friendly: "test mac"},
		Prefs: fake.Prefs{
			Version:  "7.5.1",
			Selected: map[string]string{fixture.Device: fixture.XL().ID},
			Records:  []map[string]any{{decks.RecordKey: fixture.Device, "DeviceName": ""}},
		},
		App:       fake.NewApp(paths.ProfilesDir()),
		HostNames: []string{"test mac"},
		Now:       func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) },
		Sleep:     func(time.Duration) {},
	}
	h.Decks = enumerated{h}
	return h
}

func testEnv(logTo io.Writer, h *host.Host) (Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	if logTo == nil {
		logTo = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(logTo, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return Env{Stdout: &out, Stderr: &errb, Version: "test", Logger: logger, Host: h}, &out, &errb
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("stdout\n%s\nwant (golden %s)\n%s", got, path, want)
	}
}

func TestGoldenJSON(t *testing.T) {
	cases := []struct {
		golden   string
		args     []string
		withHost bool
		wantCode int
	}{
		{"version", []string{"version", "--json"}, false, ExitOK},
		{"status", []string{"status", "--json"}, false, ExitOK},
		{"doctor-nohost", []string{"doctor", "--json"}, false, ExitFail},
		{"status-host", []string{"status", "--json"}, true, ExitOK},
		{"inventory", []string{"inventory", "--json"}, true, ExitOK},
		{"doctor-unaccepted", []string{"doctor", "--json"}, true, ExitFail},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			var h *host.Host
			if c.withHost {
				h = fakeHost(t)
			}
			env, out, errb := testEnv(nil, h)
			if code := Run(context.Background(), c.args, env); code != c.wantCode {
				t.Fatalf("exit %d, want %d; stderr %q", code, c.wantCode, errb.String())
			}
			golden(t, c.golden, out.Bytes())
		})
	}
}

func TestDoctorAcceptFingerprint(t *testing.T) {
	h := fakeHost(t)
	env, out, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitOK {
		t.Fatalf("doctor --accept-fingerprint: exit %d\n%s", code, out.String())
	}
	golden(t, "doctor-accepted", out.Bytes())
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor"}, env); code != ExitOK {
		t.Fatal("doctor still fails after the fingerprint was accepted")
	}
	// Known-bad: a new manifest key must make doctor fail again.
	manifest := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "manifest.json")
	b, _ := os.ReadFile(manifest)
	if err := os.WriteFile(manifest, bytes.Replace(b, []byte(`"Name":`), []byte(`"NewField":1,"Name":`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	env, out, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor"}, env); code != ExitFail {
		t.Fatal("doctor passed a changed format fingerprint")
	}
	if !strings.Contains(out.String(), "new key path: manifest:NewField") {
		t.Fatalf("doctor did not name the new key:\n%s", out.String())
	}
}

func TestAcceptFingerprintRefusedWhileOtherChecksFail(t *testing.T) {
	// Known-bad P7: an unexpected file inside the profile makes the loader
	// refuse it. Accepting now would record a fingerprint that leaves the
	// profile out, so doctor must refuse and write nothing.
	h := fakeHost(t)
	stray := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "stray.bin")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := fixture.CopyOf(fixture.XL(), "second")
	if err := fixture.WriteTo(h.Paths.ProfilesDir(), second.FS()); err != nil {
		t.Fatal(err)
	}
	env, out, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitFail {
		t.Fatalf("exit %d, want %d", code, ExitFail)
	}
	if !strings.Contains(out.String(), `"accept_refused":`) || strings.Contains(out.String(), `"accepted_now":true`) {
		t.Fatalf("accept was not refused:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "known-fingerprints.json")); !os.IsNotExist(err) {
		t.Fatal("a refused accept still wrote the known-good set")
	}
	// Known-good control: once the install is clean, the same command accepts.
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}
	env, out, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitOK {
		t.Fatalf("clean install: exit %d\n%s", code, out.String())
	}
}

func TestInventoryRedactsDeviceIDsByDefault(t *testing.T) {
	h := fakeHost(t)
	real := "@(1)[4057/143/" + "AB12" + "CD34EF" + "]"
	manifest := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "manifest.json")
	b, _ := os.ReadFile(manifest)
	if err := os.WriteFile(manifest, bytes.Replace(b, []byte(fixture.Device), []byte(real), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	env, out, _ := testEnv(nil, h)
	Run(context.Background(), []string{"inventory", "--json"}, env)
	if strings.Contains(out.String(), "CD34EF") {
		t.Fatal("inventory printed a deck serial without --show-ids")
	}
	env, out, _ = testEnv(nil, h)
	Run(context.Background(), []string{"inventory", "--json", "--show-ids"}, env)
	if !strings.Contains(out.String(), "CD34EF") {
		t.Fatal("--show-ids did not show the id")
	}
}

func TestInventoryReportsUnmatched(t *testing.T) {
	h := fakeHost(t)
	prefs := h.Prefs.(fake.Prefs)
	other := "@(1)[4057/99/" + "ZZ98" + "YY76" + "]"
	prefs.Records = append(prefs.Records, map[string]any{decks.RecordKey: other})
	h.Prefs = prefs
	env, out, _ := testEnv(nil, h)
	Run(context.Background(), []string{"inventory", "--json"}, env)
	var doc struct {
		Data struct {
			Unmatched *struct {
				Profiles []string `json:"profiles"`
				Decks    []string `json:"decks"`
			} `json:"unmatched"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	u := doc.Data.Unmatched
	if u == nil || len(u.Decks) != 1 || len(u.Profiles) != 0 {
		t.Fatalf("unmatched = %+v\n%s", u, out.String())
	}
	if strings.Contains(out.String(), "YY76") || u.Decks[0] != "@(1)[4057/99/<deck>]" {
		t.Fatalf("unmatched deck key not redacted: %q", u.Decks[0])
	}
}

func TestObserveStartStop(t *testing.T) {
	h := fakeHost(t)
	env, _, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-title"}, env); code != ExitOK {
		t.Fatalf("start: exit %d %s", code, errb.String())
	}
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-title"}, env); code != ExitFail {
		t.Fatal("a second start of the same observation was allowed")
	}
	// The person's action in the app: rename button 1,0's title.
	edited := fixture.XL()
	edited.Pages[0].Buttons[1].Title = "Paste"
	if err := os.RemoveAll(h.Paths.ProfilesDir()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.WriteTo(h.Paths.ProfilesDir(), edited.FS()); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "obs", "u0-title.md")
	env, out, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-title", "--out", report}, env); code != ExitOK {
		t.Fatalf("stop: exit %d %s", code, errb.String())
	}
	md, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "key 1,0") || !strings.Contains(string(md), `"Paste"`) || !bytes.Equal(md, out.Bytes()) {
		t.Fatalf("report:\n%s", md)
	}
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "observe", "u0-title")); !os.IsNotExist(err) {
		t.Fatal("unredacted snapshots were left behind after stop")
	}
}

func TestFixtureExport(t *testing.T) {
	h := fakeHost(t)
	out := filepath.Join(t.TempDir(), "fx")
	env, stdout, errb := testEnv(nil, h)
	code := Run(context.Background(), []string{"fixture", "export", "--profile", fixture.XL().Folder(), "--out", out, "--name", "EXPORTED.sdProfile", "--json"}, env)
	if code != ExitOK {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	var doc struct {
		Data fixtureData `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Data.Folder != "EXPORTED.sdProfile" || len(doc.Data.Strings) == 0 {
		t.Fatalf("data = %+v", doc.Data)
	}
	if _, err := os.Stat(filepath.Join(out, "EXPORTED.sdProfile", "manifest.json")); err != nil {
		t.Fatal(err)
	}
	env, _, _ = testEnv(nil, h)
	inside := filepath.Join(h.Paths.ProfilesDir(), "x")
	if code := Run(context.Background(), []string{"fixture", "export", "--profile", fixture.XL().Folder(), "--out", inside}, env); code == ExitOK {
		t.Fatal("export into the app's data was allowed")
	}
	// Known-bad: --name walking out of --out and into the app's data.
	escape := filepath.Join("..", "app", "ProfilesV3", "N.sdProfile")
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"fixture", "export", "--profile", fixture.XL().Folder(), "--out", filepath.Join(filepath.Dir(h.Paths.AppDataRoot()), "fx2"), "--name", escape}, env); code != ExitUsage {
		t.Fatalf("a ../ --name was not refused as a usage error: exit %d", code)
	}
	// Known-bad: --out is a symlink into the app's data.
	link := filepath.Join(t.TempDir(), "looks-safe")
	if err := os.Symlink(h.Paths.ProfilesDir(), link); err != nil {
		t.Fatal(err)
	}
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"fixture", "export", "--profile", fixture.XL().Folder(), "--out", link}, env); code != ExitUsage {
		t.Fatalf("export through a symlink into the app's data: exit %d", code)
	}
	assertAppDataUntouched(t, h)
}

// assertAppDataUntouched fails if the profiles directory holds anything but
// the one fixture profile the fake host was created with.
func assertAppDataUntouched(t *testing.T, h *host.Host) {
	t.Helper()
	entries, err := os.ReadDir(h.Paths.ProfilesDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fixture.XL().Folder() {
		t.Fatalf("the app's profiles directory was written to: %v", entries)
	}
}

func TestObserveOutRefusesAppData(t *testing.T) {
	h := fakeHost(t)
	env, _, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-guard"}, env); code != ExitOK {
		t.Fatal("start failed")
	}
	inside := filepath.Join(h.Paths.ProfilesDir(), "report.md")
	env, _, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-guard", "--out", inside}, env); code != ExitUsage {
		t.Fatalf("observe stop --out inside the app's data: exit %d %s", code, errb.String())
	}
	assertAppDataUntouched(t, h)
	// The refused stop must not have consumed the observation.
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "observe", "u0-guard")); err != nil {
		t.Fatal("a refused stop deleted the started observation")
	}
}

func TestJSONStdoutIsOneLineEvenWithDebugLogs(t *testing.T) {
	for _, h := range []*host.Host{nil, fakeHost(t)} {
		var logs bytes.Buffer
		env, out, _ := testEnv(&logs, h)
		Run(context.Background(), []string{"doctor", "--json"}, env)
		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(lines) != 1 {
			t.Fatalf("stdout has %d lines, want 1: %q", len(lines), out.String())
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &doc); err != nil {
			t.Fatalf("stdout is not one JSON document: %v", err)
		}
		if logs.Len() == 0 {
			t.Fatal("debug logs were expected on the log writer")
		}
	}
}

func TestUsageErrors(t *testing.T) {
	cases := [][]string{
		nil,
		{"nope"},
		{"--json", "status"},
		{"status", "--bogus"},
		{"status", "extra-arg"},
		{"observe"},
		{"observe", "start"},
		{"observe", "start", "Bad Name"},
		{"observe", "pause", "x"},
		{"fixture", "export"},
		{"fixture", "export", "--profile", "x.sdProfile", "--out", "/tmp/y", "--name", "nosuffix"},
	}
	for _, args := range cases {
		env, out, errb := testEnv(nil, fakeHost(t))
		code := Run(context.Background(), args, env)
		if code != ExitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, ExitUsage)
		}
		if out.Len() != 0 {
			t.Errorf("%q: usage errors must not print to stdout, got %q", args, out.String())
		}
		if errb.Len() == 0 {
			t.Errorf("%q: usage error printed no message", args)
		}
	}
}

func TestFlagsMayFollowArguments(t *testing.T) {
	env, out, _ := testEnv(nil, fakeHost(t))
	if code := Run(context.Background(), []string{"inventory", "--show-ids", "--json"}, env); code != ExitOK {
		t.Fatal(code)
	}
	if !strings.HasPrefix(out.String(), `{"schema_version":1`) {
		t.Fatalf("--json after another flag was not honored: %q", out.String())
	}
}

func TestHelpExitsZero(t *testing.T) {
	env, _, errb := testEnv(nil, nil)
	if code := Run(context.Background(), []string{"help"}, env); code != ExitOK {
		t.Fatalf("help: exit %d", code)
	}
	for _, name := range []string{"version", "status", "inventory", "doctor", "observe", "fixture"} {
		if !strings.Contains(errb.String(), name) {
			t.Errorf("help text lacks command %q", name)
		}
	}
}

func TestTextOutput(t *testing.T) {
	env, out, _ := testEnv(nil, fakeHost(t))
	if code := Run(context.Background(), []string{"status"}, env); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out.String(), "schrodeck test\nhost 7db4c9ba0faa") {
		t.Fatalf("status text = %q", out.String())
	}
}

// Contract E's documented schema_version must equal the code's.
func TestSchemaVersionDocumented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "cli-json.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("Current `schema_version`: **%d**", SchemaVersion)
	if !strings.Contains(string(doc), want) {
		t.Fatalf("docs/contracts/cli-json.md does not contain %q", want)
	}
}
```

- [ ] **Step 4: Write the golden files**

These are the expected contract E documents for the fake host (the synthetic XL fixture, user `alice`, hardware id `HW-TEST-0001`). The `hash` value is the one the independent Python reference produced in Task 4, and `host_id` the one Python produced in Task 9; the fingerprint is computed by this code, so it proves consistency only. Delete `doctor-empty.json` and `doctor-fail.json`.

`internal/cli/testdata/golden/doctor-nohost.json`:

```json
{"schema_version":1,"command":"doctor","ok":false,"error":{"message":"this command needs an OS connector, and there is none for this OS yet"}}
```

`internal/cli/testdata/golden/status-host.json`:

```json
{"schema_version":1,"command":"status","ok":true,"data":{"schrodeck_version":"test","host":{"host_id":"7db4c9ba0faa","app":{"installed":true,"running":false,"version":"7.5.1"},"decks":1,"profiles":1}}}
```

`internal/cli/testdata/golden/inventory.json`:

```json
{"schema_version":1,"command":"inventory","ok":true,"data":{"host_id":"7db4c9ba0faa","app_version":"7.5.1","norm_version":1,"fingerprint":"37013d550b55c32c95c2df70bf7ce7e0bf990ff787a33d789a1fbd014c1bf448","decks":[{"key":"@(1)[4057/143/<deck>]","model":"20GAT9902","columns":8,"rows":4,"virtual":false,"destination":true}],"profiles":[{"folder":"AAAAAAAA-0000-4000-8000-000000000001.sdProfile","name":"Fixture XL","device":"@(1)[4057/143/<deck>]","pages":3,"hash":"3224b89189a93d552a4c479bae1b722c185489a7d8597cc4c8f680f444737200"}]}}
```

`internal/cli/testdata/golden/doctor-unaccepted.json`:

```json
{"schema_version":1,"command":"doctor","ok":false,"data":{"tier":"read-only","fingerprint":"37013d550b55c32c95c2df70bf7ce7e0bf990ff787a33d789a1fbd014c1bf448","checks":[{"id":"M1","contract":"B","tier":"read-only","status":"pass","detail":"data root readable (1 entries)"},{"id":"M2","contract":"B","tier":"read-only","status":"pass","detail":"every deck's selected profile exists"},{"id":"M5","contract":"B","tier":"read-only","status":"pass","detail":"app 7.5.1"},{"id":"P1","contract":"C","tier":"read-only","status":"pass","detail":"1 profile(s) load"},{"id":"P2","contract":"C","tier":"read-only","status":"pass","detail":"every profile has Version 3.0"},{"id":"P3","contract":"C","tier":"read-only","status":"pass","detail":"every profile names its device"},{"id":"P6","contract":"C","tier":"read-only","status":"info","detail":"1 Open action(s) use an absolute path outside {{HOME}}","evidence":["AAAAAAAA-0000-4000-8000-000000000001.sdProfile › key 0,0: /Users/<user>/bin/demo.sh"]},{"id":"P7","contract":"C","tier":"read-only","status":"pass","detail":"every file is allow-listed"},{"id":"P8","contract":"C","tier":"read-only","status":"pass","detail":"no cross-profile or foreign-device references"},{"id":"P9+P11","contract":"C","tier":"read-only","status":"pass","detail":"copies hash equal under norm_version 1"},{"id":"FP","contract":"C","tier":"read-only","status":"fail","detail":"format fingerprint 37013d550b55 is not known-good: review, then run `schrodeck doctor --accept-fingerprint`"}]}}
```

`internal/cli/testdata/golden/doctor-accepted.json`:

```json
{"schema_version":1,"command":"doctor","ok":true,"data":{"tier":"read-only","fingerprint":"37013d550b55c32c95c2df70bf7ce7e0bf990ff787a33d789a1fbd014c1bf448","accepted_now":true,"checks":[{"id":"M1","contract":"B","tier":"read-only","status":"pass","detail":"data root readable (1 entries)"},{"id":"M2","contract":"B","tier":"read-only","status":"pass","detail":"every deck's selected profile exists"},{"id":"M5","contract":"B","tier":"read-only","status":"pass","detail":"app 7.5.1"},{"id":"P1","contract":"C","tier":"read-only","status":"pass","detail":"1 profile(s) load"},{"id":"P2","contract":"C","tier":"read-only","status":"pass","detail":"every profile has Version 3.0"},{"id":"P3","contract":"C","tier":"read-only","status":"pass","detail":"every profile names its device"},{"id":"P6","contract":"C","tier":"read-only","status":"info","detail":"1 Open action(s) use an absolute path outside {{HOME}}","evidence":["AAAAAAAA-0000-4000-8000-000000000001.sdProfile › key 0,0: /Users/<user>/bin/demo.sh"]},{"id":"P7","contract":"C","tier":"read-only","status":"pass","detail":"every file is allow-listed"},{"id":"P8","contract":"C","tier":"read-only","status":"pass","detail":"no cross-profile or foreign-device references"},{"id":"P9+P11","contract":"C","tier":"read-only","status":"pass","detail":"copies hash equal under norm_version 1"},{"id":"FP","contract":"C","tier":"read-only","status":"pass","detail":"format fingerprint 37013d550b55 is known-good"}]}}
```

- [ ] **Step 5: Run to verify failure**

Run: `go test ./internal/cli/`
Expected: FAIL to compile: `unknown field Host in struct literal of type Env` (and the missing commands).

- [ ] **Step 6: Replace the router**

`internal/cli/cli.go` (full replacement):

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package cli implements schrodeck's command line. Every command can print a
// JSON document instead of text (--json, anywhere after the command name);
// that document is contract E (docs/contracts/cli-json.md) and is what any UI
// consumes. Logs never go to stdout.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/csmarshall/schrodeck/internal/host"
)

// Exit codes.
const (
	ExitOK    = 0 // success
	ExitFail  = 1 // the command ran and reports failure
	ExitUsage = 2 // the command line was wrong
)

// Env is everything a command may touch. Tests build one with buffers and
// fakes.
type Env struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
	Logger  *slog.Logger
	// Host is nil when this OS has no connector; commands that need it say so.
	Host *host.Host
}

// result is what a command produced.
type result struct {
	data   any    // the envelope's "data" member
	text   string // human-readable output
	failed bool   // ran, but reports failure: "ok": false and exit 1
}

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, env Env, args []string) (result, error)
}

// usageError marks a wrong command line (exit 2) as opposed to a failed run.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// errHelp is returned by parseFlags when -h was given.
var errHelp = errors.New("help requested")

// errNoHost is returned by commands that need a connector on an OS without one.
var errNoHost = errors.New("this command needs an OS connector, and there is none for this OS yet")

// Run executes one command line and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 {
		printUsage(env.Stderr)
		return ExitUsage
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage(env.Stderr)
		return ExitOK
	}
	cmd, ok := lookup(args[0])
	if !ok {
		fmt.Fprintf(env.Stderr, "schrodeck: unknown command %q\n\n", args[0])
		printUsage(env.Stderr)
		return ExitUsage
	}
	rest, asJSON := extractJSON(args[1:])

	logger := env.Logger.With("component", "cli", "command", cmd.name)
	logger.Debug("command started")
	res, err := cmd.run(ctx, env, rest)
	if errors.Is(err, errHelp) {
		return ExitOK
	}
	if err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(env.Stderr, "schrodeck %s: %v\n", cmd.name, err)
			return ExitUsage
		}
		logger.Error("command failed", "error", err)
		if asJSON {
			if werr := writeJSON(env.Stdout, envelope{SchemaVersion: SchemaVersion, Command: cmd.name, OK: false, Error: &errorBody{Message: err.Error()}}); werr != nil {
				logger.Error("writing JSON output failed", "error", werr)
			}
		} else {
			fmt.Fprintf(env.Stderr, "schrodeck %s: %v\n", cmd.name, err)
		}
		return ExitFail
	}

	if asJSON {
		if err := writeJSON(env.Stdout, envelope{SchemaVersion: SchemaVersion, Command: cmd.name, OK: !res.failed, Data: res.data}); err != nil {
			logger.Error("writing JSON output failed", "error", err)
			return ExitFail
		}
	} else {
		fmt.Fprint(env.Stdout, res.text)
	}
	logger.Debug("command finished", "ok", !res.failed)
	if res.failed {
		return ExitFail
	}
	return ExitOK
}

// extractJSON removes the --json flag from anywhere in args.
func extractJSON(args []string) ([]string, bool) {
	var rest []string
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json", "-json", "--json=true", "-json=true":
			asJSON = true
		default:
			rest = append(rest, a)
		}
	}
	return rest, asJSON
}

// parseFlags parses fs from args, allowing flags after positional
// arguments, and returns the positional arguments. Errors are usage errors.
func parseFlags(fs *flag.FlagSet, env Env, args []string) ([]string, error) {
	fs.SetOutput(env.Stderr)
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, errHelp
			}
			return nil, usageError{err.Error()}
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: schrodeck <command> [arguments] [--json]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
}

func lookup(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func commands() []command {
	return []command{
		{name: "version", summary: "print the schrodeck version", run: runVersion},
		{name: "status", summary: "show what schrodeck sees on this computer (read-only)", run: runStatus},
		{name: "inventory", summary: "list decks and profiles with their hashes (read-only)", run: runInventory},
		{name: "doctor", summary: "run read-only checks of the app and its file format", run: runDoctor},
		{name: "observe", summary: "observe what one action in the app changes on disk: start <name> | stop <name>", run: runObserve},
		{name: "fixture", summary: "export a redacted test fixture: export --profile <folder> --out <dir>", run: runFixture},
	}
}
```

- [ ] **Step 7: Replace the commands**

`internal/cli/commands.go` (full replacement):

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/normhash"
	"github.com/csmarshall/schrodeck/deckformat/probe"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/deckformat/redact"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/doctor"
	"github.com/csmarshall/schrodeck/internal/host"
	"github.com/csmarshall/schrodeck/internal/identity"
)

func noArgs(name string, fs *flag.FlagSet, env Env, args []string) error {
	rest, err := parseFlags(fs, env, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageError{fmt.Sprintf("%s takes no arguments, got %q", name, rest)}
	}
	return nil
}

func newFlags(name string) *flag.FlagSet {
	return flag.NewFlagSet("schrodeck "+name, flag.ContinueOnError)
}

// loadProfiles reads ProfilesV3 through a read-only fs.FS.
func loadProfiles(h *host.Host) (profile.LoadResult, error) {
	return profile.LoadAll(os.DirFS(h.Paths.ProfilesDir()))
}

// redactor builds the redactor for this host. Names under 3 characters are
// skipped (redact.New refuses them) and the generic rules still apply.
func redactor(env Env) (*redact.Redactor, error) {
	var users, hosts []string
	if env.Host != nil {
		if u := env.Host.Identity.UserName(); len(u) >= 3 {
			users = append(users, u)
		}
		for _, n := range env.Host.HostNames {
			if len(n) >= 3 {
				hosts = append(hosts, n)
			}
		}
	}
	var serials []string
	for _, s := range redact.SerialsFrom(deviceIDs(env)...) {
		if len(s) >= 3 {
			serials = append(serials, s)
		}
	}
	return redact.New(redact.Options{UserNames: users, HostNames: hosts, Serials: serials})
}

// deviceIDs lists every device id this host knows of: the prefs device keys
// and each profile's Device.UUID. Their serials are redacted wherever they
// appear, including outside a device id (a plugin setting, a prefs field).
func deviceIDs(env Env) []string {
	if env.Host == nil {
		return nil
	}
	var ids []string
	if recs, err := env.Host.Prefs.DeviceRecords(); err == nil {
		for _, r := range recs {
			if k, ok := r[decks.RecordKey].(string); ok {
				ids = append(ids, k)
			}
		}
	}
	if res, err := profile.LoadAll(os.DirFS(env.Host.Paths.ProfilesDir())); err == nil {
		for _, p := range res.Profiles {
			ids = append(ids, p.DeviceUUID())
		}
	}
	return ids
}

type versionData struct {
	SchrodeckVersion string `json:"schrodeck_version"`
}

func runVersion(_ context.Context, env Env, args []string) (result, error) {
	if err := noArgs("version", newFlags("version"), env, args); err != nil {
		return result{}, err
	}
	return result{data: versionData{SchrodeckVersion: env.Version}, text: fmt.Sprintf("schrodeck %s\n", env.Version)}, nil
}

type appStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
}

type hostStatus struct {
	HostID   string    `json:"host_id"`
	App      appStatus `json:"app"`
	Decks    int       `json:"decks"`
	Profiles int       `json:"profiles"`
}

type statusData struct {
	SchrodeckVersion string      `json:"schrodeck_version"`
	Host             *hostStatus `json:"host,omitempty"`
}

func runStatus(_ context.Context, env Env, args []string) (result, error) {
	if err := noArgs("status", newFlags("status"), env, args); err != nil {
		return result{}, err
	}
	d := statusData{SchrodeckVersion: env.Version}
	var text strings.Builder
	fmt.Fprintf(&text, "schrodeck %s\n", env.Version)
	if h := env.Host; h != nil {
		hs, err := hostSummary(h)
		if err != nil {
			return result{}, err
		}
		d.Host = &hs
		fmt.Fprintf(&text, "host %s · Stream Deck app %s (installed %v, running %v) · %d deck(s) · %d profile(s)\n",
			hs.HostID, orUnknown(hs.App.Version), hs.App.Installed, hs.App.Running, hs.Decks, hs.Profiles)
	}
	text.WriteString("No setups yet: syncing arrives in a later milestone.\n")
	return result{data: d, text: text.String()}, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func hostSummary(h *host.Host) (hostStatus, error) {
	id, err := identity.HostID(h.Identity)
	if err != nil {
		return hostStatus{}, err
	}
	hs := hostStatus{HostID: id}
	if hs.App.Installed, err = h.App.Installed(); err != nil {
		return hostStatus{}, err
	}
	if hs.App.Running, err = h.App.Running(); err != nil {
		return hostStatus{}, err
	}
	if v, err := h.Prefs.AppVersion(); err == nil {
		hs.App.Version = v
	}
	if ds, err := h.Decks.Decks(); err == nil {
		hs.Decks = len(ds)
	}
	if res, err := loadProfiles(h); err == nil {
		hs.Profiles = len(res.Profiles)
	}
	return hs, nil
}

type deckInfo struct {
	Key         string `json:"key"`
	Model       string `json:"model,omitempty"`
	Columns     int    `json:"columns,omitempty"`
	Rows        int    `json:"rows,omitempty"`
	Dials       int    `json:"dials,omitempty"`
	Virtual     bool   `json:"virtual"`
	Destination bool   `json:"destination"`
	Why         string `json:"why,omitempty"`
}

type profileInfo struct {
	Folder        string `json:"folder"`
	Name          string `json:"name"`
	Device        string `json:"device"`
	Pages         int    `json:"pages"`
	Hash          string `json:"hash,omitempty"`
	HashError     string `json:"hash_error,omitempty"`
	AppIdentifier string `json:"app_identifier,omitempty"`
}

type inventoryData struct {
	HostID      string        `json:"host_id"`
	AppVersion  string        `json:"app_version,omitempty"`
	NormVersion int           `json:"norm_version"`
	Fingerprint string        `json:"fingerprint,omitempty"`
	Decks       []deckInfo    `json:"decks"`
	Profiles    []profileInfo `json:"profiles"`
	LoadErrors  []string      `json:"load_errors,omitempty"`
	Unmatched   *unmatched    `json:"unmatched,omitempty"`
}

// unmatched is what the deck list cannot tie together (the U9 observation):
// profiles bound to no device key, and device keys no profile is bound to.
type unmatched struct {
	Profiles []string `json:"profiles,omitempty"`
	Decks    []string `json:"decks,omitempty"`
}

func runInventory(_ context.Context, env Env, args []string) (result, error) {
	fs := newFlags("inventory")
	showIDs := fs.Bool("show-ids", false, "print device ids unredacted (they contain deck serials)")
	if err := noArgs("inventory", fs, env, args); err != nil {
		return result{}, err
	}
	h := env.Host
	if h == nil {
		return result{}, errNoHost
	}
	r, err := redactor(env)
	if err != nil {
		return result{}, err
	}
	show := func(s string) string {
		if *showIDs {
			return s
		}
		return r.String(s)
	}
	id, err := identity.HostID(h.Identity)
	if err != nil {
		return result{}, err
	}
	d := inventoryData{HostID: id, NormVersion: normhash.NormVersion, Decks: []deckInfo{}, Profiles: []profileInfo{}}
	d.AppVersion, _ = h.Prefs.AppVersion()

	ds, err := h.Decks.Decks()
	if err != nil {
		return result{}, err
	}
	for _, s := range decks.Annotate(ds) {
		di := deckInfo{Key: show(s.AppDeviceID), Model: s.Model, Columns: s.Geometry.Columns, Rows: s.Geometry.Rows, Dials: s.Geometry.Dials, Virtual: s.Virtual, Destination: s.Destination()}
		switch {
		case !s.KeyUnique:
			di.Why = "another deck on this computer has the same key"
		case !s.GeometryKnown:
			di.Why = "geometry not verified for this model yet"
		}
		d.Decks = append(d.Decks, di)
	}

	res, err := loadProfiles(h)
	if err != nil {
		return result{}, err
	}
	for _, e := range res.Errors {
		d.LoadErrors = append(d.LoadErrors, show(e.Error()))
	}
	sort.Slice(res.Profiles, func(i, j int) bool { return res.Profiles[i].Folder < res.Profiles[j].Folder })
	for _, p := range res.Profiles {
		pi := profileInfo{Folder: p.Folder, Name: show(p.Name()), Device: show(p.DeviceUUID()), Pages: len(p.Pages)}
		if hsh, err := normhash.Hash(p); err == nil {
			pi.Hash = hsh
		} else {
			pi.HashError = show(err.Error())
		}
		pi.AppIdentifier, _ = p.AppIdentifier()
		d.Profiles = append(d.Profiles, pi)
	}
	if len(res.Profiles) > 0 {
		if schema, err := profile.SchemaOf(res.Profiles); err == nil {
			d.Fingerprint, _ = schema.Digest()
		}
	}
	if recs, err := h.Prefs.DeviceRecords(); err == nil {
		profs, keys := decks.Unmatched(recs, res.Profiles)
		for i := range keys {
			keys[i] = show(keys[i])
		}
		if len(profs) > 0 || len(keys) > 0 {
			d.Unmatched = &unmatched{Profiles: profs, Decks: keys}
		}
	}

	var text strings.Builder
	fmt.Fprintf(&text, "host %s · app %s · norm_version %d · fingerprint %s\n\ndecks:\n", d.HostID, orUnknown(d.AppVersion), d.NormVersion, short(d.Fingerprint))
	for _, di := range d.Decks {
		grid := "?"
		if di.Columns > 0 {
			grid = fmt.Sprintf("%dx%d", di.Columns, di.Rows)
		}
		fmt.Fprintf(&text, "  %-28s %-10s %-5s destination=%v %s\n", di.Key, di.Model, grid, di.Destination, di.Why)
	}
	text.WriteString("\nprofiles:\n")
	for _, pi := range d.Profiles {
		fmt.Fprintf(&text, "  %-48s %-24q pages=%d hash=%s %s\n", pi.Folder, pi.Name, pi.Pages, short(pi.Hash), pi.HashError)
	}
	for _, e := range d.LoadErrors {
		fmt.Fprintf(&text, "  NOT LOADED: %s\n", e)
	}
	if u := d.Unmatched; u != nil {
		for _, f := range u.Profiles {
			fmt.Fprintf(&text, "  UNMATCHED profile (its Device.UUID is no device key): %s\n", f)
		}
		for _, k := range u.Decks {
			fmt.Fprintf(&text, "  UNMATCHED deck (no profile is bound to it): %s\n", k)
		}
	}
	return result{data: d, text: text.String(), failed: len(d.LoadErrors) > 0}, nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "-"
	}
	return s
}

type doctorData struct {
	Tier        string `json:"tier"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Accepted    bool   `json:"accepted_now,omitempty"`
	// AcceptRefused says why --accept-fingerprint did not record anything.
	AcceptRefused string         `json:"accept_refused,omitempty"`
	Checks        []probe.Result `json:"checks"`
}

func runDoctor(ctx context.Context, env Env, args []string) (result, error) {
	fs := newFlags("doctor")
	accept := fs.Bool("accept-fingerprint", false, "record the current format fingerprint as known-good for this host (ADR 0015)")
	if err := noArgs("doctor", fs, env, args); err != nil {
		return result{}, err
	}
	h := env.Host
	if h == nil {
		return result{}, errNoHost
	}
	r, err := redactor(env)
	if err != nil {
		return result{}, err
	}
	res, err := loadProfiles(h)
	if err != nil {
		res = profile.LoadResult{Errors: []error{err}}
	}
	probes := append(doctor.ContractB(h, res), probe.ContractC(probe.Install{Load: res, Home: h.Paths.Home()})...)

	d := doctorData{Tier: probe.ReadOnly.String()}
	// Every other check runs first: ADR 0015's "the user confirms the new
	// fingerprint as known-good" is only offered on an install that otherwise
	// passes, and with every profile loaded (a profile that failed to load is
	// missing from the fingerprint, so accepting would bless a partial view).
	d.Checks = probe.RunAll(ctx, probes, probe.ReadOnly)
	if len(res.Profiles) > 0 {
		schema, err := profile.SchemaOf(res.Profiles)
		if err != nil {
			return result{}, err
		}
		digest, err := schema.Digest()
		if err != nil {
			return result{}, err
		}
		d.Fingerprint = digest
		known, err := doctor.LoadKnown(h.Paths.StateDir())
		if err != nil {
			return result{}, err
		}
		if *accept && !known.Contains(digest) {
			switch {
			case len(res.Errors) > 0:
				d.AcceptRefused = fmt.Sprintf("%d profile(s) did not load; fix them first, the fingerprint would leave them out", len(res.Errors))
			case probe.Failed(d.Checks):
				d.AcceptRefused = "another check failed; a fingerprint is accepted only when every other check passes"
			default:
				version, _ := h.Prefs.AppVersion()
				known.Accepted = append(known.Accepted, doctor.Accepted{Digest: digest, AppVersion: version, AcceptedAt: now(h), Schema: schema})
				if err := doctor.SaveKnown(h.Paths.StateDir(), known); err != nil {
					return result{}, err
				}
				d.Accepted = true
			}
		}
		d.Checks = append(d.Checks, probe.RunAll(ctx, []probe.Probe{doctor.Fingerprint(schema, digest, known)}, probe.ReadOnly)...)
	} else if *accept {
		d.AcceptRefused = "no profile loaded, so there is no fingerprint to accept"
	}

	var text strings.Builder
	for i := range d.Checks {
		c := &d.Checks[i]
		c.Detail = r.String(c.Detail)
		for j := range c.Evidence {
			c.Evidence[j] = r.String(c.Evidence[j])
		}
		fmt.Fprintf(&text, "%-4s %-6s %s %s\n", c.Status, c.ID, c.Contract, c.Detail)
		for _, e := range c.Evidence {
			fmt.Fprintf(&text, "            %s\n", e)
		}
	}
	if d.Accepted {
		fmt.Fprintf(&text, "Recorded fingerprint %s as known-good for this host.\n", short(d.Fingerprint))
	}
	if d.AcceptRefused != "" {
		fmt.Fprintf(&text, "Fingerprint NOT accepted: %s.\n", d.AcceptRefused)
	}
	return result{data: d, text: text.String(), failed: probe.Failed(d.Checks)}, nil
}

func now(h *host.Host) time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}
```

- [ ] **Step 8: Add `observe` and `fixture`**

`internal/cli/observe.go`:

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/observe"
	"github.com/csmarshall/schrodeck/deckformat/pathguard"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/deckformat/redact"
)

// Settling: an observation snapshot is taken only once two reads this far
// apart are identical. This is a human-scale heuristic for "the app has
// finished writing after the click", not a measured app property (contract C
// P4 measures the app's own settle window in M3); it is a named setting
// because it cannot be derived.
const (
	settleInterval = 2 * time.Second
	settleTries    = 15
)

var observationName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func observeDir(env Env, name string) string {
	return filepath.Join(env.Host.Paths.StateDir(), "observe", name)
}

// snapshot takes a settled snapshot of the app's profiles and device records.
func snapshot(env Env) (*observe.Snapshot, error) {
	h := env.Host
	take := func() (*observe.Snapshot, error) {
		var prefs *jsondoc.Value
		if recs, err := h.Prefs.DeviceRecords(); err == nil {
			list := make([]any, 0, len(recs))
			for _, r := range recs {
				list = append(list, r)
			}
			if prefs, err = jsondoc.FromAny(map[string]any{"Devices": list}); err != nil {
				return nil, err
			}
		}
		version, _ := h.Prefs.AppVersion()
		return observe.Take(os.DirFS(h.Paths.ProfilesDir()), prefs, version, now(h))
	}
	sleep := h.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	return observe.Settle(take, func() { sleep(settleInterval) }, settleTries)
}

// refuseAppData refuses a write target inside the Stream Deck app's data
// (M1 never writes there). Both the app data root and the profiles directory
// are protected, in case a connector places them apart.
func refuseAppData(env Env, target string) error {
	if err := pathguard.RefuseInside(target, env.Host.Paths.AppDataRoot(), env.Host.Paths.ProfilesDir()); err != nil {
		return usageError{err.Error()}
	}
	return nil
}

type observeData struct {
	Name     string          `json:"name"`
	Snapshot string          `json:"snapshot,omitempty"`
	Report   *observe.Report `json:"report,omitempty"`
	Written  string          `json:"written,omitempty"`
}

func runObserve(_ context.Context, env Env, args []string) (result, error) {
	fs := newFlags("observe")
	out := fs.String("out", "", "stop: also write the Markdown report to this file")
	rest, err := parseFlags(fs, env, args)
	if err != nil {
		return result{}, err
	}
	if len(rest) != 2 || (rest[0] != "start" && rest[0] != "stop") {
		return result{}, usageError{"usage: schrodeck observe start|stop <name> [--out FILE]"}
	}
	verb, name := rest[0], rest[1]
	if !observationName.MatchString(name) {
		return result{}, usageError{"observation names are lower-case letters, digits and dashes, e.g. u2-new-profile"}
	}
	if env.Host == nil {
		return result{}, errNoHost
	}
	if *out != "" {
		// Checked before anything is read, so a bad --out costs nothing.
		if err := refuseAppData(env, *out); err != nil {
			return result{}, err
		}
	}
	dir := observeDir(env, name)

	if verb == "start" {
		if _, err := os.Stat(dir); err == nil {
			return result{}, fmt.Errorf("observation %q is already started; run `schrodeck observe stop %s` first", name, name)
		}
		s, err := snapshot(env)
		if err != nil {
			return result{}, err
		}
		if err := s.Save(filepath.Join(dir, "before")); err != nil {
			return result{}, err
		}
		text := fmt.Sprintf("Snapshot taken (%d profiles). Do the one thing you want to observe in the Stream Deck app, wait a few seconds, then run:\n  schrodeck observe stop %s --out docs/observations/%s.md\n", len(s.Profiles), name, name)
		return result{data: observeData{Name: name, Snapshot: "taken"}, text: text}, nil
	}

	before, err := observe.Load(filepath.Join(dir, "before"))
	if err != nil {
		return result{}, fmt.Errorf("no started observation %q: %w", name, err)
	}
	after, err := snapshot(env)
	if err != nil {
		return result{}, err
	}
	r, err := redactor(env)
	if err != nil {
		return result{}, err
	}
	rep, err := observe.Compare(name, before, after, r)
	if err != nil {
		return result{}, err
	}
	md := rep.Markdown()
	d := observeData{Name: name, Report: &rep}
	if *out != "" {
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return result{}, err
		}
		if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
			return result{}, err
		}
		d.Written = *out
	}
	// The snapshots hold unredacted data; they are deleted once reported.
	if err := os.RemoveAll(dir); err != nil {
		return result{}, err
	}
	return result{data: d, text: md}, nil
}

type fixtureData struct {
	Folder  string   `json:"folder"`
	Out     string   `json:"out"`
	Strings []string `json:"strings"`
}

func runFixture(_ context.Context, env Env, args []string) (result, error) {
	fs := newFlags("fixture")
	src := fs.String("profile", "", "folder name of the profile to export, e.g. ABCD….sdProfile")
	out := fs.String("out", "", "empty directory to write the redacted fixture into")
	name := fs.String("name", "", "folder name for the exported profile (default: the source folder name)")
	rest, err := parseFlags(fs, env, args)
	if err != nil {
		return result{}, err
	}
	if len(rest) != 1 || rest[0] != "export" || *src == "" || *out == "" {
		return result{}, usageError{"usage: schrodeck fixture export --profile <folder> --out <dir> [--name <folder>]"}
	}
	if env.Host == nil {
		return result{}, errNoHost
	}
	folder := *name
	if folder == "" {
		folder = *src
	}
	if pathguard.SingleName(folder) != nil || !strings.HasSuffix(folder, profile.Suffix) {
		return result{}, usageError{"--name must be a single folder name ending in " + profile.Suffix}
	}
	if err := refuseAppData(env, filepath.Join(*out, folder)); err != nil {
		return result{}, err
	}
	root := env.Host.Paths.ProfilesDir()
	p, err := profile.Load(os.DirFS(root), *src)
	if err != nil {
		return result{}, err
	}
	r, err := redactor(env)
	if err != nil {
		return result{}, err
	}
	if err := redact.ExportFixture(p, r, env.Host.Paths.AppDataRoot(), *out, folder); err != nil {
		if errors.Is(err, redact.ErrOutputExists) {
			return result{}, usageError{err.Error()}
		}
		return result{}, err
	}
	exported, err := profile.Load(os.DirFS(*out), folder)
	if err != nil {
		return result{}, fmt.Errorf("the exported fixture does not load: %w", err)
	}
	strs := redact.Strings(exported)
	var text strings.Builder
	fmt.Fprintf(&text, "Wrote %s. Review every string below before committing it; the leak scan is a second line of defense, not the first:\n", filepath.Join(*out, folder))
	for _, s := range strs {
		fmt.Fprintf(&text, "  %q\n", s)
	}
	return result{data: fixtureData{Folder: folder, Out: *out, Strings: strs}, text: text.String()}, nil
}
```

- [ ] **Step 9: Wire the connector into `main`**

`cmd/schrodeck/main.go` (full replacement):

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command schrodeck keeps Stream Deck setups identical across computers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/csmarshall/schrodeck/internal/cli"
	"github.com/csmarshall/schrodeck/internal/connector"
	"github.com/csmarshall/schrodeck/internal/logging"
	"github.com/csmarshall/schrodeck/internal/version"
)

func main() {
	logger, err := logging.New(os.Stderr, os.Getenv(logging.EnvLevel))
	if err != nil {
		fmt.Fprintln(os.Stderr, "schrodeck:", err)
		os.Exit(cli.ExitUsage)
	}
	h, err := connector.New()
	if err != nil {
		logger.Debug("no OS connector", "component", "main", "error", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], cli.Env{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version.Version,
		Logger:  logger,
		Host:    h,
	})
	stop()
	os.Exit(code)
}
```

- [ ] **Step 10: Update contract E**

In `docs/contracts/cli-json.md`, add to § Rules: `- Enumerations (for example a check's \`status\`) may gain values without a version bump; a UI must treat an unknown value as "not pass".` Then replace the § Commands table with:

```markdown
| command | `data` |
|---|---|
| `version` | `{schrodeck_version}` |
| `status` | `{schrodeck_version, host?: {host_id, app: {installed, running, version?}, decks, profiles}}`; `host` is absent on an OS without a connector |
| `inventory` | `{host_id, app_version?, norm_version, fingerprint?, decks: [{key, model?, columns?, rows?, dials?, virtual, destination, why?}], profiles: [{folder, name, device, pages, hash?, hash_error?, app_identifier?}], load_errors?, unmatched?: {profiles?, decks?}}`; device ids are redacted unless `--show-ids` |
| `doctor` | `{tier, fingerprint?, accepted_now?, accept_refused?, checks: [{id, contract, tier, status, detail?, evidence?}]}`, `status` ∈ `pass` \| `fail` \| `skip` \| `info`; `ok` is false when any check fails; `accept_refused` says why `--accept-fingerprint` recorded nothing |
| `observe` | `{name, snapshot?: "taken", report?: {name, before, after, app_version, changes: [{profile, where, path, kind, before?, after?}]}, written?}` |
| `fixture` | `{folder, out, strings}` (`strings`: every remaining string, for human review) |
```

- [ ] **Step 11: Run the whole suite and every CI check locally**

Run:

```bash
go mod tidy && (cd deckformat && go mod tidy) \
 && tools/ci/selftest.sh && git add go.mod go.sum cmd internal docs/contracts/cli-json.md && tools/ci/check-headers.sh && tools/ci/check-gofmt.sh && tools/ci/leak-scan.sh \
 && go vet ./... && go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./... \
 && (cd deckformat && go vet ./... && go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...) \
 && go run ./tools/archcheck/cmd/archcheck -mode core -dir . \
 && go run ./tools/archcheck/cmd/archcheck -mode boundary -dir deckformat -forbid . \
 && go run ./tools/portcheck/cmd/portcheck \
 && for g in darwin linux windows; do GOOS=$g go build ./...; done \
 && go test -race ./... && (cd deckformat && go test -race ./...)
```

Expected: every line ok, exit 0. Then run the manual pre-push leak scan from the global rules; it must print nothing.

- [ ] **Step 12: Try it read-only on the Mac** (no state is written without `--accept-fingerprint`)

```bash
go build -o /tmp/schrodeck-m1 ./cmd/schrodeck
/tmp/schrodeck-m1 status
/tmp/schrodeck-m1 inventory
/tmp/schrodeck-m1 doctor; echo "exit $?"
```

Expected: `status` shows the host id, the app version and the deck and profile counts; `inventory` lists every deck with `destination=false` ("geometry not verified for this model yet", until Task 12), every profile with a hash and no `NOT LOADED` line, and an `UNMATCHED deck` line for any deck no profile is bound to; `doctor` passes M1, M2 (or `info` for a virtual deck, U4), M5, P1–P9+P11 and fails only `FP` (not yet accepted), exit 1. This was run on the development Mac on 2026-10-02 with exactly that result. Do not accept the fingerprint yet: Task 12 does that deliberately, first.

- [ ] **Step 13: Commit, push, PR, merge** (as in Task 3 step 6)

```bash
git add go.mod go.sum cmd internal docs/contracts/cli-json.md
git status --short   # nothing else may be listed as staged; untracked *.log files are ignored
git commit -m "feat(#<n>): read-only doctor, inventory, status, observe and fixture export on macOS"
```

---

### Task 12: Settle the format unknowns on a real Mac

Issue: "Settle format unknowns on a real Mac (U1–U10, P8, P10, P11, F1, geometry; issues #1, #2)" (new worktree `<n>-observations`). This task is a set of manual observation procedures run by the owner on the development Mac, followed by documentation updates in one PR. Each observation is: start a snapshot, do exactly one thing in the Stream Deck app, stop, read the redacted, pseudonymized report. schrodeck never writes the app's files; every change on disk is made by the app itself, through its normal UI.

Unknowns are those of [docs/streamdeck-config-model.md](../../streamdeck-config-model.md) § Unknowns (U1–U6, P9 there = P11 here) and [contract C](../../contracts/profile-format.md) (P8, P10, P11), plus four found while writing this plan: **U7** (the non-record entry in prefs `Devices`), **U8** (where a virtual deck's grid size is stored), **U9** (whether `AppDeviceID` always equals `ManifestDeviceID`, contract A says unverified), **U10** (whether multi-action children carry their own `ActionID`s, which the strip list does not reach), and **F1** (whether the ADR 0015 fingerprint stays put under ordinary edits). Two open issues are observations of the same kind and are settled here too: **#2** (can a deck's profile be edited while the deck is not attached?) and, when the second Mac is available, **#1** (is `Device.UUID` identical for the same deck on every Mac?).

**Files:**
- Create: `tools/observe-m1.sh` (the procedure runner), `docs/observations/<name>.md` (one per observation, redacted reports)
- Modify: `internal/decks/decks.go` (`ProductTypes` rows and `DeviceTypes` grid splits), `internal/decks/decks_test.go`, `docs/references.md` (new rows R22 onward), `docs/contracts/profile-format.md` (P8, P10, P11 rows), `docs/contracts/client-os.md` (verified-versions row), `docs/streamdeck-config-model.md` (unknowns table), and `docs/contracts/os-connector.md` (U9 sentence) as the findings dictate

**Safety rules for every step:**
- **Throwaway profiles only.** Before the first observation, make an Elgato backup in the app (Preferences → Backup). Every edit below happens on a profile created for this task and named `schrodeck-m1-scratch` (one per deck that is observed; U2 creates the first one), never on a profile in real use. Step 12 deletes them.
- **Logs never enter the repository.** `tools/observe-m1.sh` tees every command to a timestamped log in `~/work/claude/schrodeck-logs/` (it refuses a log directory inside the repository), `*.log` is in `.gitignore` (M0 Task 1), and every `git add` below names its paths.
- `inventory` is only ever run without `--show-ids`: the vendor and product numbers this task needs are kept in the redacted key (`@(1)[4057/143/<deck>]`).

- [ ] **Step 1: Write the procedure runner and build the binary**

`tools/observe-m1.sh`:

```bash
#!/usr/bin/env bash
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Runs the M1 observations (docs/superpowers/plans/2026-10-02-m1-format-toolkit.md,
# Task 12) against this Mac's real Stream Deck app. schrodeck only reads: every
# change on disk is made by the app, through its UI, by the person running this.
# Every command's output is teed to a timestamped log OUTSIDE the repository
# ($SCHRODECK_LOG_DIR, default ~/work/claude/schrodeck-logs): logs from a real
# machine hold profile names, host ids and paths, and must never be committed.
#
# Run with no arguments for usage.
unset TMOUT
set -euo pipefail

usage() {
	cat >&2 <<'EOF'
usage: tools/observe-m1.sh observe <name> "<the one thing to do in the app>"
       tools/observe-m1.sh baseline           doctor, then doctor --accept-fingerprint
       tools/observe-m1.sh doctor <label>     doctor, logged
       tools/observe-m1.sh inventory <label>  inventory (device ids redacted), logged
env:   SCHRODECK_BIN (default /tmp/schrodeck-m1), SCHRODECK_LOG_DIR
EOF
	exit 2
}

bin=${SCHRODECK_BIN:-/tmp/schrodeck-m1}
top=$(git rev-parse --show-toplevel 2>/dev/null) || {
	echo "observe-m1: run this from inside the schrodeck worktree" >&2
	exit 2
}
repo=$(cd "$top" && pwd -P)
mkdir -p "${SCHRODECK_LOG_DIR:-$HOME/work/claude/schrodeck-logs}"
# Resolved through symlinks, so /tmp vs /private/tmp cannot hide a log dir
# that is really inside the repository.
logdir=$(cd "${SCHRODECK_LOG_DIR:-$HOME/work/claude/schrodeck-logs}" && pwd -P)
case "$logdir" in
"$repo" | "$repo"/*)
	echo "observe-m1: the log directory $logdir is inside the repository; set SCHRODECK_LOG_DIR outside it" >&2
	exit 2
	;;
esac
[[ -x "$bin" ]] || {
	echo "observe-m1: $bin not found; build it with: go build -o $bin ./cmd/schrodeck" >&2
	exit 2
}

# run <log-label> <command...>: runs the command, tees its output to a fresh
# log, prints and returns its exit status (a failing doctor is often expected).
run() {
	local label=$1
	shift
	local file
	file="$logdir/${label}_$(date +"%F-%H%M.%S").log"
	set +e
	"$@" 2>&1 | tee "$file"
	local status=${PIPESTATUS[0]}
	set -e
	echo "[exit $status; log: $file]"
	return 0
}

[[ $# -ge 1 ]] || usage
case "$1" in
observe)
	[[ $# -eq 3 ]] || usage
	name=$2 what=$3
	mkdir -p "$repo/docs/observations"
	run "observe-start-$name" "$bin" observe start "$name"
	echo
	echo ">>> In the Stream Deck app, on a THROWAWAY profile only: $what"
	echo ">>> Do exactly that one thing, wait a few seconds, then press Enter."
	read -r _
	run "observe-stop-$name" "$bin" observe stop "$name" --out "$repo/docs/observations/$name.md"
	;;
baseline)
	[[ $# -eq 1 ]] || usage
	run doctor-baseline "$bin" doctor
	run doctor-accept "$bin" doctor --accept-fingerprint
	;;
doctor | inventory)
	[[ $# -eq 2 ]] || usage
	run "$1-$2" "$bin" "$1"
	;;
*)
	usage
	;;
esac
```

```bash
cd ~/work/claude/schrodeck-worktrees/<n>-observations
chmod +x tools/observe-m1.sh && tools/ci/check-headers.sh tools/observe-m1.sh
go build -o /tmp/schrodeck-m1 ./cmd/schrodeck
```

Check it refuses a log directory inside the repository (known-bad): `SCHRODECK_LOG_DIR=$PWD/logs tools/observe-m1.sh doctor x; echo "exit $?"` must print the refusal and `exit 2`.

- [ ] **Step 2: Baseline and fingerprint (F1, part 1)**

```bash
tools/observe-m1.sh baseline
```

The first `doctor` must fail only `FP`; the second records the fingerprint (it is refused, and says why, if any other check fails: fix that first). Record the accepted fingerprint (first 12 characters) and the app version for contract B's verified-versions row: `| macOS 27 | 7.5.1 | 3.0 | <fingerprint> | M1, M2, M5, P1–P3, P6–P9, P11 (doctor read-only tier); P4/M3/M4 not yet run | <date> |`.

- [ ] **Step 3: Geometry (USB product → DeviceType, and each type's grid split)**

ADR 0003 takes geometry from Elgato's DeviceType table (R8), which documents each type's key and dial counts; `DeviceTypes` in `internal/decks/decks.go` already carries them. Two facts are missing, and this step observes both for every physical deck on this Mac:

1. Run `tools/observe-m1.sh inventory geometry`. For each deck, note the vendor/product numbers in its (redacted) key and the deck's model name as the app shows it in its device list.
2. Match the model name to R8's DeviceType (e.g. "Stream Deck XL" → 2). Add a row to `ProductTypes`, e.g. `{4057, 143}: 2, // R22`.
3. In the app's deck view, read the key grid (columns × rows) for that type and add it to the type's `DeviceTypes` row, e.g. `2: {Name: "Stream Deck XL", Keys: 32, Columns: 8, Rows: 4}, // grid: R22`. `TestGeometryTablesAreConsistent` then checks it against R8's documented key count: a misread grid that does not multiply to it fails the test, so the observation is cross-checked against the documentation rather than trusted alone.

Add `docs/references.md` row R22: `| R22 | USB product → DeviceType and grid split | **Observed** | the app's device list and deck view, app 7.5.1 | product <p> = DeviceType <t> (<name>), grid <c>×<r> (R8 documents <keys> keys); … Product ids are model ids, not serials. |`. Re-run `tools/observe-m1.sh inventory geometry`: those decks now show `destination=true`. Run `go test ./internal/decks/`.

- [ ] **Step 4: U2, what the empty `Pages.Default` page is**

```bash
tools/observe-m1.sh observe u2-new-profile "create a new, empty profile on one deck and name it schrodeck-m1-scratch"
tools/observe-m1.sh observe u2-reorder "on schrodeck-m1-scratch add a second page, then swap the order of the two pages"
```

Read both reports. Answer: does a new profile get a `Pages.Default` page with zero actions; does reordering change `Pages.Default` or only `Pages.Pages`? (Page names in the report are pseudonyms by position before the change, so a swap reads as `[profile-N/page/1, profile-N/page/0]`.)

- [ ] **Step 5: U3, smart profiles**

`tools/observe-m1.sh observe u3-smart "link schrodeck-m1-scratch to one application (profile settings → smart profile)"`. Answer: what `AppIdentifier` holds for a linked app (bundle id? path?), and whether `"*"` means "any application".

- [ ] **Step 6: U4, U5 and U8, virtual decks**

`tools/observe-m1.sh observe u4-virtual-open "open the virtual deck's window once"`. Answer: does the selected profile's folder appear (lazy creation)?

`tools/observe-m1.sh observe u5-second-virtual "create a second virtual deck"`. Answer: from the `prefs` rows, do two virtual decks share `@(0)[]` or get distinct keys? U8: does the report show where the new virtual deck's grid size is stored? (If it is in no file this harness reads, record "not in prefs Devices or ProfilesV3"; virtual decks then stay "geometry not verified" and a follow-up issue records what would settle it.) Delete the second virtual deck afterwards.

- [ ] **Step 7: P8 and P11, folders and profile switches**

`tools/observe-m1.sh observe p8-folder "on schrodeck-m1-scratch, create a Folder button and put one button inside it"`. Answer P11: how the folder button refers to its sub-page (a page UUID in its settings? the report shows it as a `profile-N/sub-page-1` pseudonym if so), and whether the sub-page appears in `Pages.Pages`.

`tools/observe-m1.sh observe p8-switch "on schrodeck-m1-scratch, add a Switch Profile action pointing at another profile"`. Answer P8: is the target referenced by its folder UUID (the report shows the target's `profile-N` pseudonym), and is a device id embedded? Confirm with `tools/observe-m1.sh doctor p8`: its P8 line should now report the reference.

- [ ] **Step 8: U10 and F1, ordinary edits and the fingerprint**

For each edit, run the observation, then `tools/observe-m1.sh doctor f1-<edit>`, and note whether `FP` still passes (and which key paths it names if not):

```bash
tools/observe-m1.sh observe f1-title-colour "on schrodeck-m1-scratch, set one button's title colour"
tools/observe-m1.sh doctor f1-title-colour
tools/observe-m1.sh observe f1-font-size "on schrodeck-m1-scratch, change one title's font size"
tools/observe-m1.sh doctor f1-font-size
tools/observe-m1.sh observe f1-multi-action "on schrodeck-m1-scratch, add a Multi Action with two steps"
tools/observe-m1.sh doctor f1-multi-action
tools/observe-m1.sh doctor f1-smart-link
```

The multi-action report also answers U10: do the steps carry their own `ActionID`s inside `Settings` (they show as `uuid-N` pseudonyms)? `f1-smart-link` needs no new edit: the U3 link already exists.

If `FP` fails on any ordinary edit, F1 is confirmed: stop and raise it with the owner, because ADR 0015's equality digest would pause sync on normal use. The likely fix (an ADR 0015 revision: trip only on key paths *outside* the accepted union rather than on any digest change) is a design decision, not part of this task.

- [ ] **Step 9: Issue #2, editing a deck that is not attached**

Disconnect the deck that holds `schrodeck-m1-scratch` (unplug it, or flip the Thunderbolt switch to the other Mac), then:

`tools/observe-m1.sh observe issue2-offline-edit "with the deck disconnected, select it in the app if it is still listed, and change one button title on schrodeck-m1-scratch; if the app does not allow it, do nothing"`

Reconnect the deck. Answer #2: did the app allow the edit, and did `ProfilesV3` change? Record the answer as a references row and comment it on issue #2 (`gh issue comment 2`), then close #2 from the PR (`Closes #2` in its body) if it is settled.

- [ ] **Step 10: U1, a bigger layout onto a smaller deck (optional)**

Only if a smaller deck is attached: create a second throwaway profile `schrodeck-m1-scratch` on it (outside an observation), then `tools/observe-m1.sh observe u1-xl-to-small "copy an XL page from schrodeck-m1-scratch that has buttons beyond column 5 and paste it into the smaller deck's schrodeck-m1-scratch"`. Answer: are out-of-range keys dropped, kept off-grid, or refused?

- [ ] **Step 11: U7, U9, P10 and issue #1 from what is already on disk**

- U7: in `docs/observations/u5-second-virtual.md` (or any report with `prefs` rows), find the `_raw` entry and record its key shape (redacted) and value type. It needs no new observation.
- U9: run `tools/observe-m1.sh inventory u9`. Its `UNMATCHED` lines (JSON: `unmatched`) list every profile whose `Device.UUID` equals no prefs device key and every device key no profile is bound to; count them. If there are none, record "equal for N of N decks on this Mac" in contract A's `DeviceEnumerator` invariants and as a references row; otherwise record what differs (redacted) and keep U9 open. A deck that simply has no profile yet is listed too: say so rather than counting it as a mismatch.
- P10 (two profiles sharing `ActionID`s): the app never creates duplicates through its UI (R20: copies get new ids), and testing tolerance would require hand-editing the app's files, which M1 does not do. Record P10 as "not tested in M1; informational only (schrodeck regenerates `ActionID`s on every install, ADR 0026); the restart-tier probe in M3 records it". If the owner wants it tested by hand anyway, that is a separate, explicitly approved step on a throwaway profile with the app quit.
- Issue #1 needs the second Mac with the same physical deck attached. If it is available: on each Mac, run `/tmp/schrodeck-m1 inventory --json --show-ids` **in the terminal only** (never through the script, never pasted anywhere: the output contains serials) and compare the key of that deck by eye; record only "identical" or "different" on issue #1 and as a references row, and close #1 from the PR. If it is not available, leave #1 open and say so in the PR body.

- [ ] **Step 12: Review every report, write the findings into the docs, clean up**

For each file in `docs/observations/`: read it in full, replace anything personal the redactor missed (profile names, titles), and fill in its draft evidence row. Then:

- `docs/references.md`: one row per settled question (R22, R23, …), status **Observed**, source `docs/observations/<name>.md`, the finding as a claim.
- `docs/streamdeck-config-model.md` § Unknowns: mark each U-row answered (with its R id) or still open (with what the observation showed); add rows U7–U10.
- `docs/contracts/profile-format.md`: update P8, P10 and P11 (Basis column: observed with R id; or still unknown).
- `docs/contracts/client-os.md`: the verified-versions row from step 2.
- If U2, U3 or P11 show that the hash definition is wrong (e.g. folder sub-pages need canonical labels), do **not** change `normhash` in this PR: record the finding and open an issue for a contract C change with a `norm_version` bump.

Finally, in the app, delete every `schrodeck-m1-scratch` profile (and the second virtual deck, if Step 6 left it).

- [ ] **Step 13: Leak-scan, commit, PR**

```bash
git add tools/observe-m1.sh docs internal/decks
git status --short   # only those paths; no .log file may appear
tools/ci/leak-scan.sh
# plus the manual pre-push scan from the global rules; both must be clean
git commit -m "docs(#<n>): settle format unknowns on a real Mac; geometry rows R22"
export $(cat ~/.ssh_agent_socket) && git push -u origin HEAD
gh pr create --fill --body "$(printf 'Closes #%s\nCloses #2\nRefs #1 (or Closes #1 if Step 11 compared two Macs)\n\nhttps://claude.ai/code/session_01BpNb9wCfEXosfKyRoBrsr4\n' <n>)"
```

Edit the body's `#1`/`#2` lines to match what was actually settled before creating the PR. Then CI, a code-review subagent, the owner's review (the owner reads every observation file before merge: they come from a real machine), squash merge.

---

## Self-review

- **Spec / milestone coverage (M1 row and ADR 0031):** model/parser keeping unknown fields (Tasks 1–2); normalizer/hasher P9, P11 (Task 4); semantic diff (Task 5); observation harness (Task 7, CLI in Task 11); probe runner (Task 8) with `doctor` built on it (Task 11); redacted fixture export (Task 6, CLI in Task 11); `status`, `doctor` read-only tier, `inventory` (Task 11); enumerate decks and profiles, normalize, hash, app version and schema check (Tasks 3, 10, 11); ADR 0010 `host_id` (Task 9); ADR 0026 identity mapping (Task 9); ADR 0003 geometry from R8 plus observed product mapping and grid split (Tasks 10, 12); ADR 0019 selected profile read only in probe M2 (Task 11); settle U1–U6, P8, P10, P11 and issues #1 (when the second Mac is available) and #2, and update contract C before any write path (Task 12). ADR 0017 logging: M1 logs to stderr through M0's `logging` package; the persistent log file under `Paths.LogDir()` is deferred to M4, the first milestone with unattended runs, and every M1 command run on a real machine is teed to a log outside the repository instead (Task 12's runner). ADR 0031's "Verified by": the import check (M0 CI, re-run in Task 8), round trip with an injected unknown field and a known-bad naive decoder (Tasks 1–2), hash pairs with known-bad variants (Task 4), `observe` against fixture before/after with redaction and pseudonyms (Tasks 7, 11). Issue #3's normalization fixtures (moved to their own issue by M0 Task 9): Task 2's `fixture` package and Task 4's pairs. Not in M1 by design: P4, P10's restart-tier record, contract B M3/M4 (all M3), variables and `{{DEVICE}}` substitution (M2/M3, ADR 0006).
- **Write boundary:** `deckformat` reads the app's files only through `fs.FS`; `observe`, `redact` and `pathguard` use `os` to write snapshots (schrodeck's state dir) and exports (a path the user names), which ADR 0031 allows, and every user-named target passes `pathguard.RefuseInside`.
- **Placeholders:** the issue numbers (`<n>`) and the Task 12 values that only an observation can produce (fingerprint, product ids, grid splits, R-row text) are the only fill-ins, and each comes with the exact procedure that yields it. Every code step is complete code that was compiled and tested in a scratch tree on 2026-10-02 (go1.27.1, darwin/arm64), with `SCHRODECK_LIVE` unset; the Linux and Windows paths were cross-built there but not run.
- **Type consistency:** `probe.Result` replaces M0's `cli.CheckResult` in `doctor`'s output (an additive change for contract E, documented in Task 11 step 10); `host.Host` is introduced in Task 10 and used by `connector`, `doctor` and `cli`; `decks.RecordKey`/`RecordRaw` are shared by `macos.DeviceRecords`, `decks.Enumerate`, `decks.Unmatched` and `doctor.m2`; `decks.KnownGeometry()` is a function derived from `DeviceTypes` and `ProductTypes`, called by the macOS connector; `pathguard` (Task 6) is used by `redact.ExportFixture` and the CLI (Task 11).
- **Review Focus:** each line names its pinning test and owning task; F1 is the one hazard a unit test can only document, so its measurement is a Task 12 step with a stop condition.
