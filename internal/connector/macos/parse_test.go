// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package macos

import (
	"testing"
	"time"

	"howett.net/plist"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/ports"
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

// jsondoc.FromAny rejects []byte and time.Time. A real plist decode yields both
// for <data> and <date>, so the records must be converted before they reach it
// rather than failing the whole read.
func TestDeviceRecordsFromRealPlistSurviveFromAny(t *testing.T) {
	when := time.Date(2026, 10, 2, 1, 2, 3, 0, time.UTC)
	raw, err := plist.Marshal(map[string]any{"Devices": map[string]any{
		"@(0)[]": map[string]any{
			"blob":   []byte{1, 2},
			"when":   when,
			"nested": []any{map[string]any{"blob": []byte{3}, "when": when}},
			"n":      uint64(7),
			"f":      1.5,
			"b":      true,
		},
	}}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	var prefs map[string]any
	if _, err := plist.Unmarshal(raw, &prefs); err != nil {
		t.Fatal(err)
	}
	// Known-bad: the unconverted decode is exactly what FromAny refuses.
	if _, err := jsondoc.FromAny(prefs["Devices"]); err == nil {
		t.Fatal("FromAny accepted raw plist data/date; the conversion is not needed, so this test proves nothing")
	}
	recs, err := DeviceRecords(prefs)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsondoc.FromAny(recs[0])
	if err != nil {
		t.Fatalf("FromAny on converted record: %v", err)
	}
	if got := v.Get("blob"); !strIs(got, "base64:AQI=") {
		t.Errorf("blob = %v", got)
	}
	if got := v.Get("when"); !strIs(got, "2026-10-02T01:02:03Z") {
		t.Errorf("when = %v", got)
	}
}

func strIs(v *jsondoc.Value, want string) bool {
	if v == nil {
		return false
	}
	s, ok := v.Str()
	return ok && s == want
}

// End to end through the connector's own path: two profiles bound to the shared
// virtual key arrive as ONE prefs record, which must still not be a destination
// (U5), even if a virtual geometry were known.
func TestSharedVirtualKeyThroughPrefsIsNeverADestination(t *testing.T) {
	raw, err := plist.Marshal(map[string]any{"Devices": map[string]any{decks.VirtualKey: map[string]any{"DeviceName": ""}}}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	var prefs map[string]any
	if _, err := plist.Unmarshal(raw, &prefs); err != nil {
		t.Fatal(err)
	}
	a := fixture.XL()
	a.Device = decks.VirtualKey
	b := fixture.CopyOf(a, "second")
	var profs []*profile.Profile
	for _, fx := range []fixture.Profile{a, b} {
		p, err := profile.Load(fx.FS(), fx.Folder())
		if err != nil {
			t.Fatal(err)
		}
		profs = append(profs, p)
	}
	recs, err := DeviceRecords(prefs)
	if err != nil {
		t.Fatal(err)
	}
	ds := decks.Enumerate(recs, profs, nil)
	if len(ds) != 1 || !ds[0].Virtual {
		t.Fatalf("decks = %+v", ds)
	}
	ds[0].Geometry = ports.Geometry{Columns: 8, Rows: 4}
	st := decks.Annotate(ds)
	if !st[0].GeometryKnown || st[0].Destination() {
		t.Fatalf("shared virtual key became a destination: %+v", st[0])
	}
}

// `defaults export` of a domain with no keys prints an empty dict; that is an
// error about missing Devices, not a crash or an empty deck list.
func TestEmptyPrefsDictHasNoDevices(t *testing.T) {
	var prefs map[string]any
	if _, err := plist.Unmarshal([]byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict/></plist>`), &prefs); err != nil {
		t.Fatal(err)
	}
	if _, err := DeviceRecords(prefs); err == nil {
		t.Fatal("empty prefs accepted")
	}
}
