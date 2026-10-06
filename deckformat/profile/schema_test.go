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
