// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package observe is the observation harness of ADR 0031: snapshot the app's
// profiles (and optionally its preferences), let a person do one thing in the
// app, snapshot again, and produce a redacted report plus a draft evidence row
// for docs/references.md. It is how unknowns about the format become
// documented observations. It only reads the app's data; snapshots are
// written to a directory the caller chooses (schrodeck's own state dir).
package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/normhash"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/deckformat/redact"
	"github.com/csmarshall/schrodeck/deckformat/semdiff"
)

// Snapshot is the app's profile data at one moment.
type Snapshot struct {
	TakenAt    time.Time
	AppVersion string
	Profiles   map[string]*profile.Profile // by folder name
	LoadErrors []string
	Prefs      *jsondoc.Value // may be nil
}

type meta struct {
	TakenAt     time.Time `json:"taken_at"`
	AppVersion  string    `json:"app_version"`
	NormVersion int       `json:"norm_version"`
	LoadErrors  []string  `json:"load_errors,omitempty"`
}

// Take reads every profile in profiles (a ProfilesV3-shaped fs.FS).
func Take(profiles fs.FS, prefs *jsondoc.Value, appVersion string, now time.Time) (*Snapshot, error) {
	res, err := profile.LoadAll(profiles)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{TakenAt: now.UTC(), AppVersion: appVersion, Profiles: map[string]*profile.Profile{}, Prefs: prefs}
	for _, p := range res.Profiles {
		s.Profiles[p.Folder] = p
	}
	for _, e := range res.Errors {
		s.LoadErrors = append(s.LoadErrors, e.Error())
	}
	return s, nil
}

// Digest identifies the snapshot's content (not its time).
func (s *Snapshot) Digest() string {
	var lines []string
	for folder, p := range s.Profiles {
		for rel, data := range p.Files() {
			sum := sha256.Sum256(data)
			lines = append(lines, folder+"/"+rel+"\x00"+hex.EncodeToString(sum[:]))
		}
	}
	if s.Prefs != nil {
		sum := sha256.Sum256(s.Prefs.Encode())
		lines = append(lines, "prefs\x00"+hex.EncodeToString(sum[:]))
	}
	lines = append(lines, s.LoadErrors...)
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

// Settle takes snapshots until two consecutive ones are identical, waiting
// between them, so a snapshot is not taken while the app is mid-write. It
// gives up after maxTries snapshots.
func Settle(take func() (*Snapshot, error), wait func(), maxTries int) (*Snapshot, error) {
	prev, err := take()
	if err != nil {
		return nil, err
	}
	for i := 1; i < maxTries; i++ {
		wait()
		cur, err := take()
		if err != nil {
			return nil, err
		}
		if cur.Digest() == prev.Digest() {
			return cur, nil
		}
		prev = cur
	}
	return nil, fmt.Errorf("observe: the app's files kept changing across %d snapshots", maxTries)
}

// ErrExists means a snapshot directory is already in use.
var ErrExists = errors.New("observe: snapshot directory already exists")

// writeNew creates path with O_EXCL (never overwriting or following an
// existing file or link) and writes data to it.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Save writes the snapshot to dir, which must not exist yet. Every file is
// created exclusively, so nothing existing is ever overwritten.
func (s *Snapshot) Save(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return err
	}
	if err := os.Mkdir(filepath.Join(dir, "profiles"), 0o700); err != nil {
		return err
	}
	for folder, p := range s.Profiles {
		for rel, data := range p.Files() {
			target := filepath.Join(dir, "profiles", folder, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := writeNew(target, data); err != nil {
				return err
			}
		}
	}
	if s.Prefs != nil {
		if err := writeNew(filepath.Join(dir, "prefs.json"), s.Prefs.Encode()); err != nil {
			return err
		}
	}
	m, err := json.Marshal(meta{TakenAt: s.TakenAt, AppVersion: s.AppVersion, NormVersion: normhash.NormVersion, LoadErrors: s.LoadErrors})
	if err != nil {
		return err
	}
	return writeNew(filepath.Join(dir, "meta.json"), m)
}

// Load reads a snapshot written by Save.
func Load(dir string) (*Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	var prefs *jsondoc.Value
	if pb, err := os.ReadFile(filepath.Join(dir, "prefs.json")); err == nil {
		if prefs, err = jsondoc.Parse(pb); err != nil {
			return nil, fmt.Errorf("prefs.json: %w", err)
		}
	}
	s, err := Take(os.DirFS(filepath.Join(dir, "profiles")), prefs, m.AppVersion, m.TakenAt)
	if err != nil {
		return nil, err
	}
	s.LoadErrors = append(s.LoadErrors, m.LoadErrors...)
	return s, nil
}

// Report is the outcome of one observation.
type Report struct {
	Name       string           `json:"name"`
	Before     time.Time        `json:"before"`
	After      time.Time        `json:"after"`
	AppVersion string           `json:"app_version"`
	Changes    []semdiff.Change `json:"changes"`
}

// Compare diffs two snapshots (raw mode: runtime fields and ids included,
// since that is what an observation is for) and redacts the result.
func Compare(name string, before, after *Snapshot, r *redact.Redactor) (Report, error) {
	changes, err := semdiff.Sets(before.Profiles, after.Profiles, semdiff.Raw)
	if err != nil {
		return Report{}, err
	}
	if before.Prefs != nil || after.Prefs != nil {
		changes = append(changes, semdiff.Values("app preferences", "prefs", before.Prefs, after.Prefs)...)
	}
	ps := newPseudonyms(before, after)
	for i := range changes {
		c := &changes[i]
		c.Profile, c.Where, c.Path = r.String(c.Profile), r.String(c.Where), r.String(c.Path)
		c.Before, c.After = redactJSON(r, c.Before), redactJSON(r, c.After)
		c.Profile, c.Where, c.Path = ps.String(c.Profile), ps.String(c.Where), ps.String(c.Path)
		c.Before, c.After = ps.String(c.Before), ps.String(c.After)
	}
	return Report{Name: name, Before: before.TakenAt, After: after.TakenAt, AppVersion: after.AppVersion, Changes: changes}, nil
}

// uuidPattern matches a UUID in any letter case.
var uuidPattern = regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)

