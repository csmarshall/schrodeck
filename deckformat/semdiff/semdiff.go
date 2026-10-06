// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package semdiff reports differences between profiles in Stream Deck terms
// ("page 2 › key 3,1", "Settings.entity") instead of file paths. Raw mode
// compares manifests as stored, runtime fields and ids included, which is
// what observing the app needs. Semantic mode compares the normalized forms
// of contract C, which is what "did the user change anything" needs.
//
// Path grammar: a path is a sequence of member names and array indices, joined so that two different paths never render alike. A member name is written as is when it is non-empty and contains none of . [ ] " (so "Settings", "a b" and "é" are bare); otherwise it is quoted as ["…"] with " and \ escaped inside. An array index is written [i]. Member names after the first segment are prefixed with ".", array indices are not: "a[0].b" for a member b of the first item of a, and a.["[0]"] for a member of a that is literally named "[0]".
package semdiff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/normhash"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// seg represents a path segment: either a member name or an array index.
type seg struct {
	name  string // member name, empty if isIdx
	idx   int    // array index, valid only if isIdx
	isIdx bool   // true if this is an array index
}

// Kind of change.
type Kind string

// Change kinds.
const (
	Added    Kind = "added"
	Removed  Kind = "removed"
	Modified Kind = "modified"
)

// Mode selects what is compared.
type Mode int

// Modes.
const (
	Raw      Mode = iota // manifests and files as stored
	Semantic             // contract C normalized forms
)

// Change is one difference.
type Change struct {
	Profile string `json:"profile"` // profile name, or folder when unnamed
	Where   string `json:"where"`   // "profile", "page 2 › key 3,1", "files", ...
	Path    string `json:"path"`    // key path inside Where
	Kind    Kind   `json:"kind"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	// Page identifies the page a page change was found in: its Profile.Pages
	// key in raw mode, its canonical label ("page/0", "default", ...) in
	// semantic mode. Empty for changes outside pages. Not serialized: it is
	// a routing key for tools that resolve Path back into the documents.
	Page string `json:"-"`
	// SlotPath is the rendered path, inside that page's manifest, of the
	// action slot Path is relative to (e.g. "Controllers[0].Actions.0,0").
	// Empty when Path is relative to the page manifest itself.
	SlotPath string `json:"-"`
}

// String renders a change on one line.
func (c Change) String() string {
	loc := c.Where
	if c.Path != "" {
		loc += " › " + c.Path
	}
	switch c.Kind {
	case Added:
		return fmt.Sprintf("%s: %s added: %s", c.Profile, loc, c.After)
	case Removed:
		return fmt.Sprintf("%s: %s removed (was %s)", c.Profile, loc, c.Before)
	}
	return fmt.Sprintf("%s: %s changed: %s → %s", c.Profile, loc, c.Before, c.After)
}

// needsQuote reports whether a member name needs quoting in a path.
func needsQuote(name string) bool {
	if name == "" {
		return true
	}
	for _, ch := range name {
		if ch == '.' || ch == '[' || ch == ']' || ch == '"' {
			return true
		}
	}
	return false
}

// renderPathSegs converts structured segments into an injective string path,
// quoting member names that contain special characters.
func renderPathSegs(segments []seg) string {
	if len(segments) == 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range segments {
		if s.isIdx {
			fmt.Fprintf(&b, "[%d]", s.idx)
		} else {
			// Add dot before member name if not the first segment
			if i > 0 {
				b.WriteByte('.')
			}
			if needsQuote(s.name) {
				b.WriteString(`["`)
				// Escape " and \ inside quoted names
				for _, ch := range s.name {
					if ch == '"' || ch == '\\' {
						b.WriteByte('\\')
					}
					b.WriteRune(ch)
				}
				b.WriteString(`"]`)
			} else {
				b.WriteString(s.name)
			}
		}
	}
	return b.String()
}

// RenderPath renders segments exactly as Change.Path is rendered, so
// RenderPath(ParsePath(p)) == p.
func RenderPath(segs []Segment) string {
	out := make([]seg, len(segs))
	for i, s := range segs {
		out[i] = seg{name: s.Name, idx: s.Index, isIdx: s.IsIndex}
	}
	return renderPathSegs(out)
}

// Segment is one step of a rendered path: a member name or an array index.
type Segment struct {
	Name    string // member name, empty if IsIndex
	Index   int    // array index, valid only if IsIndex
	IsIndex bool
}

