// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package redact scrubs personal identifiers from profile data, so that
// observations and fixtures from real machines can be shared (ADR 0031).
// Redaction errs toward removing too much. It is a first pass: anything that
// will be published still gets a human review and the repository leak scan.
//
// What it cannot catch: a serial split or reformatted across characters (for
// example "AB12-CD34EF" for "AB12CD34EF"), a name spelled phonetically or
// abbreviated, and anything in image pixels (images are replaced wholesale, not
// scrubbed). Numbered placeholders (<deck2>) are fixed by Options.Serials (sorted,
// case-folded), so the same set gives the same mapping; a serial outside it, and
// pseudonymized UUIDs, are numbered per Redactor in the order first seen.
package redact

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

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
	// Home directories: macOS, Linux, Windows (with either separator), and the
	// same path with its separators percent-encoded.
	homeDir    = regexp.MustCompile(`(/Users/|/home/|[A-Za-z]:\\Users\\|[A-Za-z]:/Users/)[^/\\\s"'<>]+`)
	homeDirEnc = regexp.MustCompile(`((?i:%2F)(?:Users|home)(?i:%2F))[^%/\\\s"'<>&]+`)
	// Member names whose values are secrets whatever they contain. "pin" and
	// "pass" are matched as whole words of the name (userPIN, pin_code), not as
	// substrings, so "mapping" and "bypass" are left alone.
	secretSubstring = regexp.MustCompile(`(?i)(token|secret|passw|api[_-]?key|private[_-]?key|access[_-]?key|auth|cookie|session|credential|bearer)`)
	// A query parameter: ?key=value, &key=value or ;key=value.
	queryParam = regexp.MustCompile(`([?&;#])([^=&#\s"'<>]+)=([^&#\s"'<>]*)`)
	uuidRe     = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	// An Authorization-style bearer credential, whatever member holds it.
	bearerValue = regexp.MustCompile(`(?i)\bBearer\s+\S+`)
	// A Basic credential, whatever member holds it. Stops at a quote or a
	// delimiter, so a credential inside a quoted header keeps its closing quote.
	basicValue = regexp.MustCompile(`(?i)\bBasic\s+[^\s"',;]+`)
	// A secret-looking key, optional closing quote, ":" or "=", then its value:
	// a quoted string, or a run of characters that are not space or a delimiter.
	// Group 2 is the key word, group 5 the value.
	keyValue = regexp.MustCompile(`([A-Za-z0-9_-]+)(["']?)([ \t]*[:=][ \t]*)("(?:[^"\\]|\\.)*"|'[^']*'|[^\s"',;&<>{}\[\]()]+)`)
	// An already-redacted serial, so redacting twice does not renumber it.
	deckPlaceholder = regexp.MustCompile(`^<deck\d*>$`)
	// Any device-key shape "@(<type>)[<body>]", well-formed or not: the app
	// may use key forms nobody has observed yet (U5, U7), and their bodies can
	// carry serials the vendor/product/serial rule does not reach.
	anyDeviceKey = regexp.MustCompile(`@\((\d+)\)\[([^\]"\n]*)\]`)
	// A body the serial rule has already redacted: vendor/product/<deckN>.
	redactedDeviceBody = regexp.MustCompile(`^\d+/\d+/<deck\d*>$`)
	// Tokens inside an odd key body: a placeholder (kept) or a run of three or
	// more letters and digits (masked).
	keyBodyToken = regexp.MustCompile(`<[a-z]+\d*>|[A-Za-z0-9]{3,}`)
	// Tokens in a member name (KeyName): a placeholder (kept) or a run of six
	// or more letters and digits (masked when it mixes both).
	keyNameToken   = regexp.MustCompile(`<[a-z]+\d*>|[A-Za-z0-9]{6,}`)
	anyPlaceholder = regexp.MustCompile(`^<[a-z]+\d*>$`)
	hasLetter      = regexp.MustCompile(`[A-Za-z]`)
	hasDigit       = regexp.MustCompile(`[0-9]`)
)

