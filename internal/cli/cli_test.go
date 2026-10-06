// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/observe"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/doctor"
	"github.com/csmarshall/schrodeck/internal/host"
	"github.com/csmarshall/schrodeck/internal/ports"
	"github.com/csmarshall/schrodeck/internal/ports/fake"
)

var update = flag.Bool("update", false, "rewrite golden files from current output")

// enumerated is a host.DeckLoader over a fake host's records and profiles,
// using the same decks.Enumerate the macOS connector uses. extra stands for a
// profile that failed to load in the deck read only (the two reads raced).
type enumerated struct {
	h     *host.Host
	extra []error
}

var _ host.DeckLoader = enumerated{}

func (e enumerated) Decks() ([]ports.Deck, error) {
	ds, _, err := e.DecksWithLoadErrors()
	return ds, err
}

func (e enumerated) DecksWithLoadErrors() ([]ports.Deck, []error, error) {
	recs, err := e.h.Prefs.DeviceRecords()
	if err != nil {
		return nil, nil, err
	}
	res, err := profile.LoadAll(os.DirFS(e.h.Paths.ProfilesDir()))
	if err != nil {
		return nil, nil, err
	}
	return decks.Enumerate(recs, res.Profiles, map[[2]int]ports.Geometry{{4057, 143}: {Columns: 8, Rows: 4}}), append(res.Errors, e.extra...), nil
}

// decksOnly hides DecksWithLoadErrors, as a connector without it would.
type decksOnly struct{ e enumerated }

func (d decksOnly) Decks() ([]ports.Deck, error) { return d.e.Decks() }

// fakeHost is a computer with the fixture XL profile installed.
func fakeHost(t *testing.T) *host.Host {
	t.Helper()
	paths := fake.Paths{Root: t.TempDir()}
	if err := fixture.WriteTo(paths.ProfilesDir(), fixture.XL().FS()); err != nil {
		t.Fatal(err)
	}
	h := &host.Host{
		Paths:    paths,
		Identity: fake.HostIdentity{Hardware: "HW-TEST-0001", User: "alice", Friendly: "test mac"},
		Prefs: fake.Prefs{
			Version:  "7.5.1",
			Selected: map[string]string{fixture.Device: fixture.XL().ID},
			Records:  []map[string]any{{decks.RecordKey: fixture.Device, "DeviceName": ""}},
		},
		App:       fake.NewApp(paths.ProfilesDir()),
		HostNames: []string{"test mac"},
		Now:       func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) },
		Sleep:     func(time.Duration) {},
	}
	h.Decks = enumerated{h: h}
	return h
}

func testEnv(logTo io.Writer, h *host.Host) (Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	if logTo == nil {
		logTo = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(logTo, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return Env{Stdout: &out, Stderr: &errb, Version: "test", Logger: logger, Host: h}, &out, &errb
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("stdout\n%s\nwant (golden %s)\n%s", got, path, want)
	}
}

func TestGoldenJSON(t *testing.T) {
	cases := []struct {
		golden   string
		args     []string
		withHost bool
		wantCode int
	}{
		{"version", []string{"version", "--json"}, false, ExitOK},
		{"status", []string{"status", "--json"}, false, ExitOK},
		{"doctor-nohost", []string{"doctor", "--json"}, false, ExitFail},
		{"status-host", []string{"status", "--json"}, true, ExitOK},
		{"inventory", []string{"inventory", "--json"}, true, ExitOK},
		{"doctor-unaccepted", []string{"doctor", "--json"}, true, ExitFail},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			var h *host.Host
			if c.withHost {
				h = fakeHost(t)
			}
			env, out, errb := testEnv(nil, h)
			if code := Run(context.Background(), c.args, env); code != c.wantCode {
				t.Fatalf("exit %d, want %d; stderr %q", code, c.wantCode, errb.String())
			}
			golden(t, c.golden, out.Bytes())
		})
	}
}

