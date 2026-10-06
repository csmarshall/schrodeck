// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package redact

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

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
	if err := ExportFixture(p, r, src, filepath.Join(src, "inside"), "F.sdProfile"); !errors.Is(err, pathguard.ErrInside) {
		t.Fatalf("export into the source tree: %v", err)
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

func TestDistinctSerialsGetDistinctPlaceholders(t *testing.T) {
	other := "@(1)[4057/143/" + "ZZ99" + "YY88XX]"
	r, err := New(Options{Serials: SerialsFrom(realDevice, other)})
	if err != nil {
		t.Fatal(err)
	}
	in := `{"` + realDevice + `":1,"` + other + `":2,"s":"` + strings.ToLower(realSerial) + ` and ` + "ZZ99" + `YY88XX"}`
	v, err := jsondoc.Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out := r.Value(v).Encode()
	want := `{"@(1)[4057/143/<deck>]":1,"@(1)[4057/143/<deck2>]":2,"s":"<deck> and <deck2>"}`
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	if _, err := jsondoc.Parse(out); err != nil {
		t.Fatalf("redacted output does not parse: %v", err)
	}
	// Stable within one Redactor: the same serial keeps its placeholder.
	if got := r.String(other); got != "@(1)[4057/143/<deck2>]" {
		t.Fatalf("second sighting changed placeholder: %s", got)
	}
}

// Known-bad for the export-side check: two member names that redaction maps to
// one name. The export must fail, name the file, and write nothing.
func TestExportRefusesManifestThatStopsParsing(t *testing.T) {
	src := fixture.XL()
	src.Pages[0].Buttons[0].Settings = `{"Alice":1,"alice":2}`
	p, err := profile.Load(src.FS(), src.Folder())
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	err = ExportFixture(p, newR(t), "", out, "F.sdProfile")
	if err == nil || !strings.Contains(err.Error(), "manifest.json") || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("collision not refused with the file named: %v", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("a refused export wrote files: %v", entries)
	}
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// C2 end to end: output directories that are aliases of a directory inside the
// source root (firmlink, long-s case folding) must be refused and write nothing.
func TestExportRefusesAliasedOutputIntoSource(t *testing.T) {
	p, _ := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"firmlink":    "/System/Volumes/Data" + src + "/sub/o1",
		"long s fold": filepath.Join(base, "\xc5\xbfrc", "sub", "o2"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := os.Stat(filepath.Dir(out)); err != nil {
				t.Skipf("this file system does not alias %s: %v", filepath.Dir(out), err)
			}
			if err := ExportFixture(p, newR(t), src, out, "F.sdProfile"); !errors.Is(err, pathguard.ErrInside) {
				t.Fatalf("export through an alias of the source tree: %v", err)
			}
			if n := countFiles(t, src); n != 0 {
				t.Fatalf("%d files written inside the protected source root", n)
			}
		})
	}
}

// C1 end to end: "<missing>/../<symlink into the source>" is refused.
func TestExportRefusesDotDotAfterMissingComponent(t *testing.T) {
	p, _ := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(base, "innocent")); err != nil {
		t.Fatal(err)
	}
	err := ExportFixture(p, newR(t), src, base+"/nonexist/../innocent/out", "F.sdProfile")
	if !errors.Is(err, pathguard.ErrUnresolvable) {
		t.Fatalf("export via a missing component then '..': %v", err)
	}
	if n := countFiles(t, src); n != 0 {
		t.Fatalf("%d files written inside the protected source root", n)
	}
}

// I3: the writer must write to the path the guard checked. "<link>/../out2" is,
// for the kernel, a sibling of the link's TARGET; lexically cleaning it would
// put the files beside the link instead.
func TestExportWritesToTheCheckedPathNotTheCleanedOne(t *testing.T) {
	p, _ := profile.Load(fixture.XL().FS(), fixture.XL().Folder())
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(base, "deep", "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(dest, link); err != nil {
		t.Fatal(err)
	}
	if err := ExportFixture(p, newR(t), filepath.Join(base, "src"), link+"/../out2", "F.sdProfile"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "deep", "out2", "F.sdProfile", "manifest.json")); err != nil {
		t.Fatalf("files did not land in the checked location: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "out2")); err == nil {
		t.Fatal("files landed in the lexically cleaned location")
	}
}

