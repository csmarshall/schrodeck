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

// P3 names which half of the device binding is missing, and fails on either.
func TestP3NamesTheMissingHalf(t *testing.T) {
	noModel := fixture.XL()
	noModel.Model = ""
	noUUID := fixture.XL()
	noUUID.Device = ""
	for _, c := range []struct {
		name      string
		p         fixture.Profile
		want, not string
	}{
		{"UUID without Model", noModel, "Device.Model missing", "UUID"},
		{"Model without UUID", noUUID, "Device.UUID missing", "Model"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := run(install(t, "/Users/<user>", c.p.FS()))["P3"]
			if r.Status != Fail || len(r.Evidence) != 1 || !strings.Contains(r.Evidence[0], c.want) || strings.Contains(r.Evidence[0], c.not) {
				t.Fatalf("P3 = %s %v, want a failure naming %q only", r.Status, r.Evidence, c.want)
			}
		})
	}
}

// P6 decides "under {{HOME}}" by path element and ignores letter case (APFS).
func TestP6HomeBoundaries(t *testing.T) {
	const home = "/Users/<user>"
	for _, c := range []struct {
		name, path string
		outside    bool
	}{
		{"exact home", home, false},
		{"inside", home + "/bin/x.sh", false},
		{"other letter case", "/users/<USER>/bin/x.sh", false},
		{"sibling sharing a prefix", home + "2/bin/x.sh", true},
		{"elsewhere", "/opt/x.sh", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.XL()
			p.Pages[0].Buttons[0].Settings = `{"path":"` + c.path + `"}`
			r := run(install(t, home, p.FS()))["P6"]
			if got := r.Status == Info; got != c.outside {
				t.Fatalf("P6 on %q = %s %v, want outside=%v", c.path, r.Status, r.Evidence, c.outside)
			}
		})
	}
}

// P8 still reports foreign device ids when the profile has no device of its
// own (an empty own id must not match every string).
func TestP8DeviceIDsInAProfileWithoutDevice(t *testing.T) {
	p := fixture.XL()
	p.Device = ""
	p.Pages[0].Buttons[1].Settings = `{"device":"@(1)[4057/99/<other-deck>]"}`
	r := run(install(t, "/Users/<user>", p.FS()))["P8"]
	if r.Status != Info || len(r.Evidence) != 1 || !strings.Contains(r.Evidence[0], "not this profile's own") {
		t.Fatalf("P8 = %s %v", r.Status, r.Evidence)
	}
}

// P8 matches folders case-insensitively: folders are upper-case on disk,
// references lower-case.
func TestP8MatchesFoldersCaseInsensitively(t *testing.T) {
	target := fixture.CopyOf(fixture.XL(), "target")
	if target.Folder() == strings.ToLower(target.Folder()) || target.ID != strings.ToLower(target.ID) {
		t.Fatal("control: the fixture must have an upper-case folder and a lower-case id")
	}
	src := fixture.XL()
	src.Pages[0].Buttons[1].Settings = `{"profile":"` + target.ID + `"}`
	r := run(install(t, "/Users/<user>", src.FS(), target.FS()))["P8"]
	if r.Status != Info || len(r.Evidence) != 1 || !strings.Contains(r.Evidence[0], "references profile "+target.Folder()) {
		t.Fatalf("P8 = %s %v", r.Status, r.Evidence)
	}
}

// P8 reports a reference to a profile that exists but failed to load (it is
// not in Load.Profiles, so a lookup there alone misses it).
func TestP8ReportsReferencesToUnloadableProfiles(t *testing.T) {
	broken := fixture.CopyOf(fixture.XL(), "broken")
	broken.Extra = map[string][]byte{"cache.db": []byte("x")}
	src := fixture.XL()
	src.Pages[0].Buttons[1].Settings = `{"profile":"` + broken.ID + `"}`
	in := install(t, "/Users/<user>", src.FS(), broken.FS())
	if len(in.Load.Errors) != 1 {
		t.Fatalf("control: the broken profile must fail to load: %v", in.Load.Errors)
	}
	r := run(in)["P8"]
	if r.Status != Info || len(r.Evidence) != 1 || !strings.Contains(r.Evidence[0], broken.Folder()) || !strings.Contains(r.Evidence[0], "failed to load") {
		t.Fatalf("P8 = %s %v", r.Status, r.Evidence)
	}
}

// P1 says how many profiles it left to P7, so its count is not mistaken for
// the whole install.
func TestP1CountsProfilesExcludedForP7(t *testing.T) {
	stray := fixture.CopyOf(fixture.XL(), "stray")
	stray.Extra = map[string][]byte{"cache.db": []byte("x")}
	r := run(install(t, "/Users/<user>", fixture.XL().FS(), stray.FS()))["P1"]
	if r.Status != Pass || !strings.Contains(r.Detail, "1 profile(s) load, 1 excluded, see P7") {
		t.Fatalf("P1 = %s %q", r.Status, r.Detail)
	}
}