// pseudonyms replaces every UUID in a report with a stable, readable name, so
// a committed report carries no real profile, page or action ids while every
// equality between ids stays visible (the same id always gets the same name).
// Profile folders become "profile-1", "profile-2", … in folder-name order;
// pages become "profile-1/page/0", "profile-1/default" or
// "profile-1/sub-page-1" by their position before the change (after it, for a
// page that is new); any other UUID (an ActionID, an unknown reference)
// becomes "uuid-1", "uuid-2", … in order of first appearance.
type pseudonyms struct {
	names map[string]string // lower-case UUID → pseudonym
	next  int
}

func newPseudonyms(snaps ...*Snapshot) *pseudonyms {
	ps := &pseudonyms{names: map[string]string{}}
	folders := map[string]*profile.Profile{}
	for _, s := range snaps {
		for folder, p := range s.Profiles {
			if _, seen := folders[folder]; !seen {
				folders[folder] = p
			}
		}
	}
	var sorted []string
	for f := range folders {
		sorted = append(sorted, f)
	}
	sort.Strings(sorted)
	for i, folder := range sorted {
		prof := fmt.Sprintf("profile-%d", i+1)
		ps.names[strings.ToLower(strings.TrimSuffix(folder, profile.Suffix))] = prof
		subPages := 0
		for _, s := range snaps {
			p := s.Profiles[folder]
			if p == nil {
				continue
			}
			labels, err := normhash.PageLabels(p)
			if err != nil {
				continue // a malformed profile: its page ids fall back to uuid-N
			}
			var ids []string
			for id := range labels {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				if _, named := ps.names[id]; named {
					continue
				}
				label := labels[id]
				if strings.HasPrefix(label, "other/") {
					subPages++
					label = fmt.Sprintf("sub-page-%d", subPages)
				}
				ps.names[id] = prof + "/" + label
			}
		}
	}
	return ps
}

// String replaces every UUID in s.
func (ps *pseudonyms) String(s string) string {
	return uuidPattern.ReplaceAllStringFunc(s, func(u string) string {
		key := strings.ToLower(u)
		if name, ok := ps.names[key]; ok {
			return name
		}
		ps.next++
		name := fmt.Sprintf("uuid-%d", ps.next)
		ps.names[key] = name
		return name
	})
}

// redactJSON redacts a JSON fragment structurally when it parses (so secret
// member names are honored), else as text.
func redactJSON(r *redact.Redactor, s string) string {
	if s == "" {
		return s
	}
	if v, err := jsondoc.Parse([]byte(s)); err == nil {
		return string(r.Value(v).Encode())
	}
	return r.String(s)
}

// escape makes s safe inside a Markdown table cell.
func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

// cellRunes is the longest table cell, in characters (not bytes, so a cut
// never splits a multi-byte character).
const cellRunes = 120

// cell escapes and shortens long JSON fragments for the change table.
func cell(s string) string {
	s = escape(s)
	if r := []rune(s); len(r) > cellRunes {
		s = string(r[:cellRunes-3]) + "..."
	}
	return s
}

// Markdown renders the report for docs/observations/<name>.md.
func (rep Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Observation: %s\n\n", rep.Name)
	fmt.Fprintf(&b, "- Before: %s · After: %s · App: %s · norm_version: %d\n", rep.Before.Format(time.RFC3339), rep.After.Format(time.RFC3339), rep.AppVersion, normhash.NormVersion)
	fmt.Fprintf(&b, "- Changes (raw, redacted): %d\n\n", len(rep.Changes))
	if len(rep.Changes) > 0 {
		b.WriteString("| Profile | Where | Path | Change | Before | After |\n|---|---|---|---|---|---|\n")
		for _, c := range rep.Changes {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", cell(c.Profile), cell(c.Where), cell(c.Path), c.Kind, cell(c.Before), cell(c.After))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Draft evidence row for docs/references.md\n\n")
	b.WriteString(rep.EvidenceRow() + "\n")
	return b.String()
}

// EvidenceRow is a draft row for docs/references.md. A person fills in the id
// and topic and rewrites the summary as a claim.
func (rep Report) EvidenceRow() string {
	var summary []string
	for i, c := range rep.Changes {
		if i == 3 {
			summary = append(summary, fmt.Sprintf("and %d more", len(rep.Changes)-3))
			break
		}
		summary = append(summary, c.String())
	}
	if len(summary) == 0 {
		summary = []string{"no change on disk"}
	}
	return fmt.Sprintf("| R?? | <topic> | **Observed** | `schrodeck observe %s`, app %s, %s | %s. Full report: [observations/%s.md](observations/%s.md) |",
		rep.Name, rep.AppVersion, rep.After.Format("2006-01-02"), escape(strings.Join(summary, "; ")), rep.Name, rep.Name)
}
