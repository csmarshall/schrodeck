// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package normhash

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

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
// This proves the hash correctly handles page-UUID relabeling in Settings and
// the lower-case UUID labeling of unreferenced pages.
const goldenRelabelMissingOther = "7201a84cf63ab71ab515ff375cdbf3426b07190a76b2652f531c2572aa144a7e"

func fixtureWithRelabelMissingOther() fixture.Profile {
	p := fixture.XL()
	// Add settings that reference page UUIDs in upper case, including nested in an array
	p.Pages[0].Buttons[0].Settings = `{"pages":["` + strings.ToUpper(p.Pages[1].ID) + `"],"target":"` + strings.ToUpper(p.Pages[0].ID) + `"}`
	// Mark one button's image as missing
	p.Pages[0].Buttons[1].MissingImage = true

	// Create an "other" page (on disk but not in Pages.Pages or Default).
	// We add it to the file system via Extra, so it exists on disk but is not listed.
	otherPageFolder := "AAAAAAAA-0000-4000-8000-0000000000FF"

	// Construct the page manifest
	otherPageManifest := []byte(`{"Controllers":[{"Actions":{"0,0":{"ActionID":"11111111-0000-4000-8000-000000000099","LinkedTitle":true,"Name":"open","Plugin":{"Name":"Fixture","UUID":"com.elgato.streamdeck.system.open","Version":"1.0"},"Resources":null,"Settings":{"path":"/other"},"State":0,"States":[{"Image":"Images/IMG00000000000000000000000099.png","Title":"Other"}],"UUID":"com.elgato.streamdeck.system.open"}},"Type":"Keypad"}],"Icon":"","Name":""}`)

	if p.Extra == nil {
		p.Extra = make(map[string][]byte)
	}
	p.Extra["Profiles/"+otherPageFolder+"/manifest.json"] = otherPageManifest
	p.Extra["Profiles/"+otherPageFolder+"/Images/IMG00000000000000000000000099.png"] = fixture.PNG(99)

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
	lp := loadP(t, p)

	// Verify that the "other" page is present in the normalized paths
	docs, err := Normalize(lp)
	if err != nil {
		t.Fatal(err)
	}
	var foundOtherLabel bool
	for _, d := range docs {
		if strings.Contains(d.Path, "other/aaaaaaaa-0000-4000-8000-0000000000ff") {
			foundOtherLabel = true
		}
	}
	if !foundOtherLabel {
		t.Fatal("expected to find 'other/<lower-case UUID>' path in normalized docs")
	}

	// Verify golden value
	if got := hash(t, p); got != goldenRelabelMissingOther {
		t.Fatalf("Hash(fixtureWithRelabelMissingOther()) = %s, reference says %s", got, goldenRelabelMissingOther)
	}
}

// --- review fixes (PR #18) ----------------------------------------------------

