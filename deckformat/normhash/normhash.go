// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package normhash computes the normalized profile hash defined in contract C
// § normalized hash (docs/contracts/profile-format.md), norm_version 1. The
// hash must change on a real user edit and on nothing else. The definition
// lives in contract C; this package implements it and nothing more.
package normhash

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// NormVersion is the contract C definition this package implements. Any
// change to the steps below, including StripList, increments it (and ships
// as a store FORMAT migration, ADR 0027).
const NormVersion = 1

// StripList is contract C step 3's strip list: runtime and per-instance
// fields removed before hashing. Paths are member-name paths; Top paths are
// relative to the top-level manifest, Action paths to each action object
// (Controllers[].Actions.<slot>).
var StripList = struct {
	Top    [][]string
	Action [][]string
}{
	Top:    [][]string{{"Device", "UUID"}, {"Pages", "Current"}},
	Action: [][]string{{"State"}, {"ActionID"}},
}

// ErrDanglingPage means Pages.Pages or Pages.Default names a page that has no
// folder.
var ErrDanglingPage = errors.New("page reference without a page folder")

// Doc is one normalized manifest: its canonical path and its tree after
// stripping and relabeling (before JCS).
type Doc struct {
	Path  string
	Value *jsondoc.Value
}

// options exist so tests can switch a step off and show the hash then fails
// its job (known-bad). Production code always uses all steps.
type options struct {
	strip, relabelPages, hashImages bool
}

var full = options{strip: true, relabelPages: true, hashImages: true}

// Normalize returns the profile's normalized manifests, sorted by path.
func Normalize(p *profile.Profile) ([]Doc, error) { return normalize(p, full) }

// Hash returns the normalized hash (hex sha256).
func Hash(p *profile.Profile) (string, error) {
	docs, err := Normalize(p)
	if err != nil {
		return "", err
	}
	return HashDocs(docs)
}

// HashDocs hashes normalized docs: sha256 over "<path>\x00<hex sha256 of the
// JCS bytes>\n" lines sorted bytewise by path (contract C step 4).
func HashDocs(docs []Doc) (string, error) {
	lines := make([]string, 0, len(docs))
	for _, d := range docs {
		c, err := d.Value.Canonical()
		if err != nil {
			return "", fmt.Errorf("%s: %w", d.Path, err)
		}
		sum := sha256.Sum256(c)
		lines = append(lines, d.Path+"\x00"+hex.EncodeToString(sum[:])+"\n")
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PageLabels maps each lower-case page id to its canonical label (contract C
// step 2): "page/<i>" for the i-th entry of Pages.Pages, "default" for
// Pages.Default, "other/<folder>" for any other page folder.
func PageLabels(p *profile.Profile) (map[string]string, error) {
	order, err := p.PageOrder()
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	for i, id := range order {
		if _, dup := labels[id]; dup {
			return nil, fmt.Errorf("%s: page %s listed twice in Pages.Pages: %w", p.Folder, id, profile.ErrMalformed)
		}
		labels[id] = fmt.Sprintf("page/%d", i)
	}
	if def := p.DefaultPage(); def != "" {
		if _, clash := labels[def]; clash {
			return nil, fmt.Errorf("%s: Pages.Default is also in Pages.Pages: %w", p.Folder, profile.ErrMalformed)
		}
		labels[def] = "default"
	}
	for id := range labels {
		if p.Pages[id] == nil {
			return nil, fmt.Errorf("%s: %s: %w", p.Folder, id, ErrDanglingPage)
		}
	}
	for id := range p.Pages {
		if _, ok := labels[id]; !ok {
			labels[id] = "other/" + strings.ToLower(id)
		}
	}
	return labels, nil
}

func normalize(p *profile.Profile, o options) ([]Doc, error) {
	labels, err := PageLabels(p)
	if err != nil {
		return nil, err
	}
	label := func(id string) string {
		if !o.relabelPages {
			return id
		}
		return labels[strings.ToLower(id)]
	}

	top := p.Manifest.Clone()
	if o.strip {
		for _, path := range StripList.Top {
			deletePath(top, path)
		}
	}
	if o.relabelPages {
		if list := top.Lookup("Pages", "Pages"); list != nil {
			for _, it := range list.Items() {
				if s, ok := it.Str(); ok {
					it.SetString(label(s))
				}
			}
		}
		if def := top.Lookup("Pages", "Default"); def != nil {
			if s, ok := def.Str(); ok && s != "" {
				def.SetString(label(s))
			}
		}
	}
	if o.hashImages {
		replaceImageRefs(top, p.Images)
	}
	docs := []Doc{{Path: "manifest.json", Value: top}}

	for id, pg := range p.Pages {
		m := pg.Manifest.Clone()
		forEachAction(m, func(action *jsondoc.Value) {
			if o.strip {
				for _, path := range StripList.Action {
					deletePath(action, path)
				}
			}
			if o.relabelPages {
				if settings := action.Get("Settings"); settings != nil {
					settings.Walk(func(_ []string, v *jsondoc.Value) {
						if s, ok := v.Str(); ok {
							if l, isPage := labels[strings.ToLower(s)]; isPage {
								v.SetString(l)
							}
						}
					})
				}
			}
		})
		if o.hashImages {
			replaceImageRefs(m, pg.Images)
		}
		dir := pg.Folder
		if o.relabelPages {
			dir = labels[id]
		}
		docs = append(docs, Doc{Path: norm.NFC.String(dir + "/manifest.json"), Value: m})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, nil
}

func deletePath(v *jsondoc.Value, path []string) {
	parent := v.Lookup(path[:len(path)-1]...)
	if parent != nil {
		parent.Delete(path[len(path)-1])
	}
}

// forEachAction calls fn for every action object: Controllers[].Actions.<slot>.
func forEachAction(page *jsondoc.Value, fn func(*jsondoc.Value)) {
	controllers := page.Get("Controllers")
	for _, c := range controllers.Items() {
		for _, m := range c.Get("Actions").Members() {
			if m.Value.Kind() == jsondoc.Object {
				fn(m.Value)
			}
		}
	}
}

// replaceImageRefs turns every "Images/<file>" string into the referenced
// file's content hash, or "missing:<reference>" when the file is absent
// (contract C step 3).
func replaceImageRefs(m *jsondoc.Value, images map[string][]byte) {
	m.Walk(func(_ []string, v *jsondoc.Value) {
		s, ok := v.Str()
		if !ok || !strings.HasPrefix(s, "Images/") {
			return
		}
		data, found := images[strings.TrimPrefix(s, "Images/")]
		if !found {
			v.SetString("missing:" + s)
			return
		}
		sum := sha256.Sum256(data)
		v.SetString("sha256:" + hex.EncodeToString(sum[:]))
	})
}