func TestDoctorAcceptFingerprint(t *testing.T) {
	h := fakeHost(t)
	env, out, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitOK {
		t.Fatalf("doctor --accept-fingerprint: exit %d\n%s", code, out.String())
	}
	golden(t, "doctor-accepted", out.Bytes())
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor"}, env); code != ExitOK {
		t.Fatal("doctor still fails after the fingerprint was accepted")
	}
	// Known-bad: a new manifest key must make doctor fail again.
	manifest := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "manifest.json")
	b, _ := os.ReadFile(manifest)
	if err := os.WriteFile(manifest, bytes.Replace(b, []byte(`"Name":`), []byte(`"NewField":1,"Name":`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	env, out, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor"}, env); code != ExitFail {
		t.Fatal("doctor passed a changed format fingerprint")
	}
	if !strings.Contains(out.String(), "new key path: manifest:NewField") {
		t.Fatalf("doctor did not name the new key:\n%s", out.String())
	}
}

func TestAcceptFingerprintRefusedWhileOtherChecksFail(t *testing.T) {
	// Known-bad P7: an unexpected file inside the profile makes the loader
	// refuse it. Accepting now would record a fingerprint that leaves the
	// profile out, so doctor must refuse and write nothing.
	h := fakeHost(t)
	stray := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "stray.bin")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := fixture.CopyOf(fixture.XL(), "second")
	if err := fixture.WriteTo(h.Paths.ProfilesDir(), second.FS()); err != nil {
		t.Fatal(err)
	}
	env, out, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitFail {
		t.Fatalf("exit %d, want %d", code, ExitFail)
	}
	if !strings.Contains(out.String(), `"accept_refused":`) || strings.Contains(out.String(), `"accepted_now":true`) {
		t.Fatalf("accept was not refused:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "known-fingerprints.json")); !os.IsNotExist(err) {
		t.Fatal("a refused accept still wrote the known-good set")
	}
	// Known-good control: once the install is clean, the same command accepts.
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}
	env, out, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitOK {
		t.Fatalf("clean install: exit %d\n%s", code, out.String())
	}
}

