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
// TestWriteFixtureForReference). This shows the two implementations agree on
// this fixture. If this test fails after a fixture change, recompute it with
// the reference, never by copying the Go output.
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

// onlyImageNames creates a copy with renamed image files. CopyOf also changes
// page IDs and ActionIDs, but the test's off-config (hashImages=false)
// neutralizes those changes, leaving only the image name change visible.
func onlyImageNames(p fixture.Profile) fixture.Profile {
	c := fixture.CopyOf(p, "images")
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

// --- golden value for relabeling, missing images, and "other" pages --------

// goldenRelabelMissingOther is the normalized hash of a fixture variant that
// exercises page relabeling, missing images, and "other" (unreferenced) pages.
// Computed with the independent reference implementation (testdata/refhash.py).
// This proves the hash correctly handles all three normalization aspects.
const goldenRelabelMissingOther = "2c1fe89b79146526194a470d7e63a34607c69837d2a6641bea57be4322b3c60c"

func fixtureWithRelabelMissingOther() fixture.Profile {
	p := fixture.XL()
	// Add settings that reference page UUIDs in upper case, including nested in an array
	p.Pages[0].Buttons[0].Settings = `{"pages":["` + strings.ToUpper(p.Pages[1].ID) + `"],"target":"` + strings.ToUpper(p.Pages[0].ID) + `"}`
	// Mark one button's image as missing
	p.Pages[0].Buttons[1].MissingImage = true
	// Add an "other" page (not in Pages.Pages or Default)
	otherPage := fixture.Page{ID: "aaaaaaaa-0000-4000-8000-0000000000ff", Buttons: []fixture.Button{
		{Slot: "0,0", ActionID: "11111111-0000-4000-8000-000000000099", Plugin: "com.elgato.streamdeck.system.open",
			Settings: `{"path":"/other"}`, Title: "Other", Image: "IMG00000000000000000000000099.png", ImageSeed: 99},
	}}
	p.Pages = append(p.Pages, otherPage)
	return p
}

func TestWriteFixtureRelabelMissingOtherForReference(t *testing.T) {
	out := os.Getenv("NORMHASH_FIXTURE_OUT")
	if out == "" {
		t.Skip("set NORMHASH_FIXTURE_OUT to write the fixture for the reference implementation")
	}
	p := fixtureWithRelabelMissingOther()
	if err := fixture.WriteTo(out, p.FS()); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s/%s", out, p.Folder())
}

func TestGoldenCoversRelabelMissingAndOther(t *testing.T) {
	p := fixtureWithRelabelMissingOther()
	if got := hash(t, p); got != goldenRelabelMissingOther {
		t.Fatalf("Hash(fixtureWithRelabelMissingOther()) = %s, reference says %s", got, goldenRelabelMissingOther)
	}
}
