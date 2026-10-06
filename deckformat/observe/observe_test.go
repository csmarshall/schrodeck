// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package observe

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/redact"
	"github.com/csmarshall/schrodeck/deckformat/semdiff"
)

var (
	t0         = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	realSerial = "AB12" + "CD34EF"
	realDevice = "@(1)[4057/143/" + realSerial + "]"
)

func prefs(t *testing.T, preferred string) *jsondoc.Value {
	t.Helper()
	v, err := jsondoc.FromAny(map[string]any{"Devices": map[string]any{
		realDevice: map[string]any{"ESDProfilesInfo": map[string]any{"ESDProfilesPreferred": preferred}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func take(t *testing.T, fp fixture.Profile, pr *jsondoc.Value, at time.Time) *Snapshot {
	t.Helper()
	s, err := Take(fp.FS(), pr, "7.5.1", at)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func redactor(t *testing.T) *redact.Redactor {
	t.Helper()
	r, err := redact.New(redact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := take(t, fixture.XL(), prefs(t, "x"), t0)
	dir := filepath.Join(t.TempDir(), "before")
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	back, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if back.Digest() != s.Digest() || !back.TakenAt.Equal(t0) || back.AppVersion != "7.5.1" {
		t.Fatalf("round trip changed the snapshot")
	}
	if err := s.Save(dir); !errors.Is(err, ErrExists) {
		t.Fatalf("second Save into the same dir: %v", err)
	}
}

func TestSettle(t *testing.T) {
	a := take(t, fixture.XL(), nil, t0)
	edited := fixture.XL()
	edited.Name = "changed"
	b := take(t, edited, nil, t0)
	seq := []*Snapshot{a, b, b}
	i := 0
	next := func() (*Snapshot, error) { s := seq[i]; i++; return s, nil }
	got, err := Settle(next, func() {}, 5)
	if err != nil || got.Digest() != b.Digest() {
		t.Fatalf("Settle = %v, %v", got, err)
	}
	i = 0
	alternating := []*Snapshot{a, b, a, b}
	next = func() (*Snapshot, error) { s := alternating[i]; i++; return s, nil }
	if _, err := Settle(next, func() {}, 4); err == nil {
		t.Fatal("Settle accepted files that never stopped changing")
	}
}

func TestCompareReportsAndRedacts(t *testing.T) {
	before := take(t, fixture.XL(), prefs(t, "aaaa"), t0)
	edited := fixture.XL()
	edited.Pages[0].Buttons[1].Title = "Paste"
	after := take(t, edited, prefs(t, "bbbb"), t0.Add(time.Minute))
	rep, err := Compare("u0-title", before, after, redactor(t))
	if err != nil {
		t.Fatal(err)
	}
	md := rep.Markdown()
	for _, want := range []string{
		"# Observation: u0-title",
		"page 1 (profile-1/page/0) › key 1,0",
		`"Paste"`,
		"@(1)[4057/143/<deck>]",
		"| R?? | <topic> | **Observed** | `schrodeck observe u0-title`, app 7.5.1, 2026-10-02 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, realSerial) {
		t.Fatalf("report leaks the serial:\n%s", md)
	}
}

func TestReportsCarryNoRealIDs(t *testing.T) {
	before := take(t, fixture.XL(), nil, t0)
	edited := fixture.XL()
	edited.Pages[0].Buttons[1].Title = "Paste"
	edited.Pages[0].Buttons[1].ActionID = "11111111-2222-4333-8444-555555555555"
	after := take(t, edited, nil, t0.Add(time.Minute))
	rep, err := Compare("ids", before, after, redactor(t))
	if err != nil {
		t.Fatal(err)
	}
	md := rep.Markdown()
	if uuidPattern.MatchString(md) {
		t.Fatalf("report contains a raw UUID:\n%s", md)
	}
	// Equalities survive: the changed ActionID became uuid-N on both sides of
	// one row, and the page is named by its position.
	if !strings.Contains(md, "uuid-") || !strings.Contains(md, "profile-1/page/0") {
		t.Fatalf("pseudonyms missing:\n%s", md)
	}
	// Known-bad control: the same comparison without pseudonyms carries UUIDs,
	// so the check above can fail.
	raw, err := semdiff.Sets(before.Profiles, after.Profiles, semdiff.Raw)
	if err != nil {
		t.Fatal(err)
	}
	var rawText strings.Builder
	for _, c := range raw {
		rawText.WriteString(c.String() + "\n")
	}
	if !uuidPattern.MatchString(rawText.String()) {
		t.Fatal("control: the raw diff has no UUID, so this test proves nothing")
	}
}

func TestPseudonymsAreStableAndDistinct(t *testing.T) {
	ps := newPseudonyms()
	a := ps.String("x AAAAAAAA-0000-4000-8000-000000000001 y aaaaaaaa-0000-4000-8000-000000000001")
	if a != "x uuid-1 y uuid-1" {
		t.Fatalf("same id in two letter cases: %q", a)
	}
	if b := ps.String("BBBBBBBB-0000-4000-8000-000000000002"); b != "uuid-2" {
		t.Fatalf("second id: %q", b)
	}
}

func TestCellCutsCharactersNotBytes(t *testing.T) {
	long := strings.Repeat("é", 200) // two bytes each
	got := cell(long)
	if !utf8.ValidString(got) {
		t.Fatal("cell split a multi-byte character")
	}
	if n := utf8.RuneCountInString(got); n != cellRunes {
		t.Fatalf("cell kept %d characters, want %d", n, cellRunes)
	}
}

func TestNoChangeIsReportedAsSuch(t *testing.T) {
	s := take(t, fixture.XL(), nil, t0)
	rep, err := Compare("nothing", s, s, redactor(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Changes) != 0 || !strings.Contains(rep.EvidenceRow(), "no change on disk") {
		t.Fatalf("got %v / %s", rep.Changes, rep.EvidenceRow())
	}
}

func TestSaveNeverClobbers(t *testing.T) {
	s := take(t, fixture.XL(), nil, t0)
	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A pre-existing directory, even an empty one, is refused.
	existing := filepath.Join(root, "exists")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(existing); !errors.Is(err, ErrExists) {
		t.Fatalf("Save into an existing dir: %v", err)
	}
	// A symlink at the destination (dangling or not) is refused, not followed.
	link := filepath.Join(root, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(link); !errors.Is(err, ErrExists) {
		t.Fatalf("Save onto a symlink: %v", err)
	}
	// writeNew refuses an existing file, including a hard link to another.
	hard := filepath.Join(root, "hard")
	if err := os.Link(victim, hard); err != nil {
		t.Fatal(err)
	}
	if err := writeNew(hard, []byte("clobber")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("writeNew over a hard link: %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("victim was overwritten: %q", b)
	}
}

func settingsXL(t *testing.T, settings string) fixture.Profile {
	t.Helper()
	p := fixture.XL()
	p.Pages[0].Buttons[0].Settings = settings
	return p
}

func TestSecretNamedValuesNeverReachTheReport(t *testing.T) {
	const (
		oldTok, newTok = "sec-one-AAA", "sec-two-BBB"
		oldN, newN     = "nest-one-CCC", "nest-two-DDD"
		oldP, newP     = "pref-one-EEE", "pref-two-FFF"
	)
	mkPrefs := func(tok string) *jsondoc.Value {
		v, err := jsondoc.FromAny(map[string]any{"Token": tok, "Harmless": "same"})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := take(t, settingsXL(t, `{"apiToken":"`+oldTok+`","auth":{"inner":"`+oldN+`"},"label":"a"}`), mkPrefs(oldP), t0)
	after := take(t, settingsXL(t, `{"apiToken":"`+newTok+`","auth":{"inner":"`+newN+`"},"label":"b"}`), mkPrefs(newP), t0.Add(time.Minute))
	rep, err := Compare("secrets", before, after, redactor(t))
	if err != nil {
		t.Fatal(err)
	}
	out := rep.Markdown() + "\n" + rep.EvidenceRow()
	for _, leak := range []string{oldTok, newTok, oldN, newN, oldP, newP} {
		if strings.Contains(out, leak) {
			t.Errorf("report leaks %q:\n%s", leak, out)
		}
	}
	// The non-secret sibling is still reported, so blanking is not wholesale.
	if !strings.Contains(out, `"b"`) {
		t.Errorf("non-secret change vanished:\n%s", out)
	}
	// Known-bad control: the raw diff does carry every secret.
	raw, err := semdiff.Sets(before.Profiles, after.Profiles, semdiff.Raw)
	if err != nil {
		t.Fatal(err)
	}
	var rawText strings.Builder
	for _, c := range append(raw, semdiff.Values("app preferences", "prefs", before.Prefs, after.Prefs)...) {
		rawText.WriteString(c.String() + "\n")
	}
	for _, secret := range []string{oldTok, newTok, oldN, newN, oldP, newP} {
		if !strings.Contains(rawText.String(), secret) {
			t.Errorf("control: the raw diff lacks %q, so this test proves nothing:\n%s", secret, rawText.String())
		}
	}
}

func TestProfileNamesAreAliased(t *testing.T) {
	named := fixture.XL()
	named.Name = "Personal Deck"
	edited := fixture.XL()
	edited.Name = "Personal Deck"
	edited.Pages[0].Buttons[1].Title = "Paste"
	extra := fixture.CopyOf(fixture.XL(), "extra")
	extra.Name = "Hidden Profile"
	fsAfter := fixture.Merge(edited.FS(), extra.FS())
	b, err := Take(named.FS(), nil, "7.5.1", t0)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Take(fsAfter, nil, "7.5.1", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Compare("names", b, a, redactor(t))
	if err != nil {
		t.Fatal(err)
	}
	out := rep.Markdown() + rep.EvidenceRow()
	for _, name := range []string{"Personal Deck", "Hidden Profile"} {
		if strings.Contains(out, name) {
			t.Errorf("report carries profile name %q:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "| profile-1 |") || !strings.Contains(out, "added") {
		t.Errorf("expected profile-N aliases and the added profile:\n%s", out)
	}
	// Known-bad control: the raw change set does carry the names.
	raw, err := semdiff.Sets(b.Profiles, a.Profiles, semdiff.Raw)
	if err != nil {
		t.Fatal(err)
	}
	var rawText strings.Builder
	for _, c := range raw {
		rawText.WriteString(c.String() + "\n")
	}
	if !strings.Contains(rawText.String(), "Personal Deck") || !strings.Contains(rawText.String(), "Hidden Profile") {
		t.Fatalf("control: the raw diff has no profile name:\n%s", rawText.String())
	}
}

func TestLoadReturnsPrefsReadErrors(t *testing.T) {
	s := take(t, fixture.XL(), nil, t0)
	dir := filepath.Join(t.TempDir(), "snap")
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	// Absent prefs.json is fine...
	if _, err := Load(dir); err != nil {
		t.Fatalf("Load without prefs: %v", err)
	}
	// ...but a prefs.json that exists and cannot be read is an error.
	if err := os.Mkdir(filepath.Join(dir, "prefs.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load swallowed a prefs.json read error")
	}
}

func TestFailedSaveRemovesItsDirectory(t *testing.T) {
	s := take(t, fixture.XL(), prefs(t, "x"), t0)
	dir := filepath.Join(t.TempDir(), "snap")
	real := writeFile
	calls := 0
	writeFile = func(path string, data []byte) error {
		calls++
		if calls == 3 {
			return errors.New("disk full")
		}
		return real(path, data)
	}
	t.Cleanup(func() { writeFile = real })
	if err := s.Save(dir); err == nil {
		t.Fatal("Save ignored the write failure")
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed Save left %s behind: %v", dir, err)
	}
	writeFile = real
	if err := s.Save(dir); err != nil {
		t.Fatalf("retry after a failed Save: %v", err)
	}
}