func TestInventoryRedactsDeviceIDsByDefault(t *testing.T) {
	h := fakeHost(t)
	real := "@(1)[4057/143/" + "AB12" + "CD34EF" + "]"
	manifest := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "manifest.json")
	b, _ := os.ReadFile(manifest)
	if err := os.WriteFile(manifest, bytes.Replace(b, []byte(fixture.Device), []byte(real), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	env, out, _ := testEnv(nil, h)
	Run(context.Background(), []string{"inventory", "--json"}, env)
	if strings.Contains(out.String(), "CD34EF") {
		t.Fatal("inventory printed a deck serial without --show-ids")
	}
	env, out, _ = testEnv(nil, h)
	Run(context.Background(), []string{"inventory", "--json", "--show-ids"}, env)
	if !strings.Contains(out.String(), "CD34EF") {
		t.Fatal("--show-ids did not show the id")
	}
}

func TestInventoryReportsUnmatched(t *testing.T) {
	h := fakeHost(t)
	prefs := h.Prefs.(fake.Prefs)
	other := "@(1)[4057/99/" + "ZZ98" + "YY76" + "]"
	prefs.Records = append(prefs.Records, map[string]any{decks.RecordKey: other})
	h.Prefs = prefs
	env, out, _ := testEnv(nil, h)
	Run(context.Background(), []string{"inventory", "--json"}, env)
	var doc struct {
		Data struct {
			Unmatched *struct {
				Profiles []string `json:"profiles"`
				Decks    []string `json:"decks"`
			} `json:"unmatched"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	u := doc.Data.Unmatched
	if u == nil || len(u.Decks) != 1 || len(u.Profiles) != 0 {
		t.Fatalf("unmatched = %+v\n%s", u, out.String())
	}
	if strings.Contains(out.String(), "YY76") || u.Decks[0] != "@(1)[4057/99/<deck>]" {
		t.Fatalf("unmatched deck key not redacted: %q", u.Decks[0])
	}
}

func TestObserveStartStop(t *testing.T) {
	h := fakeHost(t)
	env, _, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-title"}, env); code != ExitOK {
		t.Fatalf("start: exit %d %s", code, errb.String())
	}
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-title"}, env); code != ExitFail {
		t.Fatal("a second start of the same observation was allowed")
	}
	// The person's action in the app: rename button 1,0's title.
	edited := fixture.XL()
	edited.Pages[0].Buttons[1].Title = "Paste"
	if err := os.RemoveAll(h.Paths.ProfilesDir()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.WriteTo(h.Paths.ProfilesDir(), edited.FS()); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "obs", "u0-title.md")
	env, out, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-title", "--out", report}, env); code != ExitOK {
		t.Fatalf("stop: exit %d %s", code, errb.String())
	}
	md, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "key 1,0") || !strings.Contains(string(md), `"Paste"`) || !bytes.Equal(md, out.Bytes()) {
		t.Fatalf("report:\n%s", md)
	}
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "observe", "u0-title")); !os.IsNotExist(err) {
		t.Fatal("unredacted snapshots were left behind after stop")
	}
}

func TestFixtureExport(t *testing.T) {
	h := fakeHost(t)
	out := filepath.Join(t.TempDir(), "fx")
	env, stdout, errb := testEnv(nil, h)
	code := Run(context.Background(), []string{"fixture", "export", "--profile", fixture.XL().Folder(), "--out", out, "--name", "EXPORTED.sdProfile", "--json"}, env)
	if code != ExitOK {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	var doc struct {
		Data fixtureData `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Data.Folder != "EXPORTED.sdProfile" || len(doc.Data.Strings) == 0 {
		t.Fatalf("data = %+v", doc.Data)
	}
	if _, err := os.Stat(filepath.Join(out, "EXPORTED.sdProfile", "manifest.json")); err != nil {
		t.Fatal(err)
	}
	env, _, _ = testEnv(nil, h)
	inside := filepath.Join(h.Paths.ProfilesDir(), "x")
	if code := Run(context.Background(), []string{"fixture", "export", "--profile", fixture.XL().Folder(), "--out", inside}, env); code == ExitOK {
		t.Fatal("export into the app's data was allowed")
	}
	// Known-bad: --name walking out of --out and into the app's data.
	escape := filepath.Join("..", "app", "ProfilesV3", "N.sdProfile")
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"fixture", "export", "--profile", fixture.XL().Folder(), "--out", filepath.Join(filepath.Dir(h.Paths.AppDataRoot()), "fx2"), "--name", escape}, env); code != ExitUsage {
		t.Fatalf("a ../ --name was not refused as a usage error: exit %d", code)
	}
	// Known-bad: --out is a symlink into the app's data.
	link := filepath.Join(t.TempDir(), "looks-safe")
	if err := os.Symlink(h.Paths.ProfilesDir(), link); err != nil {
		t.Fatal(err)
	}
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"fixture", "export", "--profile", fixture.XL().Folder(), "--out", link}, env); code != ExitUsage {
		t.Fatalf("export through a symlink into the app's data: exit %d", code)
	}
	assertAppDataUntouched(t, h)
}

// assertAppDataUntouched fails if the profiles directory holds anything but
// the one fixture profile the fake host was created with.
func assertAppDataUntouched(t *testing.T, h *host.Host) {
	t.Helper()
	entries, err := os.ReadDir(h.Paths.ProfilesDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fixture.XL().Folder() {
		t.Fatalf("the app's profiles directory was written to: %v", entries)
	}
}

func TestObserveOutRefusesAppData(t *testing.T) {
	h := fakeHost(t)
	env, _, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-guard"}, env); code != ExitOK {
		t.Fatal("start failed")
	}
	inside := filepath.Join(h.Paths.ProfilesDir(), "report.md")
	env, _, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-guard", "--out", inside}, env); code != ExitUsage {
		t.Fatalf("observe stop --out inside the app's data: exit %d %s", code, errb.String())
	}
	assertAppDataUntouched(t, h)
	// The refused stop must not have consumed the observation.
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "observe", "u0-guard")); err != nil {
		t.Fatal("a refused stop deleted the started observation")
	}
}

func TestJSONStdoutIsOneLineEvenWithDebugLogs(t *testing.T) {
	for _, h := range []*host.Host{nil, fakeHost(t)} {
		var logs bytes.Buffer
		env, out, _ := testEnv(&logs, h)
		Run(context.Background(), []string{"doctor", "--json"}, env)
		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(lines) != 1 {
			t.Fatalf("stdout has %d lines, want 1: %q", len(lines), out.String())
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &doc); err != nil {
			t.Fatalf("stdout is not one JSON document: %v", err)
		}
		if logs.Len() == 0 {
			t.Fatal("debug logs were expected on the log writer")
		}
	}
}