// ParsePath inverts the rendering of Change.Path exactly, including the
// quoted .["…"] member form and its escapes. A string the renderer could not
// have produced is an error.
func ParsePath(path string) ([]Segment, error) {
	var segs []seg
	i := 0
	bad := func(why string) ([]Segment, error) {
		return nil, fmt.Errorf("semdiff: path %q: %s at offset %d", path, why, i)
	}
	for i < len(path) {
		if len(segs) > 0 && path[i] != '[' {
			if path[i] != '.' {
				return bad("expected '.' or '['")
			}
			i++
			if i >= len(path) {
				return bad("path ends after '.'")
			}
		}
		if path[i] == '[' {
			i++
			if i < len(path) && path[i] == '"' {
				i++
				var name []byte
				closed := false
				for i < len(path) {
					c := path[i]
					if c == '\\' && i+1 < len(path) {
						name = append(name, path[i+1])
						i += 2
						continue
					}
					if c == '"' {
						closed = true
						i++
						break
					}
					name = append(name, c)
					i++
				}
				if !closed || i >= len(path) || path[i] != ']' {
					return bad("unterminated quoted member")
				}
				i++
				segs = append(segs, seg{name: string(name)})
				continue
			}
			start := i
			for i < len(path) && path[i] >= '0' && path[i] <= '9' {
				i++
			}
			if start == i || i >= len(path) || path[i] != ']' {
				return bad("malformed array index")
			}
			n, err := strconv.Atoi(path[start:i])
			if err != nil {
				return bad("array index out of range")
			}
			i++
			segs = append(segs, seg{idx: n, isIdx: true})
			continue
		}
		start := i
		for i < len(path) && path[i] != '.' && path[i] != '[' {
			i++
		}
		segs = append(segs, seg{name: path[start:i]})
	}
	if renderPathSegs(segs) != path {
		return nil, fmt.Errorf("semdiff: path %q is not in canonical rendered form", path)
	}
	out := make([]Segment, len(segs))
	for k, sg := range segs {
		out[k] = Segment{Name: sg.name, Index: sg.idx, IsIndex: sg.isIdx}
	}
	return out, nil
}

// Values compares two JSON trees and returns one change per differing leaf.
// where and profileName label every change. An object or array that is added
// or removed as a whole is one change carrying the whole subtree.
func Values(profileName, where string, a, b *jsondoc.Value) []Change {
	return values(profileName, where, a, b, comparer{equal: rawEqual})
}

// ValueLeaves is Values, except that an added or removed object or array is
// reported as one change per leaf, each with its own path (an empty object or
// array is itself a leaf). Tools that render or redact one scalar at a time,
// such as observation reports, need every change to name a single leaf.
func ValueLeaves(profileName, where string, a, b *jsondoc.Value) []Change {
	return values(profileName, where, a, b, comparer{equal: rawEqual, leaves: true})
}

// comparer is how two trees are compared: when scalars are equal, and whether
// a wholly added or removed subtree is reported leaf by leaf.
type comparer struct {
	equal  scalarEqual
	leaves bool
}

// expands reports whether v is a subtree cmp reports leaf by leaf.
func (cmp comparer) expands(v *jsondoc.Value) bool {
	switch v.Kind() {
	case jsondoc.Object:
		return cmp.leaves && len(v.Members()) > 0
	case jsondoc.Array:
		return cmp.leaves && len(v.Items()) > 0
	}
	return false
}

// scalarEqual decides whether two scalars of the same kind are the same value.
type scalarEqual func(a, b *jsondoc.Value) bool

// rawEqual compares scalars as stored: 1.50 and 1.5 differ.
func rawEqual(a, b *jsondoc.Value) bool { return bytes.Equal(a.Raw(), b.Raw()) }

// canonicalEqual compares scalars by their RFC 8785 form, the form the
// normalized hash uses, so a spelling the hash ignores is not a change either.
// A scalar with no canonical form (a number JCS cannot carry exactly) falls
// back to its stored spelling.
func canonicalEqual(a, b *jsondoc.Value) bool {
	ca, errA := a.Canonical()
	cb, errB := b.Canonical()
	if errA != nil || errB != nil {
		return rawEqual(a, b)
	}
	return bytes.Equal(ca, cb)
}

func values(profileName, where string, a, b *jsondoc.Value, cmp comparer) []Change {
	var out []Change
	emit := func(path []seg, kind Kind, x, y *jsondoc.Value) {
		c := Change{Profile: profileName, Where: where, Path: renderPathSegs(path), Kind: kind}
		if x != nil {
			c.Before = string(x.Encode())
		}
		if y != nil {
			c.After = string(y.Encode())
		}
		out = append(out, c)
	}
	diffValues(nil, a, b, emit, cmp)
	return out
}

