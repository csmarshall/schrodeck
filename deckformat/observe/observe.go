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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	LoadErrors []LoadError
	Prefs      *jsondoc.Value // may be nil
}

// LoadError is a profile folder that exists but failed to load (for example
// a file outside contract C's allow-list, P7). It is kept with its folder so a
// report can say which profile it is instead of calling it removed.
type LoadError struct {
	Folder  string `json:"folder"`
	Message string `json:"message"`
}

type meta struct {
	TakenAt     time.Time   `json:"taken_at"`
	AppVersion  string      `json:"app_version"`
	NormVersion int         `json:"norm_version"`
	LoadErrors  []LoadError `json:"load_errors,omitempty"`
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
		le := LoadError{Message: e.Error()}
		var fe *profile.FolderError
		if errors.As(e, &fe) {
			le.Folder = fe.Folder
		}
		s.LoadErrors = append(s.LoadErrors, le)
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
	for _, le := range s.LoadErrors {
		lines = append(lines, "load error\x00"+le.Folder+"\x00"+le.Message)
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

// Settle takes snapshots until two consecutive ones are identical, waiting
// between them, so a snapshot is not taken while the app is mid-write. It
// gives up after maxTries snapshots; maxTries must be at least 2, since one
// snapshot cannot show that anything settled.
func Settle(take func() (*Snapshot, error), wait func(), maxTries int) (*Snapshot, error) {
	if maxTries < 2 {
		return nil, fmt.Errorf("observe: Settle needs at least 2 snapshots to compare, got maxTries %d", maxTries)
	}
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

// sortedKeys returns m's keys in order, so walks over maps are deterministic.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// writeFile is writeNew; a test replaces it to fail a Save part-way.
var writeFile = writeNew

// Save writes the snapshot to dir, which must not exist yet. Every file is
// created exclusively, so nothing existing is ever overwritten.
func (s *Snapshot) Save(dir string) (err error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return err
	}
	// This call made dir (Mkdir is exclusive), so a failure removes it again.
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	if err := os.Mkdir(filepath.Join(dir, "profiles"), 0o700); err != nil {
		return err
	}
	for _, folder := range sortedKeys(s.Profiles) {
		files := s.Profiles[folder].Files()
		for _, rel := range sortedKeys(files) {
			data := files[rel]
			target := filepath.Join(dir, "profiles", folder, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := writeFile(target, data); err != nil {
				return err
			}
		}
	}
	if s.Prefs != nil {
		if err := writeFile(filepath.Join(dir, "prefs.json"), s.Prefs.Encode()); err != nil {
			return err
		}
	}
	m, err := json.Marshal(meta{TakenAt: s.TakenAt, AppVersion: s.AppVersion, NormVersion: normhash.NormVersion, LoadErrors: s.LoadErrors})
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, "meta.json"), m)
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
	pb, err := os.ReadFile(filepath.Join(dir, "prefs.json"))
	switch {
	case err == nil:
		if prefs, err = jsondoc.Parse(pb); err != nil {
			return nil, fmt.Errorf("prefs.json: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("prefs.json: %w", err)
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

// LoadFailed is the Kind of a report row for a profile folder that exists but
// failed to load; Before and After carry the (redacted) error on each side.
const LoadFailed semdiff.Kind = "load error"

// emptyManifest is the manifest of the empty profile an added or removed
// profile is diffed against: just enough structure (Pages.Pages) for the page
// labels to resolve, so every real member shows up as its own leaf.
const emptyManifest = `{"Pages":{"Pages":[]}}`

// emptyProfile stands for a profile that is absent on one side.
func emptyProfile(folder string) (*profile.Profile, error) {
	m, err := jsondoc.Parse([]byte(emptyManifest))
	if err != nil {
		return nil, err
	}
	return &profile.Profile{Folder: folder, Manifest: m, Images: map[string][]byte{}, Pages: map[string]*profile.Page{}}, nil
}

// loadErrorsByFolder groups a snapshot's load errors by folder ("" for an
// error that names none).
func loadErrorsByFolder(s *Snapshot) map[string]string {
	out := map[string]string{}
	for _, le := range s.LoadErrors {
		if prev, ok := out[le.Folder]; ok {
			out[le.Folder] = prev + "; " + le.Message
			continue
		}
		out[le.Folder] = le.Message
	}
	return out
}

// Compare diffs two snapshots (raw mode: runtime fields and ids included,
// since that is what an observation is for) and redacts the result. Profile
// names are user content, so the Profile column and profile Name values carry
// the profile-N alias instead. A profile present on one side only is diffed
// against an empty profile, and an added or removed subtree is reported leaf
// by leaf (semdiff.ProfileLeaves), so every row carries one value. A profile
// folder that exists but failed to load is one "load error" row, never an
// added or removed profile. Changes are detected on the raw snapshots but
// rendered out of redacted copies of the whole documents (see renderer), so a
// changed secret shows as <redacted> on both sides and the row stays.
func Compare(name string, before, after *Snapshot, r *redact.Redactor) (Report, error) {
	ps := newPseudonyms(before, after)
	errBefore, errAfter := loadErrorsByFolder(before), loadErrorsByFolder(after)
	folders := map[string]bool{}
	for _, m := range []map[string]*profile.Profile{before.Profiles, after.Profiles} {
		for f := range m {
			folders[f] = true
		}
	}
	for _, m := range []map[string]string{errBefore, errAfter} {
		for f := range m {
			folders[f] = true
		}
	}
	var changes []semdiff.Change
	var changeFolder []string // the profile folder behind each change
	for _, folder := range sortedKeys(folders) {
		alias := ps.profileAlias(folder)
		eb, failedBefore := errBefore[folder]
		ea, failedAfter := errAfter[folder]
		if failedBefore || failedAfter {
			// One side cannot be read, so nothing about its content is known:
			// report the failure, not a removal, addition or diff.
			changes = append(changes, semdiff.Change{Profile: alias, Where: string(LoadFailed), Kind: LoadFailed, Before: eb, After: ea})
		} else {
			a, b := before.Profiles[folder], after.Profiles[folder]
			var err error
			if a == nil {
				a, err = emptyProfile(folder)
			} else if b == nil {
				b, err = emptyProfile(folder)
			}
			if err != nil {
				return Report{}, err
			}
			cs, err := semdiff.ProfileLeaves(a, b, semdiff.Raw)
			if err != nil {
				return Report{}, fmt.Errorf("%s: %w", folder, err)
			}
			for i := range cs {
				cs[i].Profile = alias
			}
			changes = append(changes, cs...)
		}
		for len(changeFolder) < len(changes) {
			changeFolder = append(changeFolder, folder)
		}
	}
	if before.Prefs != nil || after.Prefs != nil {
		changes = append(changes, semdiff.ValueLeaves("app preferences", "prefs", before.Prefs, after.Prefs)...)
		for len(changeFolder) < len(changes) {
			changeFolder = append(changeFolder, "")
		}
	}
	rd := newRenderer(before, after, r)
	for i := range changes {
		c := &changes[i]
		switch {
		case c.Kind == LoadFailed:
			// Error text is not a document value: redact it as plain text.
			c.Before, c.After = r.String(c.Before), r.String(c.After)
		case c.Where == "profile" && c.Path == "Name":
			// The profile's own name is user content: both sides become its alias.
			c.Before, c.After = profileName(c.Before, c.Profile, c.Kind), profileName(c.After, c.Profile, c.Kind)
		default:
			rd.render(changeFolder[i], c)
		}
		if c.Page != "" {
			c.Where = ps.pageWhere(c.Where, c.Page)
		}
		c.Profile, c.Where, c.Path = r.String(c.Profile), r.String(c.Where), r.String(c.Path)
		c.Profile, c.Where, c.Path = ps.String(c.Profile), ps.String(c.Where), ps.String(c.Path)
		c.Before, c.After = ps.String(c.Before), ps.String(c.After)
		// Page and SlotPath carry raw page ids and served only to resolve the change.
		c.Page, c.SlotPath = "", ""
	}
	return Report{Name: name, Before: before.TakenAt, After: after.TakenAt, AppVersion: after.AppVersion, Changes: changes}, nil
}

// profileName stands for a present profile name without showing it: the alias
// for a profile that is added or removed, "<alias> (renamed)" for a rename.
func profileName(value, alias string, kind semdiff.Kind) string {
	if value == "" {
		return value
	}
	if kind != semdiff.Modified {
		return alias
	}
	return alias + " (renamed)"
}

// uuidPattern matches a UUID in any letter case.
var uuidPattern = regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)

// pseudonyms replaces every UUID in a report with a stable, readable name, so
// a committed report carries no real profile, page or action ids while every
// equality between ids stays visible (the same id always gets the same name).
// Profile folders, loaded or not, become "profile-1", "profile-2", … in
// folder-name order, so the numbers are per report. A page present in the
// first snapshot is named by its position there ("profile-1/page/0",
// "profile-1/default", "profile-1/sub-page-1"); a page that appears only in a
// later snapshot is "profile-1/new-page-1", "profile-1/new-page-2", …, never a
// position name, which an existing page may already hold. Any other UUID (an
// ActionID, an unknown reference) becomes "uuid-1", "uuid-2", … in order of
// first appearance. The naming is injective: no two ids share a name.
type pseudonyms struct {
	names map[string]string // lower-case UUID → pseudonym
	next  int
}

// labelOrder sorts page labels by position: Pages.Pages in list order, then
// the default page, then pages listed nowhere (by id).
func labelOrder(label string) (int, int, string) {
	if n, err := strconv.Atoi(strings.TrimPrefix(label, "page/")); err == nil && strings.HasPrefix(label, "page/") {
		return 0, n, ""
	}
	if label == "default" {
		return 1, 0, ""
	}
	return 2, 0, label
}

// newPseudonyms names the ids of snaps; the first snapshot is the "before" one.
func newPseudonyms(snaps ...*Snapshot) *pseudonyms {
	ps := &pseudonyms{names: map[string]string{}}
	folders := map[string]bool{}
	for _, s := range snaps {
		for folder := range s.Profiles {
			folders[folder] = true
		}
		for _, le := range s.LoadErrors {
			if le.Folder != "" {
				folders[le.Folder] = true
			}
		}
	}
	for i, folder := range sortedKeys(folders) {
		prof := fmt.Sprintf("profile-%d", i+1)
		ps.names[strings.ToLower(strings.TrimSuffix(folder, profile.Suffix))] = prof
		subPages, newPages := 0, 0
		for si, s := range snaps {
			p := s.Profiles[folder]
			if p == nil {
				continue
			}
			labels, err := normhash.PageLabels(p)
			if err != nil {
				continue // a malformed profile: its page ids fall back to uuid-N
			}
			ids := sortedKeys(labels)
			sort.SliceStable(ids, func(x, y int) bool {
				xa, xb, xc := labelOrder(labels[ids[x]])
				ya, yb, yc := labelOrder(labels[ids[y]])
				if xa != ya {
					return xa < ya
				}
				if xb != yb {
					return xb < yb
				}
				return xc < yc
			})
			for _, id := range ids {
				if _, named := ps.names[id]; named {
					continue
				}
				label := labels[id]
				switch {
				case si > 0:
					newPages++
					label = fmt.Sprintf("new-page-%d", newPages)
				case strings.HasPrefix(label, "other/"):
					subPages++
					label = fmt.Sprintf("sub-page-%d", subPages)
				}
				ps.names[id] = prof + "/" + label
			}
		}
	}
	return ps
}

// profileAlias is the profile-N name of a profile folder.
func (ps *pseudonyms) profileAlias(folder string) string {
	return ps.names[strings.ToLower(strings.TrimSuffix(folder, profile.Suffix))]
}

// pageWhere rewrites a raw-mode page location, "page 2 (<page id>) › key 0,0",
// to name the page by its pseudonym alone: "profile-1/page/1 › key 0,0". The
// position number in front comes from the after snapshot while the pseudonym
// comes from the before one, so showing both would mislead.
func (ps *pseudonyms) pageWhere(where, page string) string {
	marker := " (" + page + ")"
	i := strings.Index(where, marker)
	if i < 0 {
		return where
	}
	name, ok := ps.names[strings.ToLower(page)]
	if !ok {
		name = page
	}
	return name + where[i+len(marker):]
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

// escape makes s safe inside a Markdown table cell.
func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

// cellRunes is the longest table cell, in characters (not bytes, so a cut
// never splits a multi-byte character).
const cellRunes = 120

// evidenceRowRunes bounds the draft evidence row, in characters.
const evidenceRowRunes = 600

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
	fmt.Fprintf(&b, "- Changes (raw, redacted): %d\n", len(rep.Changes))
	b.WriteString("- Titles, page names and setting values are shown as-is; review before committing.\n")
	b.WriteString("- profile-N numbers are assigned per report and can differ between reports.\n\n")
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

// evidenceChanges is how many changes the evidence row summarizes.
const evidenceChanges = 3

// EvidenceRow is a draft row for docs/references.md. A person fills in the id
// and topic and rewrites the summary as a claim. The summary names at most
// evidenceChanges changes and the row is at most evidenceRowRunes characters
// (for a report name of ordinary length); a cut summary says so and points to
// the full report.
func (rep Report) EvidenceRow() string {
	var parts []string
	for i, c := range rep.Changes {
		if i == evidenceChanges {
			break
		}
		parts = append(parts, c.String())
	}
	summary := escape(strings.Join(parts, "; "))
	if len(parts) == 0 {
		summary = "no change on disk"
	}
	head := fmt.Sprintf("| R?? | <topic> | **Observed** | `schrodeck observe %s`, app %s, %s | ", rep.Name, rep.AppVersion, rep.After.Format("2006-01-02"))
	tail := fmt.Sprintf(". Full report: [observations/%s.md](observations/%s.md) |", rep.Name, rep.Name)
	note := fmt.Sprintf(" … (truncated; %d change(s) in the full report)", len(rep.Changes))
	budget := evidenceRowRunes - utf8.RuneCountInString(head) - utf8.RuneCountInString(tail)
	if len(rep.Changes) > evidenceChanges || utf8.RuneCountInString(summary) > budget {
		keep := max(budget-utf8.RuneCountInString(note), 0)
		if r := []rune(summary); len(r) > keep {
			summary = string(r[:keep])
		}
		summary += note
	}
	return head + summary + tail
}

// renderer renders a change's Before/After out of REDACTED copies of the whole
// documents the change was found in, so every rule in the redact package
// (member names, name/value pairs, embedded JSON, URLs, bearer values) applies
// in context instead of to a bare scalar that has lost its surroundings. The
// change is detected on the raw snapshots; only its rendering is redacted.
// Only raw-mode changes resolve: a semantic-mode change names its page by a
// canonical label, not a Profile.Pages key, so it finds no page and is
// blanked (fail closed).
type renderer struct {
	before, after *Snapshot
	r             *redact.Redactor
	memo          map[*jsondoc.Value]*jsondoc.Value // raw document → redacted copy
}

func newRenderer(before, after *Snapshot, r *redact.Redactor) *renderer {
	return &renderer{before: before, after: after, r: r, memo: map[*jsondoc.Value]*jsondoc.Value{}}
}

func (rd *renderer) redacted(v *jsondoc.Value) *jsondoc.Value {
	if red, ok := rd.memo[v]; ok {
		return red
	}
	red := rd.r.Value(v)
	rd.memo[v] = red
	return red
}

// render replaces c.Before and c.After by their redacted renderings. A side
// that has a value but cannot be found in the redacted document is blanked
// (fail closed). File and whole-profile changes carry digests and aliases, not
// document values, and are left alone.
func (rd *renderer) render(folder string, c *semdiff.Change) {
	if c.Where == "files" || c.Where == "profiles" {
		return
	}
	segs, err := semdiff.ParsePath(c.Path)
	if err != nil {
		c.Before, c.After = blankSide(c.Before), blankSide(c.After)
		return
	}
	c.Before = rd.side(rd.before, folder, c, segs, c.Before)
	c.After = rd.side(rd.after, folder, c, segs, c.After)
}

func blankSide(s string) string {
	if s == "" {
		return s
	}
	return redact.Redacted
}

// side finds the node at segs in snapshot s and returns the encoding of the
// same node in the redacted copy. The change names exactly one document (the
// prefs, the profile manifest, or one page manifest via Change.Page, narrowed
// to an action slot via Change.SlotPath), so a value is never resolved in the
// context of another page. A side whose node is missing, whose raw encoding
// differs from the change's, or that resolves to more than one root is blanked
// (fail closed).
func (rd *renderer) side(s *Snapshot, folder string, c *semdiff.Change, segs []semdiff.Segment, raw string) string {
	if raw == "" {
		return ""
	}
	roots := rd.roots(s, folder, c)
	if len(roots) != 1 {
		return redact.Redacted
	}
	rawNode, redNode := walkPair(roots[0].raw, roots[0].red, segs)
	if rawNode == nil || string(rawNode.Encode()) != raw {
		return redact.Redacted
	}
	return string(redNode.Encode())
}

type rootPair struct{ raw, red *jsondoc.Value }

// roots lists the documents (raw and redacted) a change's path is relative to:
// exactly one when the change can be resolved, none otherwise.
func (rd *renderer) roots(s *Snapshot, folder string, c *semdiff.Change) []rootPair {
	pair := func(v *jsondoc.Value) rootPair { return rootPair{v, rd.redacted(v)} }
	switch c.Where {
	case "prefs":
		if s.Prefs != nil {
			return []rootPair{pair(s.Prefs)}
		}
		return nil
	case "profile":
		if p := s.Profiles[folder]; p != nil {
			return []rootPair{pair(p.Manifest)}
		}
		return nil
	}
	p := s.Profiles[folder]
	if p == nil || c.Page == "" {
		return nil
	}
	pg := p.Pages[c.Page]
	if pg == nil || pg.Manifest == nil {
		return nil
	}
	page := pair(pg.Manifest)
	if c.SlotPath == "" {
		return []rootPair{page}
	}
	prefix, err := semdiff.ParsePath(c.SlotPath)
	if err != nil {
		return nil
	}
	raw, red := walkPair(page.raw, page.red, prefix)
	if raw == nil {
		return nil
	}
	return []rootPair{{raw, red}}
}

// walkPair follows segs through a raw document and its redacted copy in step,
// by position, because redaction may rename members. It returns nil, nil when
// the path does not exist.
func walkPair(raw, red *jsondoc.Value, segs []semdiff.Segment) (*jsondoc.Value, *jsondoc.Value) {
	for _, sg := range segs {
		if raw == nil || red == nil {
			return nil, nil
		}
		if sg.IsIndex {
			ri, di := raw.Items(), red.Items()
			if sg.Index >= len(ri) || sg.Index >= len(di) {
				return nil, nil
			}
			raw, red = ri[sg.Index], di[sg.Index]
			continue
		}
		rm, dm := raw.Members(), red.Members()
		found := false
		for i, m := range rm {
			if m.Name == sg.Name && i < len(dm) {
				raw, red, found = m.Value, dm[i].Value, true
				break
			}
		}
		if !found {
			return nil, nil
		}
	}
	return raw, red
}