func TestUsageErrors(t *testing.T) {
	cases := [][]string{
		nil,
		{"nope"},
		{"--json", "status"},
		{"status", "--bogus"},
		{"status", "extra-arg"},
		{"observe"},
		{"observe", "start"},
		{"observe", "start", "Bad Name"},
		{"observe", "pause", "x"},
		{"fixture", "export"},
		{"fixture", "export", "--profile", "x.sdProfile", "--out", "/tmp/y", "--name", "nosuffix"},
	}
	for _, args := range cases {
		env, out, errb := testEnv(nil, fakeHost(t))
		code := Run(context.Background(), args, env)
		if code != ExitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, ExitUsage)
		}
		if out.Len() != 0 {
			t.Errorf("%q: usage errors must not print to stdout, got %q", args, out.String())
		}
		if errb.Len() == 0 {
			t.Errorf("%q: usage error printed no message", args)
		}
	}
}

func TestFlagsMayFollowArguments(t *testing.T) {
	env, out, _ := testEnv(nil, fakeHost(t))
	if code := Run(context.Background(), []string{"inventory", "--show-ids", "--json"}, env); code != ExitOK {
		t.Fatal(code)
	}
	if !strings.HasPrefix(out.String(), `{"schema_version":1`) {
		t.Fatalf("--json after another flag was not honored: %q", out.String())
	}
}

func TestHelpExitsZero(t *testing.T) {
	env, _, errb := testEnv(nil, nil)
	if code := Run(context.Background(), []string{"help"}, env); code != ExitOK {
		t.Fatalf("help: exit %d", code)
	}
	for _, name := range []string{"version", "status", "inventory", "doctor", "observe", "fixture"} {
		if !strings.Contains(errb.String(), name) {
			t.Errorf("help text lacks command %q", name)
		}
	}
}

func TestTextOutput(t *testing.T) {
	env, out, _ := testEnv(nil, fakeHost(t))
	if code := Run(context.Background(), []string{"status"}, env); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out.String(), "schrodeck test\nhost 7db4c9ba0faa") {
		t.Fatalf("status text = %q", out.String())
	}
}

// Contract E's documented schema_version must equal the code's.
func TestSchemaVersionDocumented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "cli-json.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("Current `schema_version`: **%d**", SchemaVersion)
	if !strings.Contains(string(doc), want) {
		t.Fatalf("docs/contracts/cli-json.md does not contain %q", want)
	}
}

// raced is a profile folder that only the deck read saw fail to load.
const racedFolder = "BBBBBBBB-0000-4000-8000-000000000002.sdProfile"

func racedHost(t *testing.T, withLoader bool) *host.Host {
	t.Helper()
	h := fakeHost(t)
	e := enumerated{h: h, extra: []error{&profile.FolderError{Folder: racedFolder, Err: errors.New("manifest.json: unexpected end of JSON input")}}}
	h.Decks = e
	if !withLoader {
		h.Decks = decksOnly{e}
	}
	return h
}

func TestDeckReadLoadErrorsAreReported(t *testing.T) {
	h := racedHost(t, true)
	env, out, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"inventory", "--json"}, env); code != ExitFail {
		t.Fatalf("inventory with a profile that did not load: exit %d, want %d", code, ExitFail)
	}
	if !strings.Contains(out.String(), racedFolder+": manifest.json") {
		t.Fatalf("inventory did not report the deck read's load error:\n%s", out.String())
	}
	env, out, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitFail {
		t.Fatalf("doctor with a profile that did not load: exit %d", code)
	}
	if !strings.Contains(out.String(), `"accept_refused":"1 profile(s) did not load`) {
		t.Fatalf("doctor accepted a fingerprint while a profile did not load:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "known-fingerprints.json")); !os.IsNotExist(err) {
		t.Fatal("a refused accept wrote the known-good set")
	}
	// Control: through Decks alone the raced error is invisible, so the
	// assertions above can only pass by way of DecksWithLoadErrors.
	env, out, _ = testEnv(nil, racedHost(t, false))
	if code := Run(context.Background(), []string{"inventory", "--json"}, env); code != ExitOK || strings.Contains(out.String(), racedFolder) {
		t.Fatalf("control: exit %d\n%s", code, out.String())
	}
}

