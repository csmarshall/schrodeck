// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package probe

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/csmarshall/schrodeck/deckformat/fixture"
	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/normhash"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// Install is what the contract C probes look at.
type Install struct {
	Load profile.LoadResult
	Home string // value of {{HOME}} on this host (P6)
}

// OpenAction is the built-in Open action's UUID (P6).
const OpenAction = "com.elgato.streamdeck.system.open"

var uuidShape = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// ContractC returns the read-only probes of contract C.
func ContractC(in Install) []Probe {
	c := func(id string, run func() (Status, string, []string)) Probe {
		return Probe{ID: id, Contract: "C", Tier: ReadOnly, Run: func(context.Context) (Status, string, []string) { return run() }}
	}
	return []Probe{
		c("P1", func() (Status, string, []string) { return p1(in) }),
		c("P2", func() (Status, string, []string) { return p2(in) }),
		c("P3", func() (Status, string, []string) { return p3(in) }),
		c("P6", func() (Status, string, []string) { return p6(in) }),
		c("P7", func() (Status, string, []string) { return p7(in) }),
		c("P8", func() (Status, string, []string) { return p8(in) }),
		c("P9+P11", p9p11),
	}
}

func p1(in Install) (Status, string, []string) {
	var ev []string
	for _, err := range in.Load.Errors {
		var ue *profile.UnexpectedFileError
		if !errors.As(err, &ue) {
			ev = append(ev, err.Error())
		}
	}
	if len(ev) > 0 {
		return Fail, fmt.Sprintf("%d profile(s) do not have the expected layout", len(ev)), ev
	}
	if len(in.Load.Profiles) == 0 && len(in.Load.Errors) == 0 {
		return Skip, "no profiles found", nil
	}
	detail := fmt.Sprintf("%d profile(s) load", len(in.Load.Profiles))
	if n := len(in.Load.Errors); n > 0 {
		// Every remaining error is an allow-list failure, which P7 reports.
		detail += fmt.Sprintf(", %d excluded, see P7", n)
	}
	return Pass, detail, nil
}

func p2(in Install) (Status, string, []string) {
	var ev []string
	for _, p := range in.Load.Profiles {
		if p.Version() != profile.KnownVersion {
			ev = append(ev, fmt.Sprintf("%s: Version %q", p.Folder, p.Version()))
		}
	}
	if len(ev) > 0 {
		return Fail, "profile Version differs from " + profile.KnownVersion, ev
	}
	return Pass, "every profile has Version " + profile.KnownVersion, nil
}

func p3(in Install) (Status, string, []string) {
	var ev []string
	for _, p := range in.Load.Profiles {
		var missing []string
		if p.DeviceUUID() == "" {
			missing = append(missing, "Device.UUID")
		}
		if p.DeviceModel() == "" {
			missing = append(missing, "Device.Model")
		}
		if len(missing) > 0 {
			ev = append(ev, p.Folder+": "+strings.Join(missing, " and ")+" missing")
		}
	}
	if len(ev) > 0 {
		return Fail, "profiles not bound to a device", ev
	}
	return Pass, "every profile names its device", nil
}

func p7(in Install) (Status, string, []string) {
	var ev []string
	for _, err := range in.Load.Errors {
		var ue *profile.UnexpectedFileError
		if errors.As(err, &ue) {
			ev = append(ev, ue.Error())
		}
	}
	if len(ev) > 0 {
		return Fail, "files outside the allow-list (the format may have changed)", ev
	}
	return Pass, "every file is allow-listed", nil
}

// forEachSetting calls fn for every string inside every action's Settings.
func forEachSetting(p *profile.Profile, fn func(where, value string)) {
	for _, key := range p.SortedPageKeys() {
		pg := p.Pages[key]
		for _, c := range pg.Manifest.Get("Controllers").Items() {
			for _, m := range c.Get("Actions").Members() {
				where := fmt.Sprintf("%s › page %s › key %s", p.Folder, pg.Folder, m.Name)
				m.Value.Get("Settings").Walk(func(path []string, v *jsondoc.Value) {
					if s, ok := v.Str(); ok {
						fn(where+" › Settings."+strings.Join(path, "."), s)
					}
				})
			}
		}
	}
}

