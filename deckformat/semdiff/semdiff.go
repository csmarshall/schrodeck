// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package semdiff reports differences between profiles in Stream Deck terms
// ("page 2 › key 3,1", "Settings.entity") instead of file paths. Raw mode
// compares manifests as stored, runtime fields and ids included, which is
// what observing the app needs. Semantic mode compares the normalized forms
// of contract C, which is what "did the user change anything" needs.
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

// Values compares two JSON trees and returns one change per differing leaf.
// where and profileName label every change.
func Values(profileName, where string, a, b *jsondoc.Value) []Change {
	var out []Change
	emit := func(path []string, kind Kind, x, y *jsondoc.Value) {
		c := Change{Profile: profileName, Where: where, Path: renderPath(path), Kind: kind}
		if x != nil {
			c.Before = string(x.Encode())
		}
		if y != nil {
			c.After = string(y.Encode())
		}
		out = append(out, c)
	}
	diffValues(nil, a, b, emit)
	return out
}

func diffValues(path []string, a, b *jsondoc.Value, emit func([]string, Kind, *jsondoc.Value, *jsondoc.Value)) {
	switch {
	case a == nil && b == nil:
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
			diffValues(append(path[:len(path):len(path)], m.Name), m.Value, b.Get(m.Name), emit)
		}
		for _, m := range b.Members() {
			if !seen[m.Name] {
				emit(append(path[:len(path):len(path)], m.Name), Added, nil, m.Value)
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
			diffValues(append(path[:len(path):len(path)], "["+strconv.Itoa(i)+"]"), x, y, emit)
		}
	default:
		if !bytes.Equal(a.Raw(), b.Raw()) {
			emit(path, Modified, a, b)
		}
	}
}

func renderPath(path []string) string {
	var b strings.Builder
	for _, p := range path {
		if strings.HasPrefix(p, "[") {
			b.WriteString(p)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(p)
	}
	return b.String()
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
func pageChanges(profileName, page string, a, b *jsondoc.Value) []Change {
	var out []Change
	for _, c := range Values(profileName, page, a, b) {
		parts := strings.SplitN(c.Path, ".", 3)
		// Controllers[i].Actions.<slot>[.rest]
		if len(parts) >= 2 && strings.HasPrefix(parts[0], "Controllers[") && strings.HasPrefix(parts[1], "Actions") {
			rest := ""
			if len(parts) == 3 {
				rest = parts[2]
			}
			slot, tail, _ := strings.Cut(rest, ".")
			if slot != "" {
				control := "key"
				if ctl := controllerType(a, b, parts[0]); ctl == "Encoder" {
					control = "dial"
				}
				c.Where = page + " › " + control + " " + slot
				c.Path = tail
			}
		}
		out = append(out, c)
	}
	return out
}

// controllerType returns the Type of the controller named by "Controllers[i]".
func controllerType(a, b *jsondoc.Value, elem string) string {
	i, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(elem, "Controllers["), "]"))
	if err != nil {
		return ""
	}
	for _, m := range []*jsondoc.Value{b, a} {
		items := m.Get("Controllers").Items()
		if i < len(items) {
			if s, ok := items[i].Get("Type").Str(); ok {
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
		return docChanges(name, docMap(a), docMap(b)), nil
	}
	return rawChanges(name, before, after)
}

func docMap(docs []normhash.Doc) map[string]*jsondoc.Value {
	m := map[string]*jsondoc.Value{}
	for _, d := range docs {
		m[d.Path] = d.Value
	}
	return m
}

func docChanges(name string, a, b map[string]*jsondoc.Value) []Change {
	var out []Change
	for _, path := range unionKeys(a, b) {
		if path == "manifest.json" {
			out = append(out, Values(name, "profile", a[path], b[path])...)
			continue
		}
		label := strings.TrimSuffix(path, "/manifest.json")
		out = append(out, pageChanges(name, humanPage(label), a[path], b[path])...)
	}
	return out
}

func rawChanges(name string, before, after *profile.Profile) ([]Change, error) {
	out := Values(name, "profile", before.Manifest, after.Manifest)
	labels := func(p *profile.Profile) map[string]string {
		l, err := normhash.PageLabels(p)
		if err != nil {
			l = map[string]string{}
		}
		return l
	}
	la, lb := labels(before), labels(after)
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
		out = append(out, pageChanges(name, humanPage(label)+" ("+k+")", a, b)...)
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