func TestObserveStopDeletesSnapshotsWhenItFails(t *testing.T) {
	h := fakeHost(t)
	env, _, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-broken"}, env); code != ExitOK {
		t.Fatalf("start: exit %d %s", code, errb.String())
	}
	// A manifest that loads but lacks Pages: Compare cannot diff it.
	manifest := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"Device":{"Model":"20GAT9902","UUID":"`+fixture.Device+`"},"Name":"Fixture XL","Version":"3.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "u0-broken.md")
	env, _, errb = testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-broken", "--out", report}, env); code != ExitFail {
		t.Fatalf("stop on an undiffable profile: exit %d, want %d; stderr %q", code, ExitFail, errb.String())
	}
	if !strings.Contains(errb.String(), "Pages") {
		t.Fatalf("stop did not fail where expected (in Compare): %q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "observe", "u0-broken")); !os.IsNotExist(err) {
		t.Fatal("a failed stop left the unredacted snapshots behind")
	}
	if _, err := os.Stat(report); !os.IsNotExist(err) {
		t.Fatal("a failed stop wrote a report")
	}
}

func TestObserveReportNamesDevicesByKey(t *testing.T) {
	h := fakeHost(t)
	env, _, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-prefs"}, env); code != ExitOK {
		t.Fatal("start failed")
	}
	prefs := h.Prefs.(fake.Prefs)
	prefs.Records = []map[string]any{
		{decks.RecordKey: decks.VirtualKey, "DeviceName": "virtual"},
		{decks.RecordKey: fixture.Device, "DeviceName": "renamed"},
	}
	h.Prefs = prefs
	env, out, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-prefs"}, env); code != ExitOK {
		t.Fatalf("stop: exit %d %s", code, errb.String())
	}
	// Keyed by device: the physical deck's row names its key, and adding the
	// virtual deck (sorted first) did not turn it into a list-index change.
	if !strings.Contains(out.String(), `Devices.["`+fixture.Device+`"].DeviceName | modified | "" | "renamed"`) {
		t.Fatalf("report does not name the device by key:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Devices.0") || strings.Contains(out.String(), "Devices[") {
		t.Fatalf("report addresses devices by list index:\n%s", out.String())
	}
}

func TestObserveOutRefusesExistingAndEscapingTargets(t *testing.T) {
	h := fakeHost(t)
	env, _, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-out"}, env); code != ExitOK {
		t.Fatal("start failed")
	}
	// Known-bad: a symlink into the app's data followed by "..": lexically it
	// is outside, physically it is the app data root.
	link := filepath.Join(t.TempDir(), "looks-safe")
	if err := os.Symlink(h.Paths.ProfilesDir(), link); err != nil {
		t.Fatal(err)
	}
	env, _, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-out", "--out", link + "/../report.md"}, env); code != ExitUsage {
		t.Fatalf("--out through a symlink and ..: exit %d %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(h.Paths.AppDataRoot(), "report.md")); !os.IsNotExist(err) {
		t.Fatal("the report landed in the app data root")
	}
	// Known-bad: an existing file is never overwritten, and refusing it does
	// not consume the observation.
	existing := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-out", "--out", existing}, env); code != ExitUsage {
		t.Fatalf("--out onto an existing file: exit %d", code)
	}
	if b, _ := os.ReadFile(existing); string(b) != "keep" {
		t.Fatal("an existing --out file was overwritten")
	}
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "observe", "u0-out")); err != nil {
		t.Fatal("a refused stop consumed the observation")
	}
	// Control: a fresh path outside the app's data is written.
	fresh := filepath.Join(t.TempDir(), "new", "report.md")
	env, _, errb = testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-out", "--out", fresh}, env); code != ExitOK {
		t.Fatalf("control: exit %d %s", code, errb.String())
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal(err)
	}
	assertAppDataUntouched(t, h)
}

