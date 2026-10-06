// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jsondoc

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

// bs is a backslash. Escape sequences in test inputs are assembled from it so
// no tool in the authoring path can rewrite them into the characters they
// stand for.
const bs = "\x5c"

func TestRoundTripPreservesEverything(t *testing.T) {
	inputs := []string{
		// unknown members, float literals, escapes, non-ASCII, nesting
		`{"b":1.50,"a":"x` + bs + `/y","c":[1,{"z":true}],"d":null,"e":"Café ☕","f":"` + bs + `u00e9","g":[],"h":{},"i":-1e2,"Unknown":{"Deep":[0.10]}}`,
		// the shape of a real page manifest
		`{"Controllers":[{"Actions":{"0,0":{"ActionID":"11111111-0000-4000-8000-000000000001","Settings":{},"State":0}},"Type":"Keypad"}],"Icon":"","Name":""}`,
	}
	for _, in := range inputs {
		v, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("Parse(%s): %v", in, err)
		}
		if got := string(v.Encode()); got != in {
			t.Fatalf("Encode changed the document:\n got %s\nwant %s", got, in)
		}
	}
}

// Known-bad: a typed-struct or map[string]any parser drops or rewrites data.
// This is what Parse protects against; the test shows the hazard is real.
func TestNaiveDecodeWouldLoseData(t *testing.T) {
	in := `{"b":1.50,"Unknown":1}`
	var m map[string]any
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(m)
	if string(out) == in {
		t.Fatal("expected encoding/json to rewrite 1.50 or reorder members; the round-trip guarantee would then be untested")
	}
}

func TestParseRefusesWhatItCannotReproduce(t *testing.T) {
	cases := map[string]struct {
		in           string
		notRoundTrip bool
	}{
		"indented":            {"{\"a\": 1}", true},
		"trailing newline":    {"{\"a\":1}\n", true},
		"escaped member name": {`{"a` + bs + `u0041":1}`, true},
		"duplicate names":     {`{"a":1,"a":2}`, false},
		"invalid UTF-8":       {"{\"a\":\"\xff\"}", false},
		"truncated":           {`{"a":1`, false},
		"two values":          {`{}{}`, false},
	}
	for name, c := range cases {
		_, err := Parse([]byte(c.in))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if errors.Is(err, ErrNotRoundTrip) != c.notRoundTrip {
			t.Errorf("%s: error %v, want ErrNotRoundTrip=%v", name, err, c.notRoundTrip)
		}
	}
}

func TestAccessorsAndMutation(t *testing.T) {
	v, err := Parse([]byte(`{"Pages":{"Current":"x","Pages":["a","b"]},"Name":"N"}`))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := v.Lookup("Pages", "Current").Str(); !ok || s != "x" {
		t.Fatalf("Lookup Current = %q %v", s, ok)
	}
	if n := len(v.Lookup("Pages", "Pages").Items()); n != 2 {
		t.Fatalf("Items = %d", n)
	}
	c := v.Clone()
	c.Lookup("Pages").Delete("Current")
	c.Set("Name", NewString("M"))
	c.Set("New", NewString("y"))
	c.Lookup("Pages", "Pages").Items()[0].SetString("z")
	if got := string(c.Encode()); got != `{"Pages":{"Pages":["z","b"]},"Name":"M","New":"y"}` {
		t.Fatalf("mutated = %s", got)
	}
	if got := string(v.Encode()); got != `{"Pages":{"Current":"x","Pages":["a","b"]},"Name":"N"}` {
		t.Fatalf("Clone shares state with the original: %s", got)
	}
}

func TestWalkPaths(t *testing.T) {
	v, err := Parse([]byte(`{"a":[{"b":1}],"c":2}`))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	v.Walk(func(p []string, _ *Value) { paths = append(paths, strings.Join(p, ".")) })
	if got := strings.Join(paths, " | "); got != " | a | a.[0] | a.[0].b | c" {
		t.Fatalf("Walk paths = %q", got)
	}
}

func TestCanonicalIsJCS(t *testing.T) {
	v, err := Parse([]byte(`{"b":1.50,"a":"x` + bs + `/y"}`))
	if err != nil {
		t.Fatal(err)
	}
	c, err := v.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if string(c) != `{"a":"x/y","b":1.5}` {
		t.Fatalf("Canonical = %s", c)
	}
}

func TestFromAny(t *testing.T) {
	v, err := FromAny(map[string]any{"z": []any{int64(1), uint64(2), 1.5, true, nil}, "a": "s"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(v.Encode()); got != `{"a":"s","z":[1,2,1.5,true,null]}` {
		t.Fatalf("FromAny = %s", got)
	}
	if _, err := Parse(v.Encode()); err != nil {
		t.Fatalf("FromAny output does not round-trip: %v", err)
	}
	if _, err := FromAny(struct{}{}); err == nil {
		t.Fatal("unsupported type accepted")
	}
	// Non-finite floats must be rejected
	if _, err := FromAny(math.NaN()); err == nil {
		t.Fatal("NaN accepted")
	}
	if _, err := FromAny(math.Inf(1)); err == nil {
		t.Fatal("+Inf accepted")
	}
	if _, err := FromAny(math.Inf(-1)); err == nil {
		t.Fatal("-Inf accepted")
	}
}

func TestInvalidUTF8Sanitization(t *testing.T) {
	// NewString with invalid UTF-8 sanitizes and encodes to valid JSON
	v := NewString("a\xffb")
	encoded := v.Encode()
	if _, err := Parse(encoded); err != nil {
		t.Fatalf("NewString with invalid UTF-8 produced non-round-trippable output: %v", err)
	}
	// RenameMember with invalid UTF-8 sanitizes
	obj, _ := Parse([]byte(`{"x":1}`))
	obj.RenameMember(0, "a\xffb")
	encoded = obj.Encode()
	if _, err := Parse(encoded); err != nil {
		t.Fatalf("RenameMember with invalid UTF-8 produced non-round-trippable output: %v", err)
	}
}
