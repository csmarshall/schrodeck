// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package profile reads Stream Deck profiles (ProfilesV3/<UUID>.sdProfile)
// into memory: manifests as order- and literal-preserving JSON trees, images
// as bytes. It reads through fs.FS only, so it cannot write the app's files.
// Layout and allow-list: contract C (docs/contracts/profile-format.md), P1 and P7.
package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
)

// KnownVersion is the profile manifest Version this toolkit was verified
// against (contract C P2).
const KnownVersion = "3.0"

// Suffix is the extension of a profile folder.
const Suffix = ".sdProfile"

// Page is one page folder: Profiles/<Folder>/.
type Page struct {
	Folder   string // as on disk (observed upper-case)
	Manifest *jsondoc.Value
	Images   map[string][]byte // file name -> bytes
}

// Profile is one <UUID>.sdProfile folder.
type Profile struct {
	Folder   string // "<UUID>.sdProfile", as on disk
	Manifest *jsondoc.Value
	Images   map[string][]byte // top-level Images/
	Pages    map[string]*Page  // key: lower-case page folder name
	// Junk lists ignored files (relative paths), e.g. .DS_Store.
	Junk []string
}

// UnexpectedFileError reports a path outside the allow-list (contract C P7).
type UnexpectedFileError struct {
	Profile string
	Path    string
}

func (e *UnexpectedFileError) Error() string {
	return fmt.Sprintf("%s: unexpected path %q (not in contract C's allow-list)", e.Profile, e.Path)
}

// IsJunk reports whether a file name is known noise that is ignored for
// hashing and copying (contract C § file allow-list).
func IsJunk(name string) bool {
	switch {
	case name == ".DS_Store":
		return true
	case strings.Contains(name, " (conflicted copy"):
		return true
	case strings.HasSuffix(name, ".icloud"):
		return true
	case strings.HasPrefix(name, "~$"):
		return true
	case strings.HasSuffix(name, "~"):
		return true
	case strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".swp"):
		return true
	case strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#"):
		return true
	}
	return false
}

type entryKind int

const (
	allowedDir entryKind = iota
	topManifest
	topImage
	pageManifest
	pageImage
	unexpected
)

func classify(parts []string, isDir bool) entryKind {
	switch {
	case len(parts) == 1 && parts[0] == "manifest.json" && !isDir:
		return topManifest
	case len(parts) == 1 && (parts[0] == "Images" || parts[0] == "Profiles") && isDir:
		return allowedDir
	case len(parts) == 2 && parts[0] == "Images" && !isDir:
		return topImage
	case len(parts) == 2 && parts[0] == "Profiles" && isDir:
		return allowedDir
	case len(parts) == 3 && parts[0] == "Profiles" && parts[2] == "manifest.json" && !isDir:
		return pageManifest
	case len(parts) == 3 && parts[0] == "Profiles" && parts[2] == "Images" && isDir:
		return allowedDir
	case len(parts) == 4 && parts[0] == "Profiles" && parts[2] == "Images" && !isDir:
		return pageImage
	}
	return unexpected
}