func TestObserveRedactsADeckPresentOnlyBefore(t *testing.T) {
	h := fakeHost(t)
	gone := "QQ11" + "RR22"
	prefs := h.Prefs.(fake.Prefs)
	before := prefs
	before.Records = append(append([]map[string]any{}, prefs.Records...), map[string]any{decks.RecordKey: "@(1)[4057/99/" + gone + "]", "Serial": gone})
	h.Prefs = before
	env, _, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-unplug"}, env); code != ExitOK {
		t.Fatal("start failed")
	}
	// The deck is unplugged: its record is gone by the time of stop, so only
	// the start snapshot knows its serial.
	h.Prefs = prefs
	env, out, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-unplug"}, env); code != ExitOK {
		t.Fatalf("stop: exit %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Serial | removed") {
		t.Fatalf("the removed deck's record is not in the report:\n%s", out.String())
	}
	if strings.Contains(strings.ToUpper(out.String()), gone) {
		t.Fatalf("the report shows the unplugged deck's serial:\n%s", out.String())
	}
}

// brokenPrefs fails every device-record read, as a prefs read can at any time.
type brokenPrefs struct{ fake.Prefs }

func (brokenPrefs) DeviceRecords() ([]map[string]any, error) {
	return nil, errors.New("fake prefs: unreadable")
}

func TestReportRedactorUsesBothSnapshots(t *testing.T) {
	h := fakeHost(t)
	beforeOnly, afterOnly := "BB11"+"CC22", "DD33"+"EE44"
	snap := func(serial string) *observe.Snapshot {
		prefs, err := jsondoc.FromAny(map[string]any{"Devices": map[string]any{"@(1)[4057/99/" + serial + "]": map[string]any{}}})
		if err != nil {
			t.Fatal(err)
		}
		return &observe.Snapshot{Profiles: map[string]*profile.Profile{}, Prefs: prefs}
	}
	// The host cannot be read again, so only the snapshots can supply serials.
	h.Prefs = brokenPrefs{h.Prefs.(fake.Prefs)}
	env, _, _ := testEnv(nil, h)
	r, err := reportRedactor(env, snap(beforeOnly), snap(afterOnly))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{beforeOnly, afterOnly} {
		if got := r.String("bare " + s); strings.Contains(got, s) {
			t.Errorf("serial %s left in %q", s, got)
		}
	}
}

func TestObserveRedactsADeckPresentOnlyAfter(t *testing.T) {
	h := fakeHost(t)
	env, _, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-plug"}, env); code != ExitOK {
		t.Fatal("start failed")
	}
	added := "FF55" + "GG66"
	prefs := h.Prefs.(fake.Prefs)
	prefs.Records = append(append([]map[string]any{}, prefs.Records...), map[string]any{decks.RecordKey: "@(1)[4057/99/" + added + "]", "Serial": added})
	h.Prefs = prefs
	env, out, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-plug"}, env); code != ExitOK {
		t.Fatalf("stop: exit %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Serial | added") {
		t.Fatalf("the plugged-in deck's record is not in the report:\n%s", out.String())
	}
	if strings.Contains(strings.ToUpper(out.String()), added) {
		t.Fatalf("the report shows the new deck's serial:\n%s", out.String())
	}
}

func TestInventoryRedactsAppIdentifier(t *testing.T) {
	h := fakeHost(t)
	manifest := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "manifest.json")
	b, _ := os.ReadFile(manifest)
	app := "/Users/" + "alice" + "/Applications/Tool.app"
	if err := os.WriteFile(manifest, bytes.Replace(b, []byte(`"Name":`), []byte(`"AppIdentifier":"`+app+`","Name":`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	env, out, _ := testEnv(nil, h)
	Run(context.Background(), []string{"inventory", "--json"}, env)
	if !strings.Contains(out.String(), `"app_identifier":"/Users/<user>/Applications/Tool.app"`) {
		t.Fatalf("app_identifier not redacted:\n%s", out.String())
	}
}

func TestWrittenPathsAreRedacted(t *testing.T) {
	h := fakeHost(t)
	env, _, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-path"}, env); code != ExitOK {
		t.Fatal("start failed")
	}
	base := t.TempDir()
	env, out, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-path", "--json", "--out", filepath.Join(base, "alice-notes", "r.md")}, env); code != ExitOK {
		t.Fatalf("stop: exit %d %s", code, errb.String())
	}
	if strings.Contains(out.String(), "alice") || !strings.Contains(out.String(), "<user>-notes") {
		t.Fatalf("observe written path not redacted:\n%s", out.String())
	}
	env, out, errb = testEnv(nil, h)
	if code := Run(context.Background(), []string{"fixture", "export", "--json", "--profile", fixture.XL().Folder(), "--out", filepath.Join(base, "alice-fx")}, env); code != ExitOK {
		t.Fatalf("fixture: exit %d %s", code, errb.String())
	}
	if strings.Contains(out.String(), "alice") || !strings.Contains(out.String(), "<user>-fx") {
		t.Fatalf("fixture out path not redacted:\n%s", out.String())
	}
}

