// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package semdiff

import (
	"sort"
	"strings"
	"testing"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/normhash"
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

func TestPathInjectivityBracketMember(t *testing.T) {
	// Member named "[0]" should be distinguishable from array index [0]
	a, _ := jsondoc.Parse([]byte(`{"x":{"[0]":1}}`))
	b, _ := jsondoc.Parse([]byte(`{"x":{"[0]":2}}`))
	cs := Values("P", "w", a, b)
	if len(cs) != 1 {
		t.Fatalf("expected 1 change, got %d: %s", len(cs), render(cs))
	}
	// Path must be x.["[0]"] to distinguish from x[0]
	if cs[0].Path != `x.["[0]"]` {
		t.Errorf("BracketMember path: got %q, want %q", cs[0].Path, `x.["[0]"]`)
	}
}

func TestPathInjectivityArrayVsMember(t *testing.T) {
	// {"a":[1],"a[0]":1} has two distinct locations: array a[0] and member "a[0]"
	// Both should be changed without collision
	a, _ := jsondoc.Parse([]byte(`{"a":[1],"a[0]":0}`))
	b, _ := jsondoc.Parse([]byte(`{"a":[2],"a[0]":1}`))
	cs := Values("P", "w", a, b)
	if len(cs) != 2 {
		t.Fatalf("expected 2 changes (array and member), got %d: %s", len(cs), render(cs))
	}
	// The two paths should be exactly "a[0]" (array index) and `["a[0]"]` (quoted member name)
	paths := []string{cs[0].Path, cs[1].Path}
	sort.Strings(paths)
	expectedPaths := []string{`a[0]`, `["a[0]"]`}
	sort.Strings(expectedPaths)
	if paths[0] != expectedPaths[0] || paths[1] != expectedPaths[1] {
		t.Errorf("ArrayVsMember paths: got %q, want %q and %q", paths, expectedPaths[0], expectedPaths[1])
	}
}

func TestPathInjectivityDotMember(t *testing.T) {
	// {"a":{"b":1},"a.b":1} has two distinct locations: nested a→b and member "a.b"
	// Both should be changed without collision
	a, _ := jsondoc.Parse([]byte(`{"a":{"b":1},"a.b":0}`))
	b, _ := jsondoc.Parse([]byte(`{"a":{"b":2},"a.b":1}`))
	cs := Values("P", "w", a, b)
	if len(cs) != 2 {
		t.Fatalf("expected 2 changes (nested and member), got %d: %s", len(cs), render(cs))
	}
	// The two paths should be exactly "a.b" (nested) and `["a.b"]` (quoted member name)
	paths := []string{cs[0].Path, cs[1].Path}
	sort.Strings(paths)
	expectedPaths := []string{`a.b`, `["a.b"]`}
	sort.Strings(expectedPaths)
	if paths[0] != expectedPaths[0] || paths[1] != expectedPaths[1] {
		t.Errorf("DotMember paths: got %q, want %q and %q", paths, expectedPaths[0], expectedPaths[1])
	}
}

func TestPathInjectivitySettingsDotMember(t *testing.T) {
	// A Settings object with a member name containing a dot, to show that
	// slot/tail splitting in pageChanges still works correctly
	before := fixture.XL()
	after := fixture.XL()
	// Modify a Settings member that contains a dot in its name
	after.Pages[0].Buttons[0].Settings = `{"openInBrowser":true,"my.setting":123}`
	cs, err := Profiles(loadP(t, before), loadP(t, after), Semantic)
	if err != nil {
		t.Fatal(err)
	}
	// Should find exactly one change with the quoted member in Settings
	found := false
	for _, c := range cs {
		if c.Where == "page 1 › key 0,0" && c.Path == `Settings.["my.setting"]` {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("SettingsDotMember: expected Where=%q Path=%q in %s", "page 1 › key 0,0", `Settings.["my.setting"]`, render(cs))
	}
}

func TestPathEscaping(t *testing.T) {
	// Member name containing both " and \ should be escaped correctly
	a, _ := jsondoc.Parse([]byte(`{"x":{"a\"b\\c":1}}`))
	b, _ := jsondoc.Parse([]byte(`{"x":{"a\"b\\c":2}}`))
	cs := Values("P", "w", a, b)
	if len(cs) != 1 {
		t.Fatalf("expected 1 change, got %d: %s", len(cs), render(cs))
	}
	// Path should have both " and \ properly escaped inside the quotes
	expected := `x.["a\"b\\c"]`
	if cs[0].Path != expected {
		t.Errorf("escaping path: got %q, want %q", cs[0].Path, expected)
	}
}

// Semantic mode must report nothing for profiles the normalized hash calls
// equal: a number or string spelled differently, or members in another order,
// is not a user change. Raw mode still shows the spelling, which is what
// observing the app needs.
func TestSemanticIgnoresSpellingTheHashIgnores(t *testing.T) {
	bs := string(rune(92))
	pairs := []struct{ name, a, b string }{
		{"number spelling", `{"gain":1.50}`, `{"gain":1.5}`},
		{"escape vs literal", `{"label":"caf` + bs + `u00e9"}`, `{"label":"café"}`},
		{"member order", `{"a":1,"b":2}`, `{"b":2,"a":1}`},
	}
	for _, c := range pairs {
		t.Run(c.name, func(t *testing.T) {
			before, after := fixture.XL(), fixture.XL()
			before.Pages[0].Buttons[0].Settings = c.a
			after.Pages[0].Buttons[0].Settings = c.b
			pa, pb := loadP(t, before), loadP(t, after)
			ha, err := normhash.Hash(pa)
			if err != nil {
				t.Fatal(err)
			}
			hb, err := normhash.Hash(pb)
			if err != nil {
				t.Fatal(err)
			}
			if ha != hb {
				t.Fatalf("premise broken: the pair no longer hashes equal, so the test says nothing about semdiff")
			}
			sem, err := Profiles(pa, pb, Semantic)
			if err != nil {
				t.Fatal(err)
			}
			if len(sem) != 0 {
				t.Fatalf("Semantic reported changes on a hash-equal pair:\n%s", render(sem))
			}
			raw, err := Profiles(pa, pb, Raw)
			if err != nil {
				t.Fatal(err)
			}
			if c.name != "member order" && len(raw) == 0 {
				t.Fatal("Raw reported nothing, so the pair does not differ as stored and the test cannot see the Semantic comparison")
			}
		})
	}
}

func TestParsePathInvertsRendering(t *testing.T) {
	cases := [][]seg{
		{{name: "a"}, {name: "b"}},
		{{name: "x"}, {name: "[0]"}},
		{{name: "a"}, {idx: 0, isIdx: true}},
		{{name: "a[0]"}},
		{{name: "a.b"}},
		{{name: "x"}, {name: `a"b\c`}},
		{{name: ""}, {name: "k"}},
		{{name: "a"}, {idx: 12, isIdx: true}, {idx: 3, isIdx: true}, {name: "z"}},
		{{idx: 1, isIdx: true}, {name: "q"}},
		{{name: `\`}},
	}
	for _, segs := range cases {
		path := renderPathSegs(segs)
		got, err := ParsePath(path)
		if err != nil {
			t.Errorf("ParsePath(%q): %v", path, err)
			continue
		}
		if len(got) != len(segs) {
			t.Errorf("ParsePath(%q) = %v", path, got)
			continue
		}
		for i := range segs {
			if got[i] != (Segment{Name: segs[i].name, Index: segs[i].idx, IsIndex: segs[i].isIdx}) {
				t.Errorf("ParsePath(%q)[%d] = %+v, want %+v", path, i, got[i], segs[i])
			}
		}
	}
	for _, bad := range []string{"a.", "a..b", `["x`, `["x"`, "[x]", "[]", "a[1", "a b.", `a["x"]`, "[99999999999999999999]"} {
		if _, err := ParsePath(bad); err == nil {
			t.Errorf("ParsePath(%q) accepted a string the renderer never produces", bad)
		}
	}
}

// TestPageChangesNameTheirPageAndSlot checks the routing fields a caller needs
// to resolve a page change against exactly one page manifest.
func TestPageChangesNameTheirPageAndSlot(t *testing.T) {
	before := fixture.XL()
	after := fixture.XL()
	after.Pages[1].Buttons[0].Settings = `{"x":1}`
	for _, tc := range []struct {
		mode Mode
		page string
	}{{Raw, after.Pages[1].ID}, {Semantic, "page/1"}} {
		cs, err := Profiles(loadP(t, before), loadP(t, after), tc.mode)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range cs {
			if c.Path != "Settings.x" {
				continue
			}
			found = true
			if c.Page != tc.page || c.SlotPath != "Controllers[0].Actions.7,3" {
				t.Errorf("mode %d: Page %q SlotPath %q, want %q and Controllers[0].Actions.7,3", tc.mode, c.Page, c.SlotPath, tc.page)
			}
		}
		if !found {
			t.Fatalf("mode %d: no Settings.x change in\n%s", tc.mode, render(cs))
		}
		for _, c := range cs {
			if (c.Where == "profile" || c.Where == "files") && (c.Page != "" || c.SlotPath != "") {
				t.Errorf("mode %d: non-page change carries page routing: %+v", tc.mode, c)
			}
		}
	}
}