func diffValues(path []seg, a, b *jsondoc.Value, emit func([]seg, Kind, *jsondoc.Value, *jsondoc.Value), cmp comparer) {
	switch {
	case a == nil && b == nil:
		return
	case a == nil && cmp.expands(b):
		eachChild(path, b, func(p []seg, v *jsondoc.Value) { diffValues(p, nil, v, emit, cmp) })
		return
	case b == nil && cmp.expands(a):
		eachChild(path, a, func(p []seg, v *jsondoc.Value) { diffValues(p, v, nil, emit, cmp) })
		return
	case a == nil:
		emit(path, Added, nil, b)
		return
	case b == nil:
		emit(path, Removed, a, nil)
		return
	case a.Kind() != b.Kind():
		emit(path, Modified, a, b)
		return
	}
	switch a.Kind() {
	case jsondoc.Object:
		seen := map[string]bool{}
		for _, m := range a.Members() {
			seen[m.Name] = true
			diffValues(append(path[:len(path):len(path)], seg{name: m.Name}), m.Value, b.Get(m.Name), emit, cmp)
		}
		for _, m := range b.Members() {
			if !seen[m.Name] {
				diffValues(append(path[:len(path):len(path)], seg{name: m.Name}), nil, m.Value, emit, cmp)
			}
		}
	case jsondoc.Array:
		ai, bi := a.Items(), b.Items()
		for i := 0; i < len(ai) || i < len(bi); i++ {
			var x, y *jsondoc.Value
			if i < len(ai) {
				x = ai[i]
			}
			if i < len(bi) {
				y = bi[i]
			}
			diffValues(append(path[:len(path):len(path)], seg{idx: i, isIdx: true}), x, y, emit, cmp)
		}
	default:
		if !cmp.equal(a, b) {
			emit(path, Modified, a, b)
		}
	}
}

// eachChild calls fn with the path and value of every member or item of v.
func eachChild(path []seg, v *jsondoc.Value, fn func([]seg, *jsondoc.Value)) {
	for _, m := range v.Members() {
		fn(append(path[:len(path):len(path)], seg{name: m.Name}), m.Value)
	}
	for i, it := range v.Items() {
		fn(append(path[:len(path):len(path)], seg{idx: i, isIdx: true}), it)
	}
}

// humanPage turns a canonical page label into words.
func humanPage(label string) string {
	switch {
	case label == "default":
		return "default page"
	case strings.HasPrefix(label, "page/"):
		n, err := strconv.Atoi(strings.TrimPrefix(label, "page/"))
		if err == nil {
			return fmt.Sprintf("page %d", n+1)
		}
	case strings.HasPrefix(label, "other/"):
		return "sub-page " + strings.TrimPrefix(label, "other/")
	}
	return "page " + label
}

// pageChanges compares two page manifests and moves the key slot out of the
// path into Where: "page 1 › key 3,1" + "Settings.path".
// It works on the structured path representation to avoid re-splitting collisions.
func pageChanges(profileName, page, pageID string, a, b *jsondoc.Value, cmp comparer) []Change {
	var out []Change

	// Walk through differences and rebuild paths from segments
	emit := func(path []seg, kind Kind, x, y *jsondoc.Value) {
		// Check if this is a Controllers[i].Actions.<slot> path
		if len(path) >= 3 &&
			path[0].name == "Controllers" &&
			!path[0].isIdx &&
			path[1].isIdx &&
			path[2].name == "Actions" &&
			!path[2].isIdx {

			// Extract the controller index
			ctlIdx := path[1].idx

			// Everything after Controllers[i].Actions should be the slot and tail
			slot := ""
			var tail []seg
			if len(path) > 3 {
				// Next segment is the slot name
				slot = path[3].name
				// Everything after is the tail
				if len(path) > 4 {
					tail = path[4:]
				}
			}

			if slot != "" {
				control := "key"
				if ctl := controllerType(a, b, ctlIdx); ctl == "Encoder" {
					control = "dial"
				}

				c := Change{
					Profile:  profileName,
					Where:    page + " › " + control + " " + slot,
					Path:     renderPathSegs(tail),
					Kind:     kind,
					Page:     pageID,
					SlotPath: renderPathSegs(path[:4]),
				}
				if x != nil {
					c.Before = string(x.Encode())
				}
				if y != nil {
					c.After = string(y.Encode())
				}
				out = append(out, c)
				return
			}
		}

		// Not a controller slot, emit as normal
		c := Change{
			Profile: profileName,
			Where:   page,
			Path:    renderPathSegs(path),
			Kind:    kind,
			Page:    pageID,
		}
		if x != nil {
			c.Before = string(x.Encode())
		}
		if y != nil {
			c.After = string(y.Encode())
		}
		out = append(out, c)
	}

	diffValues(nil, a, b, emit, cmp)
	return out
}

// controllerType returns the Type of the controller at the given index.
func controllerType(a, b *jsondoc.Value, idx int) string {
	for _, m := range []*jsondoc.Value{b, a} {
		items := m.Get("Controllers").Items()
		if idx < len(items) {
			if s, ok := items[idx].Get("Type").Str(); ok {
				return s
			}
		}
	}
	return ""
}