func redactJSON(t *testing.T, r *Redactor, in string) string {
	t.Helper()
	v, err := jsondoc.Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return string(r.Value(v).Encode())
}

// I4 and m5: every scalar under a secret-named member goes, at any depth, and
// the wider list of secret names, URL parameters, name/value pairs and JSON
// embedded in a string are all covered.
func TestSecretsAreRedactedEverywhere(t *testing.T) {
	r := newR(t)
	cases := [][2]string{
		{`{"token":{"value":"tok123"}}`, "tok123"},
		{`{"auth":["tok456",{"deep":["tok457"]}]}`, "tok45"},
		{`{"secret":{"n":12345,"b":true}}`, "12345"},
		{`{"url":"https://h.example/api?access_token=tok789&x=1"}`, "tok789"},
		{`{"url":"https://h.example/api?x=1&password=hunter2"}`, "hunter2"},
		{`{"privateKey":"pk1"}`, "pk1"},
		{`{"private_key":"pk2"}`, "pk2"},
		{`{"AccessKey":"ak1"}`, "ak1"},
		{`{"credentials":{"u":"cr1"}}`, "cr1"},
		{`{"userCredential":"cr2"}`, "cr2"},
		{`{"pass":"pw1"}`, "pw1"},
		{`{"pin":"4321"}`, "4321"},
		{`{"userPIN":"4322"}`, "4322"},
		{`{"bearer":"br1"}`, "br1"},
		{`{"Authorization":"Bearer br2"}`, "br2"},
		{`[{"name":"token","value":"nv1"}]`, "nv1"},
		{`[{"key":"apiKey","value":{"v":"nv2"}}]`, "nv2"},
		{`{"payload":"{\"token\":\"emb1\"}"}`, "emb1"},
		{`{"payload":"[{\"name\":\"secret\",\"value\":\"emb2\"}]"}`, "emb2"},
		{`{"payload":"{\"token\": \"emb3\"}"}`, "emb3"},
		{`{"payload":"{\n  \"token\": \"emb4\",\n  \"n\": [1, 2]\n}\n"}`, "emb4"},
		{`{"payload":"  {\"token\":\"emb5\"}"}`, "emb5"},
		{`{"payload":"[ {\"name\": \"token\", \"value\": \"emb6\"} ]"}`, "emb6"},
		{`{"payload":"{\"outer\": {\"password\": \"emb7\", \"p\": \"` + realHome + `\"}}"}`, "alice"},
		{`{"payload":"{\"token\":\"dup1\",\"token\":\"dup2\", \"n\": 1.5}"}`, "dup"},
		{`{"u":"https://x/#access_token=frag1"}`, "frag1"},
		{`{"u":"https://x/?a=1#id_token=frag2"}`, "frag2"},
		{`{"h":"Authorization: Bearer br3"}`, "br3"},
		{`{"h":"curl -H 'authorization: bearer   br4' x"}`, "br4"},
	}
	for _, c := range cases {
		if out := redactJSON(t, r, c[0]); strings.Contains(out, c[1]) {
			t.Errorf("secret %q survives in %s", c[1], out)
		}
	}
	// Known-good: names that merely contain the letters keep their values.
	for _, keep := range []string{`{"mapping":"keepme"}`, `{"Bypass":"keepme"}`, `{"spinner":"keepme"}`} {
		if out := redactJSON(t, r, keep); !strings.Contains(out, "keepme") {
			t.Errorf("over-redacted %s -> %s", keep, out)
		}
	}
	// Embedded JSON that is not an object or array stays a plain string.
	if out := redactJSON(t, r, `{"note":"{not json"}`); !strings.Contains(out, "{not json") {
		t.Errorf("non-JSON string altered: %s", out)
	}
}

