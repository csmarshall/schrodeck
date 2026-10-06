// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package redact scrubs personal identifiers from profile data, so that
// observations and fixtures from real machines can be shared (ADR 0031).
// Redaction errs toward removing too much. It is a first pass: anything that
// will be published still gets a human review and the repository leak scan.
package redact

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/pathguard"
	"github.com/csmarshall/schrodeck/deckformat/profile"
)

// Placeholders written in place of what was removed.
const (
	User     = "<user>"
	Host     = "<host>"
	Deck     = "<deck>"
	Redacted = "<redacted>"
)

var (
	// A Stream Deck device id embeds the USB serial: @(1)[vendor/product/serial].
	// Vendor and product are public model ids and are kept.
	deviceID     = regexp.MustCompile(`@\((\d+)\)\[(\d+)/(\d+)/[^\]\s"]+\]`)
	deviceSerial = regexp.MustCompile(`@\(\d+\)\[\d+/\d+/([^\]\s"]+)\]`)
	// Home directories: macOS, Linux, Windows (with either separator).
	homeDir = regexp.MustCompile(`(/Users/|/home/|[A-Za-z]:\\Users\\|[A-Za-z]:/Users/)[^/\\\s"'<>]+`)
	// Member names whose values are secrets whatever they contain.
	secretName = regexp.MustCompile(`(?i)(token|secret|passw|api[_-]?key|auth|cookie|session)`)
)

// Options lists identifiers known to the caller (from the host connector).
// Serials are the serial parts of every device id the caller has seen (prefs
// keys and manifests, see SerialsFrom): they are removed wherever they appear,
// not only inside a device id.
type Options struct {
	UserNames []string
	HostNames []string
	Serials   []string
}

// Redactor applies the rules.
type Redactor struct {
	literals []literal
	serials  []*regexp.Regexp

	// Each distinct serial gets its own placeholder, numbered in order of first
	// appearance: <deck>, <deck2>, <deck3>. Two decks therefore never collapse
	// into one member name, and a serial seen bare maps like the id carrying it.
	mu    sync.Mutex
	decks map[string]string
}

// deckFor returns the placeholder for a serial, assigning the next one on first
// sight. Serials compare case-insensitively.
func (r *Redactor) deckFor(serial string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(serial)
	if ph, ok := r.decks[key]; ok {
		return ph
	}
	ph := Deck
	if n := len(r.decks) + 1; n > 1 {
		ph = Deck[:len(Deck)-1] + strconv.Itoa(n) + ">"
	}
	r.decks[key] = ph
	return ph
}

type literal struct {
	re          *regexp.Regexp
	placeholder string
}

// New builds a redactor. Names shorter than 3 characters are refused rather
// than redacted everywhere they occur as a substring.
func New(o Options) (*Redactor, error) {
	r := &Redactor{decks: map[string]string{}}
	add := func(names []string, placeholder string) error {
		for _, n := range names {
			if n == "" {
				continue
			}
			if len(n) < 3 {
				return fmt.Errorf("redact: %q is too short to redact safely", n)
			}
			r.literals = append(r.literals, literal{regexp.MustCompile(`(?i)` + regexp.QuoteMeta(n)), placeholder})
		}
		return nil
	}
	if err := add(o.HostNames, Host); err != nil {
		return nil, err
	}
	if err := add(o.UserNames, User); err != nil {
		return nil, err
	}
	for _, n := range o.Serials {
		if n == "" {
			continue
		}
		if len(n) < 3 {
			return nil, fmt.Errorf("redact: %q is too short to redact safely", n)
		}
		r.serials = append(r.serials, regexp.MustCompile(`(?i)`+regexp.QuoteMeta(n)))
	}
	// Longest first, so "alice-mbp" is replaced before "alice".
	sort.SliceStable(r.literals, func(i, j int) bool {
		return len(r.literals[i].re.String()) > len(r.literals[j].re.String())
	})
	return r, nil
}

