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
	for _, bad := range []string{"", "Devices", "@(1)[4057/143]", "x@(1)[1/2/3]", "@(1)[4057/143/]", "@(2)[4057/143/<deck>]", "@(9)[]", "@(1)[]", "@(1)[99999999999999999999/1/<deck>]", "@(1)[1/99999999999999999999/<deck>]"} {
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
	// Known-bad: a product mapped to a type that has no observed grid. The
	// table is local, so observing grids in the live DeviceTypes (Task 12)
	// cannot turn this case into a valid one.
	gridless := map[int]TypeInfo{2: {Name: "Stream Deck XL", Keys: 32}}
	if err := Validate(gridless, map[[2]int]int{{4057, 143}: 2}); err == nil {
		t.Fatal("a mapped DeviceType without columns/rows was accepted")
	}
	// Known-bad: a grid that contradicts R8's documented key count.
	bad := map[int]TypeInfo{2: {Name: "Stream Deck XL", Keys: 32, Columns: 8, Rows: 3}}
	if err := Validate(bad, nil); err == nil {
		t.Fatal("an 8×3 grid for a 32-key type was accepted")
	}
	// Known-bad: a product mapped to a type R8 does not list.
	if err := Validate(gridless, map[[2]int]int{{4057, 1}: 99}); err == nil {
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

// Task 11 presents device records to observe as an object keyed by the device
// record key; Keyed is that form.
func TestKeyed(t *testing.T) {
	records := []map[string]any{
		{RecordKey: "@(0)[]", "a": "x"},
		{RecordKey: "SomethingElse", RecordRaw: "opaque"},
	}
	got := Keyed(records)
	if len(got) != 2 || got["@(0)[]"]["a"] != "x" || got["SomethingElse"][RecordRaw] != "opaque" {
		t.Fatalf("Keyed = %v", got)
	}
	if got["@(0)[]"][RecordKey] != "@(0)[]" {
		t.Error("the record lost its key member")
	}
	if len(Keyed(nil)) != 0 {
		t.Error("nil records must give an empty object")
	}
}

// A record with no profile bound to it is still a deck, just one without a
// manifest id or model.
func TestEnumerateDeckWithoutProfile(t *testing.T) {
	known := map[[2]int]ports.Geometry{{4057, 143}: {Columns: 8, Rows: 4}}
	got := Enumerate([]map[string]any{{RecordKey: fixture.Device}}, nil, known)
	if len(got) != 1 || got[0].ManifestDeviceID != "" || got[0].Model != "" || got[0].AppDeviceID != fixture.Device {
		t.Fatalf("got %+v", got)
	}
	if st := Annotate(got); !st[0].Destination() {
		t.Errorf("a known-geometry unique deck without a profile should still be a destination: %+v", st[0])
	}
}

// Review F31 / U5: a shared virtual key is never unique, even when it appears
// once (the prefs dictionary collapses N virtual decks into one record).
func TestVirtualKeyIsNeverUnique(t *testing.T) {
	d := ports.Deck{AppDeviceID: VirtualKey, Virtual: true, Geometry: ports.Geometry{Columns: 8, Rows: 4}}
	if st := Annotate([]ports.Deck{d}); st[0].KeyUnique || st[0].Destination() {
		t.Fatalf("a lone virtual deck must not be a destination: %+v", st[0])
	}
}

// Known-bad: type 0 with a physical-looking body must stay virtual (never
// unique, never a destination), and an unknown type must be rejected.
func TestOnlyTypeOneIsPhysical(t *testing.T) {
	if v, _, _, _, ok := ParseKey("@(0)[4057/143/<deck>]"); !ok || !v {
		t.Fatalf("type 0 with a body must parse as virtual, got virtual=%v ok=%v", v, ok)
	}
	if _, _, _, _, ok := ParseKey("@(2)[4057/143/<deck>]"); ok {
		t.Fatal("type 2 accepted")
	}
	known := map[[2]int]ports.Geometry{{4057, 143}: {Columns: 8, Rows: 4}}
	records := []map[string]any{{RecordKey: "@(0)[4057/143/<deck>]"}, {RecordKey: "@(2)[4057/143/<deck>]"}}
	ds := Enumerate(records, nil, known)
	if len(ds) != 1 || !ds[0].Virtual {
		t.Fatalf("decks = %+v", ds)
	}
	ds[0].Geometry = ports.Geometry{Columns: 8, Rows: 4}
	for _, st := range Annotate(ds) {
		if st.Destination() {
			t.Fatalf("a type-0 key became a destination: %+v", st)
		}
	}
	_, unbound := Unmatched(records, nil)
	if len(unbound) != 2 {
		t.Errorf("Unmatched must list both keys, got %v", unbound)
	}
}

func TestUnrecognisedListsEveryRejectedKey(t *testing.T) {
	p, err := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	if err != nil {
		t.Fatal(err)
	}
	odd := "@(2)[4057/143/<deck>]"
	p.Manifest.Lookup("Device", "UUID").SetString(odd)
	records := []map[string]any{
		{RecordKey: fixture.Device},
		{RecordKey: odd},                                  // bound: used to vanish
		{RecordKey: "not a device key"},                   // unbound
		{RecordKey: "SomethingElse", RecordRaw: "opaque"}, // not a record: listed by RawEntries
	}
	got := Unrecognised(records, []*profile.Profile{p})
	want := []UnrecognisedKey{{Key: odd, Profiles: 1}, {Key: "not a device key", Profiles: 0}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Unrecognised = %+v, want %+v", got, want)
	}
	raw := RawEntries(records)
	if len(raw) != 1 || raw[0] != (RawEntry{Key: "SomethingElse", Type: "string"}) {
		t.Fatalf("RawEntries = %+v", raw)
	}
}