// m6: URL-escaped spellings of a name leak as surely as the plain one.
func TestEscapedNamesAreRedacted(t *testing.T) {
	r, err := New(Options{UserNames: []string{"Alice"}, HostNames: []string{"Alice's MacBook Pro"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{
		"vnc://Alice%27s%20MacBook%20Pro.local",
		"vnc://alice%27s%20macbook%20pro.local",
		"smb://host/?q=Alice%27s+MacBook+Pro",
		"file://" + realHome + "/x",
		"x%2FUsers%2Fbob%2Ffile",
	} {
		got := r.String(in)
		for _, leak := range []string{"MacBook", "macbook", "Alice", "alice", "bob"} {
			if strings.Contains(got, leak) {
				t.Errorf("String(%q) = %q still contains %q", in, got, leak)
			}
		}
	}
}

// ExportFixture pseudonymizes profile, page and action UUIDs consistently in
// manifests and in folder names, upper-case folders staying upper-case and
// lower-case references lower-case. Relabeling must be hash-neutral, so the
// export hashes the same as the source after the same non-UUID redaction.
func TestExportPseudonymizesUUIDsConsistently(t *testing.T) {
	src := fixture.CopyOf(personal(), "real")
	p, err := profile.Load(src.FS(), src.Folder())
	if err != nil {
		t.Fatal(err)
	}
	// Source hash after the same redaction but with the UUIDs left alone.
	wantFS := fstest.MapFS{}
	ref := newR(t)
	for rel, data := range p.Files() {
		if strings.HasSuffix(rel, "manifest.json") {
			doc, err := jsondoc.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			data = ref.Value(doc).Encode()
		} else {
			data = SyntheticPNG(data)
		}
		wantFS["REF.sdProfile/"+rel] = &fstest.MapFile{Data: data}
	}
	refProfile, err := profile.Load(wantFS, "REF.sdProfile")
	if err != nil {
		t.Fatal(err)
	}
	wantHash, err := normhash.Hash(refProfile)
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	folder := strings.ToUpper(src.ID) + ".sdProfile"
	if err := ExportFixture(p, newR(t), "", out, folder); err != nil {
		t.Fatal(err)
	}
	var realIDs []string
	realIDs = append(realIDs, src.ID, src.Current, src.Default.ID)
	for _, pg := range src.Pages {
		realIDs = append(realIDs, pg.ID)
		for _, b := range pg.Buttons {
			realIDs = append(realIDs, b.ActionID)
		}
	}
	err = filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(out, path)
		blob := strings.ToLower(rel)
		if !d.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			blob += "\n" + strings.ToLower(string(data))
		}
		for _, id := range realIDs {
			if strings.Contains(blob, strings.ToLower(id)) {
				t.Errorf("%s still carries real id %s", rel, id)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 1 || entries[0].Name() != strings.ToUpper(entries[0].Name()[:len(entries[0].Name())-len(".sdProfile")])+".sdProfile" {
		t.Fatalf("exported profile folder is not upper-case UUID.sdProfile: %v", entries)
	}
	exported, err := profile.Load(os.DirFS(out), entries[0].Name())
	if err != nil {
		t.Fatalf("pseudonymized export does not load: %v", err)
	}
	got, err := normhash.Hash(exported)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantHash {
		t.Fatalf("pseudonymizing UUIDs changed the hash: %s != %s", got, wantHash)
	}
	// Two runs give the same names.
	out2 := t.TempDir()
	if err := ExportFixture(p, newR(t), "", out2, folder); err != nil {
		t.Fatal(err)
	}
	var names1, names2 []string
	for _, o := range []struct {
		dir string
		dst *[]string
	}{{out, &names1}, {out2, &names2}} {
		filepath.WalkDir(o.dir, func(path string, d fs.DirEntry, err error) error {
			rel, _ := filepath.Rel(o.dir, path)
			*o.dst = append(*o.dst, rel)
			return err
		})
	}
	if strings.Join(names1, "|") != strings.Join(names2, "|") {
		t.Fatalf("export names are not deterministic:\n%v\n%v", names1, names2)
	}
}