// lockObserveDir makes the observe/ dir unwritable so its entries cannot be
// removed, and restores it at cleanup.
func lockObserveDir(t *testing.T, h *host.Host) {
	t.Helper()
	dir := filepath.Join(h.Paths.StateDir(), "observe")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		f.Close()
		t.Skip("cannot make a directory unwritable here (running as root?)")
	}
}

func TestObserveStopReportsSnapshotsItCouldNotDelete(t *testing.T) {
	// Success path: the report went out, so exit 0, with a warning naming
	// where the raw snapshots remain.
	h := fakeHost(t)
	env, _, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-stuck"}, env); code != ExitOK {
		t.Fatal("start failed")
	}
	lockObserveDir(t, h)
	env, _, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-stuck"}, env); code != ExitOK {
		t.Fatalf("stop: exit %d %s", code, errb.String())
	}
	want := "unredacted snapshots remain in " + filepath.Join(h.Paths.StateDir(), "observe", "u0-stuck")
	if !strings.Contains(errb.String(), want) {
		t.Fatalf("no warning about the leftover snapshots: %q", errb.String())
	}
	// Failure path: both the failure and the removal error are reported.
	h = fakeHost(t)
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-stuck"}, env); code != ExitOK {
		t.Fatal("start failed")
	}
	manifest := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"Name":"Fixture XL","Version":"3.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	lockObserveDir(t, h)
	env, _, errb = testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-stuck"}, env); code != ExitFail {
		t.Fatalf("stop: exit %d", code)
	}
	if !strings.Contains(errb.String(), "Pages") || !strings.Contains(errb.String(), "could not be deleted, remove "+filepath.Join(h.Paths.StateDir(), "observe", "u0-stuck")) {
		t.Fatalf("stderr lacks the failure or the removal error: %q", errb.String())
	}
}

// insideState puts schrodeck's state dir inside the app data root.
type insideState struct{ fake.Paths }

func (p insideState) StateDir() string { return filepath.Join(p.AppDataRoot(), "schrodeck") }

func TestStateDirInsideAppDataIsRefused(t *testing.T) {
	h := fakeHost(t)
	h.Paths = insideState{h.Paths.(fake.Paths)}
	env, out, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitFail {
		t.Fatalf("doctor --accept-fingerprint: exit %d\n%s", code, out.String())
	}
	env, _, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-state"}, env); code != ExitFail || !strings.Contains(errb.String(), "state path is refused") {
		t.Fatalf("observe start: exit %d %q", code, errb.String())
	}
	if _, err := os.Stat(h.Paths.StateDir()); !os.IsNotExist(err) {
		t.Fatal("schrodeck wrote state inside the app's data")
	}
}

func TestAcceptRefusedWhenAnotherCheckFailsWithEveryProfileLoaded(t *testing.T) {
	h := fakeHost(t)
	prefs := h.Prefs.(fake.Prefs)
	prefs.Version = "" // M5 fails; every profile still loads
	h.Prefs = prefs
	env, out, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitFail {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), `"accept_refused":"another check failed`) {
		t.Fatalf("accept not refused for a failing M5:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), doctor.KnownFile)); !os.IsNotExist(err) {
		t.Fatal("a refused accept wrote the known-good set")
	}
}

