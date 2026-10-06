// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/probe"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/host"
	"github.com/csmarshall/schrodeck/internal/ports/fake"
)

func TestKnownRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	k, err := LoadKnown(dir)
	if err != nil || len(k.Accepted) != 0 {
		t.Fatalf("missing file: %v %v", k, err)
	}
	k.Accepted = append(k.Accepted, Accepted{Digest: "abc", AppVersion: "7.5.1", AcceptedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	if err := SaveKnown(dir, k); err != nil {
		t.Fatal(err)
	}
	back, err := LoadKnown(dir)
	if err != nil || !back.Contains("abc") || back.Contains("abd") {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("stage file left behind: %v", entries)
	}
}

func run(p probe.Probe) (probe.Status, string, []string) { return p.Run(context.Background()) }

func TestFingerprintProbe(t *testing.T) {
	old := profile.Schema{KeyPaths: []string{"manifest:Name", "manifest:Gone"}}
	cur := profile.Schema{KeyPaths: []string{"manifest:Name", "manifest:New"}}
	known := Known{Accepted: []Accepted{{Digest: "0123456789abcdef", Schema: old}}}
	if st, _, _ := run(Fingerprint(cur, "0123456789abcdef", known)); st != probe.Pass {
		t.Fatalf("known digest: %s", st)
	}
	st, detail, ev := run(Fingerprint(cur, "fedcba9876543210", known))
	if st != probe.Fail || !strings.Contains(detail, "--accept-fingerprint") {
		t.Fatalf("unknown digest: %s %s", st, detail)
	}
	if strings.Join(ev, "|") != "new key path: manifest:New|key path gone: manifest:Gone" {
		t.Fatalf("evidence = %v", ev)
	}
}

func hostWith(t *testing.T, selected map[string]string, records []map[string]any) (*host.Host, profile.LoadResult) {
	t.Helper()
	paths := fake.Paths{Root: t.TempDir()}
	if err := fixture.WriteTo(paths.ProfilesDir(), fixture.XL().FS()); err != nil {
		t.Fatal(err)
	}
	res, err := profile.LoadAll(os.DirFS(paths.ProfilesDir()))
	if err != nil {
		t.Fatal(err)
	}
	return &host.Host{Paths: paths, Prefs: fake.Prefs{Version: "7.5.1", Selected: selected, Records: records}}, res
}

func probeByID(ps []probe.Probe, id string) probe.Probe {
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	return probe.Probe{}
}

func TestM2(t *testing.T) {
	virtual := "@(0)[]"
	records := []map[string]any{{decks.RecordKey: fixture.Device}, {decks.RecordKey: virtual}, {decks.RecordKey: "x", decks.RecordRaw: "s"}}

	h, res := hostWith(t, map[string]string{fixture.Device: fixture.XL().ID}, records)
	if st, _, _ := run(probeByID(ContractB(h, res), "M2")); st != probe.Pass {
		t.Fatalf("matching selection: %s", st)
	}
	h, res = hostWith(t, map[string]string{fixture.Device: fixture.XL().ID, virtual: "11111111-2222-4333-8444-555555555555"}, records)
	if st, _, ev := run(probeByID(ContractB(h, res), "M2")); st != probe.Info || len(ev) != 1 {
		t.Fatalf("virtual deck pointing nowhere (U4) should be info: %s %v", st, ev)
	}
	h, res = hostWith(t, map[string]string{fixture.Device: "11111111-2222-4333-8444-555555555555"}, records)
	if st, _, _ := run(probeByID(ContractB(h, res), "M2")); st != probe.Fail {
		t.Fatalf("physical deck pointing nowhere must fail: %s", st)
	}
}

func TestM1AndM5(t *testing.T) {
	h, res := hostWith(t, nil, nil)
	if st, _, _ := run(probeByID(ContractB(h, res), "M1")); st != probe.Pass {
		t.Fatalf("M1 on a readable data root: %s", st)
	}
	if st, _, _ := run(probeByID(ContractB(h, res), "M5")); st != probe.Pass {
		t.Fatalf("M5 with a version: %s", st)
	}
	h.Paths = fake.Paths{Root: filepath.Join(t.TempDir(), "absent")}
	h.Prefs = fake.Prefs{}
	if st, _, _ := run(probeByID(ContractB(h, res), "M1")); st != probe.Fail {
		t.Fatalf("M1 on a missing data root: %s", st)
	}
	if st, _, _ := run(probeByID(ContractB(h, res), "M5")); st != probe.Fail {
		t.Fatalf("M5 without a version: %s", st)
	}
}