// reverseTopLevelMembers re-emits a compact JSON object with its top-level
// members in reverse sorted order, values untouched. The loader (P12) only
// requires that a manifest round-trips byte for byte, so this is a valid
// manifest an app could write.
func reverseTopLevelMembers(t *testing.T, data []byte) []byte {
	t.Helper()
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(members))
	for n := range members {
		names = append(names, n)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(n)
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(members[n])
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func loadFS(t *testing.T, fsys fstest.MapFS, folder string) *profile.Profile {
	t.Helper()
	p, err := profile.Load(fsys, folder)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// I1: JCS (contract C step 3) is what makes member order irrelevant.
func TestMemberOrderDoesNotCount(t *testing.T) {
	base := fixture.XL()
	reordered := base.FS()
	for name, f := range reordered {
		if strings.HasSuffix(name, "/manifest.json") {
			f.Data = reverseTopLevelMembers(t, f.Data)
		}
	}
	if bytes.Equal(reordered[base.Folder()+"/manifest.json"].Data, base.FS()[base.Folder()+"/manifest.json"].Data) {
		t.Fatal("the reordering did not change the manifest bytes; the test cannot see JCS working")
	}
	got, err := Hash(loadFS(t, reordered, base.Folder()))
	if err != nil {
		t.Fatal(err)
	}
	if want := hash(t, base); got != want {
		t.Fatalf("manifests differing only in member order hash differently: %s vs %s", got, want)
	}
}

// I2: relabeling is scoped to action Settings values (contract C step 2).
func TestPageUUIDOutsideSettingsIsNotRelabeled(t *testing.T) {
	withTitle := func(p fixture.Profile) fixture.Profile {
		p.Pages[0].Buttons[1].Title = strings.ToUpper(p.Pages[1].ID)
		return p
	}
	a := withTitle(fixture.XL())
	b := withTitle(fixture.CopyOf(fixture.XL(), "copy"))
	if hash(t, a) == hash(t, b) {
		t.Fatal("a Title equal to a page UUID was relabeled; relabeling must stay inside action Settings")
	}
}

// m1: paths are NFC. "e" + U+0301 (NFD) must hash like U+00E9 (NFC); the
// strings are built from bytes so no editor or tool can normalize the source.
func TestOtherPageFolderIsNFC(t *testing.T) {
	const nfd, nfc = "e\xcc\x81", "\xc3\xa9"
	withFolder := func(name string) string {
		p := fixture.XL()
		p.Extra = map[string][]byte{
			"Profiles/" + name + "/manifest.json": []byte(`{"Controllers":[],"Icon":"","Name":""}`),
		}
		h, err := Hash(loadP(t, p))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	if withFolder(nfd) != withFolder(nfc) {
		t.Fatal("a page folder named NFD hashes differently from the same name in NFC")
	}
}

// m2: HashDocs sorts, so callers need not.
func TestHashDocsSortsByPath(t *testing.T) {
	docs, err := Normalize(loadP(t, fixture.XL()))
	if err != nil {
		t.Fatal(err)
	}
	want, err := HashDocs(docs)
	if err != nil {
		t.Fatal(err)
	}
	rev := make([]Doc, len(docs))
	for i, d := range docs {
		rev[len(docs)-1-i] = d
	}
	got, err := HashDocs(rev)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("HashDocs depends on the order of its input")
	}
}

func TestHashDocsRejectsDuplicatePathsAndNilValues(t *testing.T) {
	docs, err := Normalize(loadP(t, fixture.XL()))
	if err != nil {
		t.Fatal(err)
	}
	dup := append(append([]Doc{}, docs...), docs[0])
	if _, err := HashDocs(dup); !errors.Is(err, profile.ErrMalformed) {
		t.Errorf("duplicate path: err = %v, want ErrMalformed", err)
	}
	nilValue := append(append([]Doc{}, docs...), Doc{Path: "zzz/manifest.json"})
	if _, err := HashDocs(nilValue); !errors.Is(err, profile.ErrMalformed) {
		t.Errorf("nil Value: err = %v, want ErrMalformed", err)
	}
}

// m3, m4: structural errors in the page lists. Each case edits the top
// manifest of the base fixture by string replacement and must fail with the
// stated error; the replacement is checked to have applied, so a case cannot
// pass by testing an unchanged profile.
func TestPageListErrors(t *testing.T) {
	base := fixture.XL()
	a1, a2, d0 := base.Pages[0].ID, base.Pages[1].ID, base.Default.ID
	cases := []struct {
		name     string
		from, to string
		want     error
	}{
		{"non-string Pages.Default", `"Default":"` + d0 + `"`, `"Default":7`, profile.ErrMalformed},
		{"non-string Pages.Pages entry", `"` + a2 + `"]`, `7]`, profile.ErrMalformed},
		{"duplicate in Pages.Pages", `"` + a2 + `"]`, `"` + a1 + `"]`, profile.ErrMalformed},
		{"Default also in Pages.Pages", `"Default":"` + d0 + `"`, `"Default":"` + a1 + `"`, profile.ErrMalformed},
		{"Pages.Pages entry without a folder", `"` + a2 + `"]`, `"` + a2 + `","aaaaaaaa-0000-4000-8000-0000000000ee"]`, ErrDanglingPage},
		{"Pages.Default without a folder", `"Default":"` + d0 + `"`, `"Default":"aaaaaaaa-0000-4000-8000-0000000000ee"`, ErrDanglingPage},
	}
	for _, c := range cases {
		fsys := base.FS()
		path := base.Folder() + "/manifest.json"
		edited := strings.Replace(string(fsys[path].Data), c.from, c.to, 1)
		if edited == string(fsys[path].Data) {
			t.Fatalf("%s: the edit did not apply", c.name)
		}
		fsys[path] = &fstest.MapFile{Data: []byte(edited)}
		p, err := profile.Load(fsys, base.Folder())
		if err != nil {
			t.Fatalf("%s: Load: %v", c.name, err)
		}
		if _, err := Hash(p); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

// Hashes are lower-case hex (contract C step 4).
func TestHashIsLowerCaseHex(t *testing.T) {
	h := hash(t, fixture.XL())
	if len(h) != 64 || h != strings.ToLower(h) {
		t.Fatalf("hash = %q, want 64 lower-case hex digits", h)
	}
}
