// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package doctor assembles `schrodeck doctor`'s read-only tier: contract B's
// probes for this OS, contract C's probes (deckformat/probe), and the format
// fingerprint check of ADR 0015 against this host's known-good set.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/probe"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/host"
)

// KnownFile holds this host's accepted fingerprints, in its state dir.
const KnownFile = "known-fingerprints.json"

// Accepted is one fingerprint the user confirmed as known-good.
type Accepted struct {
	Digest     string         `json:"digest"`
	AppVersion string         `json:"app_version"`
	AcceptedAt time.Time      `json:"accepted_at"`
	Schema     profile.Schema `json:"schema"`
}

// Known is the known-good set.
type Known struct {
	Accepted []Accepted `json:"accepted"`
}

// Contains reports whether digest was accepted.
func (k Known) Contains(digest string) bool {
	for _, a := range k.Accepted {
		if a.Digest == digest {
			return true
		}
	}
	return false
}

// LoadKnown reads the known-good set; a missing file is an empty set.
func LoadKnown(stateDir string) (Known, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, KnownFile))
	if errors.Is(err, os.ErrNotExist) {
		return Known{}, nil
	}
	if err != nil {
		return Known{}, err
	}
	var k Known
	if err := json.Unmarshal(b, &k); err != nil {
		return Known{}, fmt.Errorf("%s: %w", KnownFile, err)
	}
	return k, nil
}

// SaveKnown writes the set by stage + rename, so a crash leaves the old file.
func SaveKnown(stateDir string, k Known) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir, KnownFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(stateDir, KnownFile))
}

func readOnly(id, contract string, run func() (probe.Status, string, []string)) probe.Probe {
	return probe.Probe{ID: id, Contract: contract, Tier: probe.ReadOnly, Run: func(context.Context) (probe.Status, string, []string) { return run() }}
}

// ContractB returns contract B's read-only probes for macOS (M1, M2, M5).
// M3 and M4 restart the app and arrive with M3.
func ContractB(h *host.Host, load profile.LoadResult) []probe.Probe {
	return []probe.Probe{
		readOnly("M1", "B", func() (probe.Status, string, []string) {
			entries, err := os.ReadDir(h.Paths.AppDataRoot())
			if err != nil {
				return probe.Fail, "the Stream Deck data root is not readable", []string{err.Error()}
			}
			return probe.Pass, fmt.Sprintf("data root readable (%d entries)", len(entries)), nil
		}),
		readOnly("M2", "B", func() (probe.Status, string, []string) { return m2(h, load) }),
		readOnly("M5", "B", func() (probe.Status, string, []string) {
			v, err := h.Prefs.AppVersion()
			if err != nil || v == "" {
				return probe.Fail, "app version unreadable", []string{fmt.Sprint(err)}
			}
			return probe.Pass, "app " + v, nil
		}),
	}
}

func m2(h *host.Host, load profile.LoadResult) (probe.Status, string, []string) {
	recs, err := h.Prefs.DeviceRecords()
	if err != nil {
		return probe.Fail, "prefs unreadable", []string{err.Error()}
	}
	folders := map[string]bool{}
	for _, p := range load.Profiles {
		folders[strings.ToLower(strings.TrimSuffix(p.Folder, profile.Suffix))] = true
	}
	status := probe.Pass
	var ev []string
	for _, rec := range recs {
		if _, raw := rec[decks.RecordRaw]; raw {
			continue
		}
		key, _ := rec[decks.RecordKey].(string)
		virtual, _, _, _, ok := decks.ParseKey(key)
		if !ok {
			continue
		}
		sel, err := h.Prefs.SelectedProfile(key)
		if err != nil {
			continue // a deck with no selection recorded is fine
		}
		if folders[strings.ToLower(sel)] {
			continue
		}
		if virtual {
			ev = append(ev, key+": selected profile has no folder on disk (known for virtual decks, U4)")
			if status == probe.Pass {
				status = probe.Info
			}
			continue
		}
		ev = append(ev, key+": selected profile has no folder on disk")
		status = probe.Fail
	}
	detail := map[probe.Status]string{
		probe.Pass: "every deck's selected profile exists",
		probe.Info: "only virtual decks point at missing profiles",
		probe.Fail: "a physical deck's selected profile is missing",
	}[status]
	return status, detail, ev
}

// Fingerprint is ADR 0015's check: is this host's current format
// fingerprint in its known-good set? When not, the evidence lists the key
// paths that differ from the most recently accepted schema.
func Fingerprint(schema profile.Schema, digest string, known Known) probe.Probe {
	return readOnly("FP", "C", func() (probe.Status, string, []string) {
		if known.Contains(digest) {
			return probe.Pass, "format fingerprint " + Short(digest) + " is known-good", nil
		}
		var ev []string
		if n := len(known.Accepted); n > 0 {
			added, removed := diffKeys(known.Accepted[n-1].Schema.KeyPaths, schema.KeyPaths)
			for _, a := range added {
				ev = append(ev, "new key path: "+a)
			}
			for _, r := range removed {
				ev = append(ev, "key path gone: "+r)
			}
		}
		return probe.Fail, "format fingerprint " + Short(digest) + " is not known-good: review, then run `schrodeck doctor --accept-fingerprint`", ev
	})
}

// Short is the leading 12 characters of a digest, the form shown to people;
// a shorter string is returned whole.
func Short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func diffKeys(old, cur []string) (added, removed []string) {
	o, c := map[string]bool{}, map[string]bool{}
	for _, k := range old {
		o[k] = true
	}
	for _, k := range cur {
		c[k] = true
		if !o[k] {
			added = append(added, k)
		}
	}
	for _, k := range old {
		if !c[k] {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