func TestObserveSettleStopsOnInterrupt(t *testing.T) {
	h := fakeHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleeps := 0
	h.Sleep = func(time.Duration) { sleeps++; cancel() } // Ctrl-C during the first wait
	env, _, errb := testEnv(nil, h)
	if code := Run(ctx, []string{"observe", "start", "u0-int"}, env); code != ExitFail || !strings.Contains(errb.String(), "context canceled") {
		t.Fatalf("interrupted start: exit %d %q", code, errb.String())
	}
	if sleeps != 1 {
		t.Fatalf("settle waited %d times after the interrupt", sleeps)
	}
	if _, err := os.Stat(filepath.Join(h.Paths.StateDir(), "observe", "u0-int")); !os.IsNotExist(err) {
		t.Fatal("an interrupted start saved a snapshot")
	}
}

func TestCorruptKnownSetFailsFPAndCanBeReplaced(t *testing.T) {
	h := fakeHost(t)
	if err := os.MkdirAll(h.Paths.StateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Paths.StateDir(), doctor.KnownFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, out, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--json"}, env); code != ExitFail {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), `"id":"P1"`) || !strings.Contains(out.String(), `"id":"FP","contract":"C","tier":"read-only","status":"fail","detail":"the known-good fingerprint set cannot be read`) {
		t.Fatalf("corrupt known set did not fail FP alongside the other checks:\n%s", out.String())
	}
	env, out, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitOK {
		t.Fatalf("accept over a corrupt set: exit %d\n%s", code, out.String())
	}
	env, _, _ = testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor"}, env); code != ExitOK {
		t.Fatal("doctor still fails after the corrupt set was replaced")
	}
}

func TestObserveDirSymlinkedIntoAppDataIsRefused(t *testing.T) {
	// The reviewer's probe: StateDir itself is fine, but StateDir/observe is a
	// symlink into ProfilesV3, so the observation dir would land in the app's data.
	h := fakeHost(t)
	if err := os.MkdirAll(h.Paths.StateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(h.Paths.ProfilesDir(), filepath.Join(h.Paths.StateDir(), "observe")); err != nil {
		t.Fatal(err)
	}
	env, _, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "start", "u0-sym"}, env); code == ExitOK || !strings.Contains(errb.String(), "state path is refused") {
		t.Fatalf("start through a symlinked observe dir: exit %d %q", code, errb.String())
	}
	assertAppDataUntouched(t, h)
	// A stop must refuse too, never RemoveAll inside the app's data. Plant a
	// folder where the observation would be, as an earlier unguarded start did.
	planted := filepath.Join(h.Paths.ProfilesDir(), "u0-sym")
	if err := os.Mkdir(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	env, _, errb = testEnv(nil, h)
	if code := Run(context.Background(), []string{"observe", "stop", "u0-sym"}, env); code == ExitOK || !strings.Contains(errb.String(), "state path is refused") {
		t.Fatalf("stop through a symlinked observe dir: exit %d %q", code, errb.String())
	}
	if _, err := os.Stat(planted); err != nil {
		t.Fatal("stop removed a folder inside the app's data")
	}
}

func TestUnreadableKnownSetIsNeverReplaced(t *testing.T) {
	h := fakeHost(t)
	env, _, _ := testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint"}, env); code != ExitOK {
		t.Fatal("first accept failed")
	}
	known := filepath.Join(h.Paths.StateDir(), doctor.KnownFile)
	valid, err := os.ReadFile(known)
	if err != nil {
		t.Fatal(err)
	}
	// A new format fingerprint, so an accept would want to write.
	manifest := filepath.Join(h.Paths.ProfilesDir(), fixture.XL().Folder(), "manifest.json")
	b, _ := os.ReadFile(manifest)
	if err := os.WriteFile(manifest, bytes.Replace(b, []byte(`"Name":`), []byte(`"NewField":1,"Name":`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(known, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(known, 0o600) })
	if _, err := os.ReadFile(known); err == nil {
		t.Skip("cannot make a file unreadable here (running as root?)")
	}
	env, out, errb := testEnv(nil, h)
	if code := Run(context.Background(), []string{"doctor", "--accept-fingerprint", "--json"}, env); code != ExitFail || !strings.Contains(out.String(), "permission denied") {
		t.Fatalf("accept over an unreadable known set: exit %d\n%s%s", code, out.String(), errb.String())
	}
	if err := os.Chmod(known, 0o600); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(known); !bytes.Equal(after, valid) {
		t.Fatal("an unreadable but valid known set was replaced")
	}
}