// SerialsFrom returns the serial part of every device id among ids (an
// "@(n)[vendor/product/serial]" string); other strings and serial-less ids
// such as a virtual deck's "@(0)[]" contribute nothing.
func SerialsFrom(ids ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		for _, m := range deviceSerial.FindAllStringSubmatch(id, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	sort.Strings(out)
	return out
}

// String redacts one string.
func (r *Redactor) String(s string) string {
	s = deviceID.ReplaceAllStringFunc(s, func(id string) string {
		m := deviceID.FindStringSubmatch(id)
		return "@(" + m[1] + ")[" + m[2] + "/" + m[3] + "/" + r.deckFor(deviceSerial.FindStringSubmatch(id)[1]) + "]"
	})
	s = homeDir.ReplaceAllString(s, "${1}"+User)
	for _, l := range r.literals {
		s = l.re.ReplaceAllString(s, l.placeholder)
	}
	for _, re := range r.serials {
		s = re.ReplaceAllStringFunc(s, r.deckFor)
	}
	return s
}

// Value returns a redacted deep copy: every string and member name is
// redacted, and the values of secret-looking members become "<redacted>".
func (r *Redactor) Value(v *jsondoc.Value) *jsondoc.Value {
	c := v.Clone()
	r.redactInPlace(c)
	return c
}

func (r *Redactor) redactInPlace(v *jsondoc.Value) {
	switch v.Kind() {
	case jsondoc.String:
		s, _ := v.Str()
		if red := r.String(s); red != s {
			v.SetString(red)
		}
	case jsondoc.Object:
		for i, m := range v.Members() {
			if red := r.String(m.Name); red != m.Name {
				v.RenameMember(i, red)
			}
			if secretName.MatchString(m.Name) && m.Value.Kind() != jsondoc.Object && m.Value.Kind() != jsondoc.Array {
				m.Value.SetString(Redacted)
				continue
			}
			r.redactInPlace(m.Value)
		}
	case jsondoc.Array:
		for _, it := range v.Items() {
			r.redactInPlace(it)
		}
	}
}

// Strings lists every distinct string value and member name in a profile's
// manifests, for the human review that must precede publishing a fixture.
func Strings(p *profile.Profile) []string {
	set := map[string]bool{}
	collect := func(v *jsondoc.Value) {
		v.Walk(func(path []string, x *jsondoc.Value) {
			if len(path) > 0 && !strings.HasPrefix(path[len(path)-1], "[") {
				set[path[len(path)-1]] = true
			}
			if s, ok := x.Str(); ok && s != "" {
				set[s] = true
			}
		})
	}
	collect(p.Manifest)
	for _, pg := range p.Pages {
		collect(pg.Manifest)
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// SyntheticPNG replaces an image: a 4×4 PNG whose pixels are derived from the
// original's sha256, so distinct images stay distinct (hash tests keep their
// meaning) while the picture itself is gone.
func SyntheticPNG(original []byte) []byte {
	sum := sha256.Sum256(original)
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < 16; i++ {
		img.Set(i%4, i/4, color.RGBA{sum[(i*2)%32], sum[(i*2+1)%32], sum[(i*5)%32], 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ErrOutputExists means the export target already has content.
var ErrOutputExists = errors.New("redact: output directory is not empty")

// ExportFixture writes a redacted copy of p to outDir/<folder>: manifests
// redacted, images replaced by SyntheticPNG. folder must be a single path
// element, outDir must be empty or absent, and the final target must not lie
// inside sourceRoot (the folder p was read from), so an export can never write
// into the app's own data (pathguard resolves symlinks and "../").
func ExportFixture(p *profile.Profile, r *Redactor, sourceRoot, outDir, folder string) error {
	if err := pathguard.SingleName(folder); err != nil {
		return err
	}
	// Resolve outDir the way the kernel will (never filepath.Abs, which cleans
	// "link/../x" into a different place), check the final target, and write to
	// exactly that resolved path so the check and the write cannot disagree.
	resolvedOut, err := pathguard.Resolve(outDir)
	if err != nil {
		return err
	}
	finalTarget, err := pathguard.Resolve(filepath.Join(resolvedOut, folder))
	if err != nil {
		return err
	}
	if err := pathguard.RefuseInside(finalTarget, sourceRoot); err != nil {
		return err
	}
	if entries, err := os.ReadDir(resolvedOut); err == nil && len(entries) > 0 {
		return ErrOutputExists
	}
	// Redact and re-parse every manifest before anything is written: a manifest
	// that no longer parses (for example two member names redacted into one)
	// must fail the export, not land on disk. Paths are sorted so that numbered
	// placeholders are assigned in a deterministic order.
	files := p.Files()
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	redacted := make(map[string][]byte, len(files))
	for _, rel := range rels {
		data := files[rel]
		if strings.HasSuffix(rel, "manifest.json") {
			doc, err := jsondoc.Parse(data)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			data = r.Value(doc).Encode()
			if _, err := jsondoc.Parse(data); err != nil {
				return fmt.Errorf("redact: %s does not parse after redaction, nothing written: %w", rel, err)
			}
		} else {
			data = SyntheticPNG(data)
		}
		redacted[rel] = data
	}
	for _, rel := range rels {
		target := filepath.Join(finalTarget, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, redacted[rel], 0o644); err != nil {
			return err
		}
	}
	return nil
}
