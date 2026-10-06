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
	return digestOf(t, ps...)
}

func digestOf(t *testing.T, ps ...*Profile) string {
	t.Helper()
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
	if digestOf(t, v4) == base {
		t.Error("a changed Version kept the fingerprint")
	}
	unknown := load(t, fixture.XL())
	unknown.Manifest.Set("FutureField", jsondoc.NewString("x"))
	if digestOf(t, unknown) == base {
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

func TestSchemaRejectsMissingVersionInAnyOrder(t *testing.T) {
	good := func() *Profile { return load(t, fixture.XL()) }
	versionless := func() *Profile {
		p := load(t, fixture.CopyOf(fixture.XL(), "v"))
		p.Manifest.Delete("Version")
		return p
	}
	cases := map[string][]*Profile{
		"versionless alone":        {versionless()},
		"versionless then good":    {versionless(), good()},
		"good then versionless":    {good(), versionless()},
		"two versionless profiles": {versionless(), versionless()},
	}
	for name, ps := range cases {
		if s, err := SchemaOf(ps); err == nil {
			t.Errorf("%s accepted (version %q)", name, s.Version)
		}
	}
}

func TestSchemaRejectsEmptyProfileList(t *testing.T) {
	for _, ps := range [][]*Profile{nil, {}} {
		if _, err := SchemaOf(ps); err == nil {
			t.Error("an empty profile list was accepted")
		}
	}
}

// slotAction returns the action object at slot on the first page.
func slotAction(t *testing.T, p *Profile, slot string) *jsondoc.Value {
	t.Helper()
	for _, key := range p.SortedPageKeys() {
		if a := p.Pages[key].Manifest.Lookup("Controllers").Items()[0].Lookup("Actions", slot); a != nil {
			return a
		}
	}
	t.Fatalf("no action at slot %s", slot)
	return nil
}

func TestSchemaCollapseRules(t *testing.T) {
	base := schemaDigest(t, fixture.XL())

	// Known-good: key-slot names are data, so a button at a new slot keeps it.
	moreButtons := fixture.XL()
	moreButtons.Pages[0].Buttons = append(moreButtons.Pages[0].Buttons, fixture.Button{
		Slot: "5,2", ActionID: "11111111-0000-4000-8000-0000000000ff", Plugin: "com.elgato.streamdeck.system.open",
		Settings: `{}`, Title: "More", Image: "IMG000000000000000000000000FF.png", ImageSeed: 9,
	})
	if schemaDigest(t, moreButtons) != base {
		t.Error("a button at a new slot altered the fingerprint")
	}

	// Known-good: a value-only change inside an array (States) keeps it.
	valueOnly := load(t, fixture.XL())
	slotAction(t, valueOnly, "0,0").Lookup("States").Items()[0].Get("Title").SetString("Other")
	if digestOf(t, valueOnly) != base {
		t.Error("a value change inside an array altered the fingerprint")
	}

	// Known-bad: a new member inside an action object changes it.
	newMember := load(t, fixture.XL())
	slotAction(t, newMember, "0,0").Set("FutureField", jsondoc.NewString("x"))
	if digestOf(t, newMember) == base {
		t.Error("a new member inside an action object kept the fingerprint")
	}

	// Known-bad: a new member inside a child action nested in a multi-action
	// Actions array changes it (array items are not key slots).
	multi := func(extra bool) *Profile {
		p := load(t, fixture.XL())
		child := `{"Name":"open"`
		if extra {
			child += `,"FutureField":1`
		}
		doc, err := jsondoc.Parse([]byte(`[` + child + `}]`))
		if err != nil {
			t.Fatal(err)
		}
		slotAction(t, p, "0,0").Set("Actions", doc)
		return p
	}
	if digestOf(t, multi(true)) == digestOf(t, multi(false)) {
		t.Error("a new member in a child action inside an Actions array kept the fingerprint")
	}
}
