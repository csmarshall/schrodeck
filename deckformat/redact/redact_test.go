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

// Known-bad (F3): "<symlink into the source>/../out" is, for the kernel, a
// directory inside the source tree, although cleaning the ".." lexically puts
// it beside the symlink, outside.
func TestExportRefusesDotDotAfterSymlink(t *testing.T) {
	p, _ := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(src, "sub"), link); err != nil {
		t.Fatal(err)
	}
	if err := ExportFixture(p, newR(t), src, link+"/../out", "F.sdProfile"); !errors.Is(err, pathguard.ErrInside) {
		t.Fatalf("export through <link>/../ into the source tree: %v", err)
	}
	for _, landed := range []string{filepath.Join(src, "out"), filepath.Join(base, "out")} {
		if _, err := os.Stat(landed); err == nil {
			t.Errorf("a refused export created %s", landed)
		}
	}
}

// An allowed export lands exactly where the guard looked: through a symlinked
// output directory that points outside the source, at the link's target.
func TestExportWritesWhereTheGuardChecked(t *testing.T) {
	p, _ := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	base := t.TempDir()
	dest := filepath.Join(base, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(dest, link); err != nil {
		t.Fatal(err)
	}
	if err := ExportFixture(p, newR(t), filepath.Join(base, "src"), link, "F.sdProfile"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "F.sdProfile", "manifest.json")); err != nil {
		t.Fatalf("export did not land in the link target: %v", err)
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