func p6(in Install) (Status, string, []string) {
	var ev []string
	// APFS is case-insensitive, so letter case does not move a path out of
	// {{HOME}}. Comparison is by whole path element: /Users/<user>2 is not
	// under /Users/<user>.
	home := strings.ToLower(filepath.Clean(in.Home))
	for _, p := range in.Load.Profiles {
		for _, key := range p.SortedPageKeys() {
			for _, c := range p.Pages[key].Manifest.Get("Controllers").Items() {
				for _, m := range c.Get("Actions").Members() {
					if uuid, _ := m.Value.Get("UUID").Str(); uuid != OpenAction {
						continue
					}
					path, ok := m.Value.Lookup("Settings", "path").Str()
					if !ok || !filepath.IsAbs(path) {
						continue
					}
					lower := strings.ToLower(filepath.Clean(path))
					if home == "." || (lower != home && !strings.HasPrefix(lower, home+string(filepath.Separator))) {
						ev = append(ev, fmt.Sprintf("%s › key %s: %s", p.Folder, m.Name, path))
					}
				}
			}
		}
	}
	if len(ev) > 0 {
		return Info, fmt.Sprintf("%d Open action(s) use an absolute path outside {{HOME}}", len(ev)), ev
	}
	return Pass, "every absolute Open path is under {{HOME}}", nil
}

func p8(in Install) (Status, string, []string) {
	// Folder ids are matched case-insensitively: folders are upper-case on
	// disk, references lower-case. A profile that exists but failed to load is
	// still a reference target, so its folder is listed too.
	id := func(folder string) string { return strings.ToLower(strings.TrimSuffix(folder, profile.Suffix)) }
	folders := map[string]string{}
	for _, p := range in.Load.Profiles {
		folders[id(p.Folder)] = p.Folder
	}
	for _, err := range in.Load.Errors {
		var fe *profile.FolderError
		if errors.As(err, &fe) {
			folders[id(fe.Folder)] = fe.Folder + " (which failed to load)"
		}
	}
	var ev []string
	for _, p := range in.Load.Profiles {
		own := p.DeviceUUID()
		self := id(p.Folder)
		forEachSetting(p, func(where, s string) {
			if uuidShape.MatchString(s) {
				if target, ok := folders[strings.ToLower(s)]; ok && strings.ToLower(s) != self {
					ev = append(ev, where+": references profile "+target)
				}
			}
			// A profile without a device of its own has no id to exclude: an
			// empty own id would match every string.
			if strings.Contains(s, "@(") && (own == "" || !strings.Contains(s, own)) {
				ev = append(ev, where+": embeds a device id that is not this profile's own")
			}
		})
	}
	sort.Strings(ev)
	if len(ev) > 0 {
		return Info, fmt.Sprintf("%d cross-profile or foreign-device reference(s)", len(ev)), ev
	}
	return Pass, "no cross-profile or foreign-device references", nil
}

// p9p11 checks this build's hasher against contract C's own claim: a copy
// (new ActionIDs, page folders and image names) hashes equal to its original.
// It only hashes the built-in fixture pair, so it is a self-test of this build,
// not a check of the real install.
func p9p11() (Status, string, []string) {
	a, b := fixture.XL(), fixture.CopyOf(fixture.XL(), "doctor")
	pa, err := profile.Load(a.FS(), a.Folder())
	if err != nil {
		return Fail, err.Error(), nil
	}
	pb, err := profile.Load(b.FS(), b.Folder())
	if err != nil {
		return Fail, err.Error(), nil
	}
	ha, err := normhash.Hash(pa)
	if err != nil {
		return Fail, err.Error(), nil
	}
	hb, err := normhash.Hash(pb)
	if err != nil {
		return Fail, err.Error(), nil
	}
	if ha != hb {
		return Fail, "a copy of the reference fixture hashes differently", nil
	}
	return Pass, fmt.Sprintf("self-test: this build hashes the built-in fixture pair equal under norm_version %d (it reads no real profile, so it cannot fail on a real host; its known-bad is normhash.TestEachCanonicalizationIsNeeded)", normhash.NormVersion), nil
}