func displayName(p *profile.Profile) string {
	if n := p.Name(); n != "" {
		return n
	}
	return p.Folder
}

// Profiles compares two versions of one profile.
func Profiles(before, after *profile.Profile, mode Mode) ([]Change, error) {
	return profiles(before, after, mode, false)
}

// ProfileLeaves is Profiles with the per-leaf reporting of ValueLeaves: an
// added or removed object or array (a new key, a new page) is one change per
// leaf, still placed in its page and key slot.
func ProfileLeaves(before, after *profile.Profile, mode Mode) ([]Change, error) {
	return profiles(before, after, mode, true)
}

func profiles(before, after *profile.Profile, mode Mode, leaves bool) ([]Change, error) {
	name := displayName(after)
	if mode == Semantic {
		a, err := normhash.Normalize(before)
		if err != nil {
			return nil, err
		}
		b, err := normhash.Normalize(after)
		if err != nil {
			return nil, err
		}
		return docChanges(name, docMap(a), docMap(b), comparer{equal: canonicalEqual, leaves: leaves}), nil
	}
	return rawChanges(name, before, after, comparer{equal: rawEqual, leaves: leaves})
}

func docMap(docs []normhash.Doc) map[string]*jsondoc.Value {
	m := map[string]*jsondoc.Value{}
	for _, d := range docs {
		m[d.Path] = d.Value
	}
	return m
}

func docChanges(name string, a, b map[string]*jsondoc.Value, cmp comparer) []Change {
	var out []Change
	for _, path := range unionKeys(a, b) {
		if path == "manifest.json" {
			out = append(out, values(name, "profile", a[path], b[path], cmp)...)
			continue
		}
		label := strings.TrimSuffix(path, "/manifest.json")
		out = append(out, pageChanges(name, humanPage(label), label, a[path], b[path], cmp)...)
	}
	return out
}

func rawChanges(name string, before, after *profile.Profile, cmp comparer) ([]Change, error) {
	out := values(name, "profile", before.Manifest, after.Manifest, cmp)

	la, err := normhash.PageLabels(before)
	if err != nil {
		return nil, fmt.Errorf("page labels (before): %w", err)
	}
	lb, err := normhash.PageLabels(after)
	if err != nil {
		return nil, fmt.Errorf("page labels (after): %w", err)
	}

	keys := map[string]bool{}
	for k := range before.Pages {
		keys[k] = true
	}
	for k := range after.Pages {
		keys[k] = true
	}
	var sortedKeys []string
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	for _, k := range sortedKeys {
		label := lb[k]
		if label == "" {
			label = la[k]
		}
		if label == "" {
			label = "other/" + k
		}
		var a, b *jsondoc.Value
		if pg := before.Pages[k]; pg != nil {
			a = pg.Manifest
		}
		if pg := after.Pages[k]; pg != nil {
			b = pg.Manifest
		}
		out = append(out, pageChanges(name, humanPage(label)+" ("+k+")", k, a, b, cmp)...)
	}
	out = append(out, fileChanges(name, before.Files(), after.Files())...)
	return out, nil
}

// fileChanges reports added, removed and changed non-manifest files by
// content digest.
func fileChanges(name string, a, b map[string][]byte) []Change {
	digest := func(data []byte) string {
		sum := sha256.Sum256(data)
		return "sha256:" + hex.EncodeToString(sum[:8])
	}
	var out []Change
	for _, path := range unionKeys(a, b) {
		if strings.HasSuffix(path, "manifest.json") {
			continue
		}
		x, inA := a[path]
		y, inB := b[path]
		switch {
		case !inA:
			out = append(out, Change{Profile: name, Where: "files", Path: path, Kind: Added, After: digest(y)})
		case !inB:
			out = append(out, Change{Profile: name, Where: "files", Path: path, Kind: Removed, Before: digest(x)})
		case !bytes.Equal(x, y):
			out = append(out, Change{Profile: name, Where: "files", Path: path, Kind: Modified, Before: digest(x), After: digest(y)})
		}
	}
	return out
}

func unionKeys[V any](a, b map[string]V) []string {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Sets compares two sets of profiles keyed by folder name: whole profiles
// added or removed, and changes inside profiles present in both.
func Sets(before, after map[string]*profile.Profile, mode Mode) ([]Change, error) {
	var out []Change
	for _, folder := range unionKeys(before, after) {
		a, b := before[folder], after[folder]
		switch {
		case a == nil:
			out = append(out, Change{Profile: displayName(b), Where: "profiles", Path: folder, Kind: Added, After: displayName(b)})
		case b == nil:
			out = append(out, Change{Profile: displayName(a), Where: "profiles", Path: folder, Kind: Removed, Before: displayName(a)})
		default:
			cs, err := Profiles(a, b, mode)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", folder, err)
			}
			out = append(out, cs...)
		}
	}
	return out, nil
}
