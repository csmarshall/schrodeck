// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package probe

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

func install(t *testing.T, home string, fss ...fstest.MapFS) Install {
	t.Helper()
	res, err := profile.LoadAll(fixture.Merge(fss...))
	if err != nil {
		t.Fatal(err)
	}
	return Install{Load: res, Home: home}
}

func byID(rs []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range rs {
		m[r.ID] = r
	}
	return m
}

func run(in Install) map[string]Result {
	return byID(RunAll(context.Background(), ContractC(in), ReadOnly))
}

func TestHealthyInstallPasses(t *testing.T) {
	rs := run(install(t, "/Users/<user>", fixture.XL().FS()))
	for _, id := range []string{"P1", "P2", "P3", "P6", "P7", "P8", "P9+P11"} {
		if rs[id].Status != Pass {
			t.Errorf("%s = %s (%s) %v", id, rs[id].Status, rs[id].Detail, rs[id].Evidence)
		}
	}
}

func TestEmptyInstallSkipsP1(t *testing.T) {
	if r := run(install(t, "/Users/<user>"))["P1"]; r.Status != Skip {
		t.Fatalf("P1 on no profiles = %s", r.Status)
	}
}

func TestEachProbeFailsOnItsKnownBad(t *testing.T) {
	v4 := fixture.XL()
	v4fs := v4.FS()
	key := v4.Folder() + "/manifest.json"
	v4fs[key] = &fstest.MapFile{Data: []byte(strings.Replace(string(v4fs[key].Data), `"Version":"3.0"`, `"Version":"4.0"`, 1))}

	stray := fixture.CopyOf(fixture.XL(), "stray")
	stray.Extra = map[string][]byte{"cache.db": []byte("x")}

	noDevice := fixture.CopyOf(fixture.XL(), "nodev")
	noDevice.Device = ""

	broken := fixture.CopyOf(fixture.XL(), "broken")
	bfs := broken.FS()
	bfs[broken.Folder()+"/manifest.json"] = &fstest.MapFile{Data: []byte("{\"Name\": 1}")}

	cases := []struct {
		id  string
		fs  fstest.MapFS
		bad Status
	}{
		{"P2", v4fs, Fail},
		{"P7", stray.FS(), Fail},
		{"P3", noDevice.FS(), Fail},
		{"P1", bfs, Fail},
	}
	for _, c := range cases {
		if r := run(install(t, "/Users/<user>", c.fs))[c.id]; r.Status != c.bad {
			t.Errorf("%s on its known-bad input = %s (%s)", c.id, r.Status, r.Detail)
		}
	}
}

func TestP6ReportsPathsOutsideHome(t *testing.T) {
	r := run(install(t, "/Users/<other>", fixture.XL().FS()))["P6"]
	if r.Status != Info || len(r.Evidence) != 1 || !strings.Contains(r.Evidence[0], "/Users/<user>/bin/demo.sh") {
		t.Fatalf("P6 = %s %v", r.Status, r.Evidence)
	}
}

func TestP8FindsReferences(t *testing.T) {
	target := fixture.CopyOf(fixture.XL(), "target")
	src := fixture.XL()
	src.Pages[0].Buttons[1].Settings = `{"profile":"` + target.ID + `","device":"@(1)[4057/99/<other-deck>]"}`
	r := run(install(t, "/Users/<user>", src.FS(), target.FS()))["P8"]
	if r.Status != Info || len(r.Evidence) != 2 {
		t.Fatalf("P8 = %s %v", r.Status, r.Evidence)
	}
	joined := strings.Join(r.Evidence, "\n")
	if !strings.Contains(joined, "references profile "+target.Folder()) || !strings.Contains(joined, "not this profile's own") {
		t.Fatalf("P8 evidence = %s", joined)
	}
}

func TestRunAllSkipsHigherTiers(t *testing.T) {
	ran := false
	ps := []Probe{{ID: "X", Contract: "C", Tier: Restart, Run: func(context.Context) (Status, string, []string) {
		ran = true
		return Pass, "", nil
	}}}
	rs := RunAll(context.Background(), ps, ReadOnly)
	if ran || rs[0].Status != Skip || rs[0].Tier != "restart" {
		t.Fatalf("restart-tier probe ran or was not skipped: %+v", rs[0])
	}
	if Failed(rs) {
		t.Fatal("a skip is not a failure")
	}
	if !Failed([]Result{{Status: Fail}}) {
		t.Fatal("Failed missed a failure")
	}
}