// IsSecretName reports whether a member name says its value is a secret.
// It is the one home of the rule; callers that decide by member name use it.
func IsSecretName(name string) bool {
	if secretSubstring.MatchString(name) {
		return true
	}
	for _, w := range nameWords(name) {
		switch w {
		case "pin", "pass", "passcode":
			return true
		}
	}
	return false
}

// nameWords splits a member name into lower-case words at separators and
// camelCase boundaries: "userPIN" gives user, pin; "api_key" gives api, key.
func nameWords(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	rs := []rune(name)
	for i, c := range rs {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) {
			flush()
			continue
		}
		if unicode.IsUpper(c) && len(cur) > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, c)
	}
	flush()
	return words
}

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

	// Each distinct serial gets its own placeholder: <deck>, <deck2>, <deck3>,
	// assigned to Options.Serials in sorted order by New, then to any serial first
	// seen outside that list in order of first appearance. Two decks therefore never collapse
	// into one member name, and a serial seen bare maps like the id carrying it.
	mu    sync.Mutex
	decks map[string]string
	uuids map[string]string
	// Odd device-key bodies (<key>, <key2>, …) and mixed tokens in member
	// names (<id>, <id2>, …), numbered in order of first sight, case-sensitive.
	keys map[string]string
	ids  map[string]string
}

// numbered returns the placeholder for value in m ("<name>", then
// "<name2>", …), assigning the next one on first sight.
func (r *Redactor) numbered(m map[string]string, name, value string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ph, ok := m[value]; ok {
		return ph
	}
	ph := "<" + name + ">"
	if n := len(m) + 1; n > 1 {
		ph = "<" + name + strconv.Itoa(n) + ">"
	}
	m[value] = ph
	return ph
}

// maskOddDeviceKeys masks the body of every device key that is not in
// vendor/product/serial form (those are handled by the serial rule): each run
// of three or more letters and digits becomes <key>, <key2>, …, while the
// "@(n)[", "]" and punctuation are kept so the key's shape stays visible.
func (r *Redactor) maskOddDeviceKeys(s string) string {
	return anyDeviceKey.ReplaceAllStringFunc(s, func(k string) string {
		m := anyDeviceKey.FindStringSubmatch(k)
		body := m[2]
		if body == "" || redactedDeviceBody.MatchString(body) {
			return k
		}
		body = keyBodyToken.ReplaceAllStringFunc(body, func(tok string) string {
			if anyPlaceholder.MatchString(tok) {
				return tok
			}
			return r.numbered(r.keys, "key", tok)
		})
		return "@(" + m[1] + ")[" + body + "]"
	})
}

// KeyName redacts a member name whose shape is unknown, such as a prefs
// Devices entry that is not a device record (U7): String's rules apply, and
// then every token of six or more characters that mixes letters and digits
// becomes <id>, <id2>, … (an identifier or serial, by its look). Words of
// letters only, such as "ESDProfilesPreferred", and plain numbers stay readable.
func (r *Redactor) KeyName(s string) string {
	s = r.String(s)
	return keyNameToken.ReplaceAllStringFunc(s, func(tok string) string {
		if anyPlaceholder.MatchString(tok) || !hasLetter.MatchString(tok) || !hasDigit.MatchString(tok) {
			return tok
		}
		return r.numbered(r.ids, "id", tok)
	})
}

