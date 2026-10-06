// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"sort"
	"strings"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
)

// Schema is the input of ADR 0015's format fingerprint: the profile Version,
// the structural key paths of every manifest and the file-name patterns
// present.
type Schema struct {
	Version      string   `json:"version"`
	KeyPaths     []string `json:"key_paths"`
	FilePatterns []string `json:"file_patterns"`
}

// opaqueMembers hold plugin-defined content, not format structure: key paths
// stop at them (ADR 0015's example path ends at ...Actions.*.Settings).
var opaqueMembers = map[string]bool{"Settings": true}

// wildcardMembers are objects whose member NAMES are data (key slots such as
// "3,1"); their names collapse to "*".
var wildcardMembers = map[string]bool{"Actions": true}

// SchemaOf computes the schema over a set of profiles. Profiles with
// different Version values are an error: one fingerprint cannot describe two
// formats.
func SchemaOf(ps []*Profile) (Schema, error) {
	keys := map[string]bool{}
	patterns := map[string]bool{}
	version := ""
	for _, p := range ps {
		if version == "" {
			version = p.Version()
		} else if p.Version() != version {
			return Schema{}, fmt.Errorf("profiles have different Version values (%q, %q)", version, p.Version())
		}
		keyPaths("manifest:", p.Manifest, keys)
		for _, pg := range p.Pages {
			keyPaths("page:", pg.Manifest, keys)
		}
		for rel := range p.Files() {
			patterns[pattern(rel)] = true
		}
	}
	return Schema{Version: version, KeyPaths: sorted(keys), FilePatterns: sorted(patterns)}, nil
}

// Digest is ADR 0015's fingerprint: sha256 of the JCS form of the schema.
func (s Schema) Digest() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	v := jsontext.Value(b)
	if err := v.Canonicalize(); err != nil {
		return "", err
	}
	sum := sha256.Sum256(v)
	return hex.EncodeToString(sum[:]), nil
}

func keyPaths(prefix string, v *jsondoc.Value, out map[string]bool) {
	var walk func(path string, v *jsondoc.Value, parent string)
	walk = func(path string, v *jsondoc.Value, parent string) {
		switch v.Kind() {
		case jsondoc.Object:
			for _, m := range v.Members() {
				name := m.Name
				if wildcardMembers[parent] {
					name = "*"
				}
				p := name
				if path != "" {
					p = path + "." + name
				}
				out[prefix+p] = true
				if !opaqueMembers[m.Name] {
					walk(p, m.Value, m.Name)
				}
			}
		case jsondoc.Array:
			for _, it := range v.Items() {
				walk(path+"[]", it, parent)
			}
		}
	}
	walk("", v, "")
}

func pattern(rel string) string {
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) == 2 && parts[0] == "Images":
		return "Images/*"
	case len(parts) == 3 && parts[0] == "Profiles":
		return "Profiles/*/" + parts[2]
	case len(parts) == 4 && parts[0] == "Profiles":
		return "Profiles/*/Images/*"
	}
	return rel
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