// Load reads the profile folder named folder from fsys (fsys is rooted like
// ProfilesV3). It fails on a path outside the allow-list, a missing manifest
// or a manifest that does not round-trip (jsondoc.ErrNotRoundTrip).
func Load(fsys fs.FS, folder string) (*Profile, error) {
	p := &Profile{Folder: folder, Images: map[string][]byte{}, Pages: map[string]*Page{}}
	page := func(name string) *Page {
		key := strings.ToLower(name)
		if p.Pages[key] == nil {
			p.Pages[key] = &Page{Folder: name, Images: map[string][]byte{}}
		}
		return p.Pages[key]
	}
	err := fs.WalkDir(fsys, folder, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if full == folder {
			return nil
		}
		rel := strings.TrimPrefix(full, folder+"/")
		if IsJunk(d.Name()) {
			p.Junk = append(p.Junk, rel)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		parts := strings.Split(rel, "/")
		kind := classify(parts, d.IsDir())
		if kind == unexpected {
			return &UnexpectedFileError{Profile: folder, Path: rel}
		}
		if kind == allowedDir {
			return nil
		}
		data, err := fs.ReadFile(fsys, full)
		if err != nil {
			return err
		}
		switch kind {
		case topManifest, pageManifest:
			doc, err := jsondoc.Parse(data)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", folder, rel, err)
			}
			if kind == topManifest {
				p.Manifest = doc
			} else {
				page(parts[1]).Manifest = doc
			}
		case topImage:
			p.Images[parts[1]] = data
		case pageImage:
			page(parts[1]).Images[parts[3]] = data
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if p.Manifest == nil {
		return nil, fmt.Errorf("%s: no manifest.json", folder)
	}
	for _, pg := range p.Pages {
		if pg.Manifest == nil {
			return nil, fmt.Errorf("%s: page folder %s has no manifest.json", folder, pg.Folder)
		}
	}
	return p, nil
}

// LoadResult is every profile found in a ProfilesV3-shaped file system.
type LoadResult struct {
	Profiles []*Profile
	// Errors holds one error per profile that failed to load.
	Errors []error
	// Skipped lists top-level entries that are not profile folders.
	Skipped []string
}

// LoadAll loads every *.sdProfile folder at the root of fsys. One broken
// profile does not hide the others.
func LoadAll(fsys fs.FS) (LoadResult, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return LoadResult{}, err
	}
	var res LoadResult
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), Suffix) {
			res.Skipped = append(res.Skipped, e.Name())
			continue
		}
		p, err := Load(fsys, e.Name())
		if err != nil {
			res.Errors = append(res.Errors, err)
			continue
		}
		res.Profiles = append(res.Profiles, p)
	}
	return res, nil
}

func str(v *jsondoc.Value) string {
	s, _ := v.Str()
	return s
}

// Version is the manifest's Version.
func (p *Profile) Version() string { return str(p.Manifest.Get("Version")) }

// Name is the profile's display name.
func (p *Profile) Name() string { return str(p.Manifest.Get("Name")) }

// DeviceUUID is the top-level Device.UUID (it embeds the deck's serial, R9).
func (p *Profile) DeviceUUID() string { return str(p.Manifest.Lookup("Device", "UUID")) }

// DeviceModel is the top-level Device.Model.
func (p *Profile) DeviceModel() string { return str(p.Manifest.Lookup("Device", "Model")) }

// AppIdentifier returns the smart-profile field, if present (U3).
func (p *Profile) AppIdentifier() (string, bool) {
	v := p.Manifest.Get("AppIdentifier")
	if v == nil {
		return "", false
	}
	return v.Str()
}

// ErrMalformed marks a manifest whose structure is not what contract C says.
var ErrMalformed = errors.New("malformed manifest")

// PageOrder returns Pages.Pages, lower-cased, in order.
func (p *Profile) PageOrder() ([]string, error) {
	list := p.Manifest.Lookup("Pages", "Pages")
	if list == nil || list.Kind() != jsondoc.Array {
		return nil, fmt.Errorf("%s: Pages.Pages is not a list: %w", p.Folder, ErrMalformed)
	}
	var out []string
	for _, it := range list.Items() {
		s, ok := it.Str()
		if !ok {
			return nil, fmt.Errorf("%s: Pages.Pages holds a non-string: %w", p.Folder, ErrMalformed)
		}
		out = append(out, strings.ToLower(s))
	}
	return out, nil
}

// DefaultPage returns Pages.Default, lower-cased ("" if absent).
func (p *Profile) DefaultPage() string {
	return strings.ToLower(str(p.Manifest.Lookup("Pages", "Default")))
}

// Files returns every allow-listed file as relative path -> bytes. Manifests
// are re-encoded from their trees, which Load guarantees is byte-identical
// to what was read.
func (p *Profile) Files() map[string][]byte {
	out := map[string][]byte{"manifest.json": p.Manifest.Encode()}
	for name, b := range p.Images {
		out["Images/"+name] = b
	}
	for _, pg := range p.Pages {
		out[path.Join("Profiles", pg.Folder, "manifest.json")] = pg.Manifest.Encode()
		for name, b := range pg.Images {
			out[path.Join("Profiles", pg.Folder, "Images", name)] = b
		}
	}
	return out
}

// SortedPageKeys returns the page keys in a stable order.
func (p *Profile) SortedPageKeys() []string {
	keys := make([]string, 0, len(p.Pages))
	for k := range p.Pages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