// deckFor returns the placeholder for a serial, assigning the next one on first
// sight. Serials compare case-insensitively.
func (r *Redactor) deckFor(serial string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if deckPlaceholder.MatchString(serial) {
		return serial
	}
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

// uuidFor returns the synthetic UUID (lower-case) for a real one, numbered in
// order of first appearance and shaped like the synthetic fixtures'.
func (r *Redactor) uuidFor(real string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(real)
	if syn, ok := r.uuids[key]; ok {
		return syn
	}
	syn := fmt.Sprintf("aaaaaaaa-0000-4000-8000-%012x", len(r.uuids)+1)
	r.uuids[key] = syn
	return syn
}

// pseudonymizeUUIDs replaces every UUID in s by its synthetic one, consistently
// and case-insensitively. The synthetic id keeps the case of the original,
// except when onDisk is set (a folder name), which is always upper-case as the
// app writes them.
func (r *Redactor) pseudonymizeUUIDs(s string, onDisk bool) string {
	return uuidRe.ReplaceAllStringFunc(s, func(m string) string {
		syn := r.uuidFor(m)
		if onDisk || m != strings.ToLower(m) {
			return strings.ToUpper(syn)
		}
		return syn
	})
}

func (r *Redactor) pseudonymizeValue(v *jsondoc.Value) {
	switch v.Kind() {
	case jsondoc.String:
		s, _ := v.Str()
		if out := r.pseudonymizeUUIDs(s, false); out != s {
			v.SetString(out)
		}
	case jsondoc.Object:
		for i, m := range v.Members() {
			if out := r.pseudonymizeUUIDs(m.Name, false); out != m.Name {
				v.RenameMember(i, out)
			}
			r.pseudonymizeValue(m.Value)
		}
	case jsondoc.Array:
		for _, it := range v.Items() {
			r.pseudonymizeValue(it)
		}
	}
}

type literal struct {
	re          *regexp.Regexp
	placeholder string
}

// New builds a redactor. Names shorter than 3 characters are refused rather
// than redacted everywhere they occur as a substring.
func New(o Options) (*Redactor, error) {
	r := &Redactor{decks: map[string]string{}, uuids: map[string]string{}, keys: map[string]string{}, ids: map[string]string{}}
	add := func(names []string, placeholder string) error {
		for _, n := range names {
			if n == "" {
				continue
			}
			if len(n) < 3 {
				return fmt.Errorf("redact: %q is too short to redact safely", n)
			}
			// The name as written, and as it appears percent-encoded in a URL.
			seen := map[string]bool{}
			for _, form := range []string{n, url.PathEscape(n), url.QueryEscape(n)} {
				if !seen[form] {
					seen[form] = true
					r.literals = append(r.literals, literal{regexp.MustCompile(`(?i)` + regexp.QuoteMeta(form)), placeholder})
				}
			}
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
	// Number the listed serials up front, in case-folded sorted order, so one
	// Serials set always gives one mapping, whatever order the decks are met in.
	// A serial first seen outside the list is numbered after these.
	listed := append([]string(nil), o.Serials...)
	sort.Slice(listed, func(i, j int) bool { return strings.ToLower(listed[i]) < strings.ToLower(listed[j]) })
	for _, n := range listed {
		if n != "" {
			r.deckFor(n)
		}
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
	s = r.maskOddDeviceKeys(s)
	s = homeDir.ReplaceAllString(s, "${1}"+User)
	s = homeDirEnc.ReplaceAllString(s, "${1}"+User)
	s = bearerValue.ReplaceAllString(s, "Bearer "+Redacted)
	s = basicValue.ReplaceAllString(s, "Basic "+Redacted)
	s = queryParam.ReplaceAllStringFunc(s, func(q string) string {
		m := queryParam.FindStringSubmatch(q)
		key := m[2]
		if dec, err := url.QueryUnescape(key); err == nil {
			key = dec
		}
		if IsSecretName(key) {
			return m[1] + m[2] + "=" + Redacted
		}
		return q
	})
	s = keyValue.ReplaceAllStringFunc(s, func(kv string) string {
		m := keyValue.FindStringSubmatch(kv)
		key, quote, sep, val := m[1], m[2], m[3], m[4]
		// "Authorization: Basic <redacted>": the scheme word is not the secret,
		// the credential after it already went.
		if !IsSecretName(key) || strings.EqualFold(val, "Basic") || strings.EqualFold(val, "Bearer") {
			return kv
		}
		if q := val[0]; q == '"' || q == '\'' {
			val = string(q) + Redacted + string(q)
		} else {
			val = Redacted
		}
		return key + quote + sep + val
	})
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
		// JSON embedded in a string (plugins store settings that way) is parsed,
		// redacted as a document and put back.
		inner, unparseable := parseEmbedded(s)
		if unparseable {
			v.SetString(Redacted)
			return
		}
		if inner != nil {
			r.redactInPlace(inner)
			v.SetString(string(inner.Encode()))
			return
		}
		if red := r.String(s); red != s {
			v.SetString(red)
		}
	case jsondoc.Object:
		// A {"name": "token", "value": ...} pair: the name says what the value is.
		pairSecret := false
		for _, m := range v.Members() {
			if k := strings.ToLower(m.Name); k == "name" || k == "key" {
				if s, ok := m.Value.Str(); ok && IsSecretName(s) {
					pairSecret = true
				}
			}
		}
		for i, m := range v.Members() {
			if red := r.String(m.Name); red != m.Name {
				v.RenameMember(i, red)
			}
			if IsSecretName(m.Name) || (pairSecret && strings.EqualFold(m.Name, "value")) {
				r.blank(m.Value)
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

// maxJSONDepth is where encoding/json stops validating; jsondoc's parser has a
// limit of the same size.
const maxJSONDepth = 10000

// parseEmbedded returns the document inside s when the trimmed string (after an
// optional UTF-8 byte order mark) is valid JSON with an object or array at the
// top, in any formatting; nil otherwise. A document that is not already compact
// is re-encoded compactly, so the format of the embedded string changes when it
// is redacted. unparseable is true when the string is JSON that no parser here
// can take (nesting beyond the limit): it cannot be redacted member by member,
// so the caller replaces it whole.
func parseEmbedded(s string) (doc *jsondoc.Value, unparseable bool) {
	t := strings.TrimSpace(strings.TrimPrefix(s, "\xef\xbb\xbf"))
	if len(t) < 2 || (t[0] != '{' && t[0] != '[') {
		return nil, false
	}
	if !json.Valid([]byte(t)) {
		return nil, bracketDepth(t) > maxJSONDepth
	}
	if v, err := jsondoc.Parse([]byte(t)); err == nil {
		return v, false
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(t)); err == nil {
		if v, err := jsondoc.Parse(compact.Bytes()); err == nil {
			return v, false
		}
	}
	// Last resort (for example duplicate member names, which jsondoc refuses):
	// decode generically. Later duplicates win; numbers keep integer or float form.
	dec := json.NewDecoder(strings.NewReader(t))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, true
	}
	v, err := jsondoc.FromAny(numbersToNative(x))
	if err != nil {
		return nil, true
	}
	return v, false
}

// bracketDepth is the deepest bracket nesting of t outside string literals.
func bracketDepth(t string) int {
	depth, deepest, inString := 0, 0, false
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case inString:
			if c == '\\' {
				i++
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
		case c == '[' || c == '{':
			if depth++; depth > deepest {
				deepest = depth
			}
		case c == ']' || c == '}':
			depth--
		}
	}
	return deepest
}

func numbersToNative(x any) any {
	switch t := x.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		f, _ := t.Float64()
		return f
	case []any:
		for i := range t {
			t[i] = numbersToNative(t[i])
		}
	case map[string]any:
		for k := range t {
			t[k] = numbersToNative(t[k])
		}
	}
	return x
}

// blank replaces every scalar under v, at any depth, by Redacted. Member names
// are still redacted, since they can carry identifiers too.
func (r *Redactor) blank(v *jsondoc.Value) {
	switch v.Kind() {
	case jsondoc.Object:
		for i, m := range v.Members() {
			if red := r.String(m.Name); red != m.Name {
				v.RenameMember(i, red)
			}
			r.blank(m.Value)
		}
	case jsondoc.Array:
		for _, it := range v.Items() {
			r.blank(it)
		}
	default:
		v.SetString(Redacted)
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
// redacted, UUIDs pseudonymized (in manifests, folder names and paths), images
// replaced by SyntheticPNG. folder must be a single path element, outDir must be
// empty or absent (a directory that cannot be listed is refused, not assumed
// empty), and the final target must not lie inside any of roots (the folder p
// was read from, the app's data root), so an export can never write into the
// app's own data. No roots, or an empty root, is refused with
// pathguard.ErrNoRoots before anything is read or written. It returns the absolute path of the profile folder it wrote,
// which differs from outDir/folder when the folder name carried a UUID that was
// pseudonymized. Every file is created exclusively (O_EXCL), so an existing
// file, including a hard link to another file, is never opened for writing.
//
// The guard decides by file identity on the path the kernel will reach, and the
// files are written through an os.Root opened on that exact directory, so a
// relative name inside the export cannot step out of it. One race remains that
// no path check can close: someone with write access renaming an ANCESTOR of
// outDir between the guard and the open. The identity re-check after the open
// narrows that window; it does not remove it.
func ExportFixture(p *profile.Profile, r *Redactor, outDir, folder string, roots ...string) (string, error) {
	// RefuseInside below refuses missing roots too; checking first keeps the
	// refusal independent of how far the export gets.
	if err := pathguard.RefuseInside(outDir, roots...); errors.Is(err, pathguard.ErrNoRoots) {
		return "", err
	}
	if err := pathguard.SingleName(folder); err != nil {
		return "", err
	}
	outFolder := r.pseudonymizeUUIDs(folder, true)
	// Resolve outDir the way the kernel will (never filepath.Abs, which cleans
	// "link/../x" into a different place), check the final target, and write to
	// exactly that resolved path so the check and the write cannot disagree.
	resolvedOut, err := pathguard.Resolve(outDir)
	if err != nil {
		return "", err
	}
	finalTarget, err := pathguard.Resolve(filepath.Join(resolvedOut, outFolder))
	if err != nil {
		return "", err
	}
	if err := pathguard.RefuseInside(finalTarget, roots...); err != nil {
		return "", err
	}
	// Only "absent" is tolerated: a directory that cannot be listed (mode 0300,
	// a file in its place) might hold anything, so it is refused.
	switch entries, err := os.ReadDir(resolvedOut); {
	case err == nil && len(entries) > 0:
		return "", ErrOutputExists
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return "", err
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
				return "", fmt.Errorf("%s: %w", rel, err)
			}
			doc = r.Value(doc)
			r.pseudonymizeValue(doc)
			data = doc.Encode()
			if _, err := jsondoc.Parse(data); err != nil {
				return "", fmt.Errorf("redact: %s does not parse after redaction, nothing written: %w", rel, err)
			}
		} else {
			data = SyntheticPNG(data)
		}
		redacted[r.pseudonymizeUUIDs(rel, true)] = data
	}
	outRels := make([]string, 0, len(redacted))
	for rel := range redacted {
		outRels = append(outRels, rel)
	}
	sort.Strings(outRels)

	if err := os.MkdirAll(finalTarget, 0o755); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(finalTarget)
	if err != nil {
		return "", err
	}
	defer root.Close()
	// The directory opened must be the directory that was checked, and must
	// still not be inside the source.
	opened, err := root.Stat(".")
	if err != nil {
		return "", err
	}
	checked, err := os.Stat(finalTarget)
	if err != nil {
		return "", err
	}
	if !os.SameFile(opened, checked) {
		return "", fmt.Errorf("redact: %s changed while the export was starting", finalTarget)
	}
	if err := pathguard.RefuseInside(finalTarget, roots...); err != nil {
		return "", err
	}
	for _, rel := range outRels {
		if dir := path.Dir(rel); dir != "." {
			if err := root.MkdirAll(filepath.FromSlash(dir), 0o755); err != nil {
				return "", err
			}
		}
		if err := writeNew(root, filepath.FromSlash(rel), redacted[rel]); err != nil {
			return "", err
		}
	}
	return finalTarget, nil
}

// writeNew creates name below root exclusively and writes data to it.
func writeNew(root *os.Root, name string, data []byte) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
