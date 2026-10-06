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
		"Images/x.png~":                                   false,
		".manifest.json.swp":                              false,
		"#manifest.json#":                                 false,
		"~$x.png":                                         false,
		"notes.swp":                                       true,
		"#x":                                              true,
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

func TestJunkPatternsRecorded(t *testing.T) {
	cases := []string{
		"Images/x.png~",
		".manifest.json.swp",
		"#manifest.json#",
		"~$x.png",
	}
	for _, junk := range cases {
		fp := fixture.XL()
		fp.Extra = map[string][]byte{junk: []byte("x")}
		p := load(t, fp)
		if len(p.Junk) != 1 || p.Junk[0] != junk {
			t.Errorf("%s: Junk = %v, want [%q]", junk, p.Junk, junk)
		}
		if _, ok := p.Files()[junk]; ok {
			t.Errorf("%s: junk must not be part of the profile's files", junk)
		}
	}
}

func TestCaseSensitivePageFolders(t *testing.T) {
	fp := fixture.XL()
	fsys := fp.FS()
	// Add conflicting page folders that differ only in case
	fsys[fp.Folder()+"/Profiles/ABC/manifest.json"] = &fstest.MapFile{Data: []byte(`{"Controllers":[{"Type":"Keypad"}],"Icon":"","Name":""}`)}
	fsys[fp.Folder()+"/Profiles/abc/manifest.json"] = &fstest.MapFile{Data: []byte(`{"Controllers":[{"Type":"Keypad"}],"Icon":"","Name":""}`)}
	_, err := Load(fsys, fp.Folder())
	if err == nil {
		t.Fatal("loading with conflicting page folder case must error")
	}
	if !strings.Contains(err.Error(), "ABC") || !strings.Contains(err.Error(), "abc") {
		t.Fatalf("error must name both conflicting folders: %v", err)
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
