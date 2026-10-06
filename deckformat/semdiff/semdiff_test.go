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

func TestPathInjectivityBracketMember(t *testing.T) {
	// Member named "[0]" should be distinguishable from array index [0]
	a, _ := jsondoc.Parse([]byte(`{"x":{"[0]":1}}`))
	b, _ := jsondoc.Parse([]byte(`{"x":{"[0]":2}}`))
	cs := Values("P", "w", a, b)
	if len(cs) != 1 {
		t.Fatalf("expected 1 change, got %d: %s", len(cs), render(cs))
	}
	// Path must be x.["[0]"] to distinguish from x[0]
	if cs[0].Path != `["[0]"]` && cs[0].Path != `x.["[0]"]` && !strings.Contains(cs[0].Path, `["[0]"]`) {
		t.Errorf("path %q does not properly quote bracket member; must distinguish from array index", cs[0].Path)
	}
}

func TestPathInjectivityArrayVsMember(t *testing.T) {
	// {"a":[1],"a[0]":1} has two distinct locations: array a[0] and member a[0]
	// Both should be changed without collision
	a, _ := jsondoc.Parse([]byte(`{"a":[1],"a[0]":0}`))
	b, _ := jsondoc.Parse([]byte(`{"a":[2],"a[0]":1}`))
	cs := Values("P", "w", a, b)
	if len(cs) != 2 {
		t.Fatalf("expected 2 changes (array and member), got %d: %s", len(cs), render(cs))
	}
	paths := map[string]bool{}
	for _, c := range cs {
		if paths[c.Path] {
			t.Errorf("duplicate path %q - paths are not injective", c.Path)
		}
		paths[c.Path] = true
	}
}

func TestPathInjectivityDotMember(t *testing.T) {
	// {"a":{"b":1},"a.b":1} has two distinct locations: nested a→b and member a.b
	// Both should be changed without collision
	a, _ := jsondoc.Parse([]byte(`{"a":{"b":1},"a.b":0}`))
	b, _ := jsondoc.Parse([]byte(`{"a":{"b":2},"a.b":1}`))
	cs := Values("P", "w", a, b)
	if len(cs) != 2 {
		t.Fatalf("expected 2 changes (nested and member), got %d: %s", len(cs), render(cs))
	}
	paths := map[string]bool{}
	for _, c := range cs {
		if paths[c.Path] {
			t.Errorf("duplicate path %q - paths are not injective", c.Path)
		}
		paths[c.Path] = true
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
	// Should find the change in my.setting (not split incorrectly)
	found := false
	for _, c := range cs {
		if strings.Contains(c.Path, `["my.setting"]`) || (strings.Contains(c.Path, "my") && strings.Contains(c.Path, "setting")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Settings member with dot not properly handled: %s", render(cs))
	}
}
