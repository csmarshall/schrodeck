// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package jsondoc is an ordered JSON tree that keeps every scalar exactly as
// it was written (number literals, string escapes) and every unknown member.
// Parse refuses any input it could not re-encode byte for byte, so code that
// reads a Stream Deck manifest and writes it back can never silently change
// data it does not understand (ADR 0031: unknown fields are preserved).
package jsondoc

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
)

// Kind is a JSON value's type.
type Kind byte

// Value kinds.
const (
	Null Kind = iota
	Bool
	Number
	String
	Object
	Array
)

// Member is one name/value pair of an object, in document order.
type Member struct {
	Name  string
	Value *Value
}

// Value is one JSON value. Scalars keep their literal bytes.
type Value struct {
	kind    Kind
	raw     []byte
	members []Member
	items   []*Value
}

// ErrNotRoundTrip means the input is valid JSON but re-encoding the parsed
// tree would not reproduce it byte for byte (for example it is indented, has
// a trailing newline, or escapes an object member name).
var ErrNotRoundTrip = errors.New("jsondoc: input does not re-encode byte-identically")

// Parse reads one JSON value. Duplicate member names and invalid UTF-8 are
// errors.
func Parse(data []byte) (*Value, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	v, err := readValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("jsondoc: data after the top-level value")
	}
	if !bytes.Equal(v.Encode(), data) {
		return nil, ErrNotRoundTrip
	}
	return v, nil
}

func readValue(dec *jsontext.Decoder) (*Value, error) {
	switch dec.PeekKind() {
	case '{':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		v := &Value{kind: Object}
		for dec.PeekKind() != '}' {
			tok, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			name := tok.String() // a token is only valid until the next read
			child, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			v.members = append(v.members, Member{Name: name, Value: child})
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return v, nil
	case '[':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		v := &Value{kind: Array}
		for dec.PeekKind() != ']' {
			child, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			v.items = append(v.items, child)
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return v, nil
	default:
		raw, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		v := &Value{raw: bytes.Clone(raw)}
		switch raw.Kind() {
		case 'n':
			v.kind = Null
		case 't', 'f':
			v.kind = Bool
		case '"':
			v.kind = String
		default:
			v.kind = Number
		}
		return v, nil
	}
}

// Encode returns compact JSON: members in document order, scalars as read.
func (v *Value) Encode() []byte { return v.appendTo(nil) }

func (v *Value) appendTo(b []byte) []byte {
	switch v.kind {
	case Object:
		b = append(b, '{')
		for i, m := range v.members {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendQuoted(b, m.Name)
			b = append(b, ':')
			b = m.Value.appendTo(b)
		}
		return append(b, '}')
	case Array:
		b = append(b, '[')
		for i, it := range v.items {
			if i > 0 {
				b = append(b, ',')
			}
			b = it.appendTo(b)
		}
		return append(b, ']')
	default:
		return append(b, v.raw...)
	}
}

// appendQuoted quotes s. Names and strings in a tree come from Parse (valid
// UTF-8) or NewString, so AppendQuote cannot fail here.
func appendQuoted(b []byte, s string) []byte {
	out, err := jsontext.AppendQuote(b, s)
	if err != nil {
		panic(fmt.Sprintf("jsondoc: invalid UTF-8 in %q", s))
	}
	return out
}

// Canonical returns the RFC 8785 (JCS) form of the value.
func (v *Value) Canonical() ([]byte, error) {
	c := jsontext.Value(v.Encode())
	if err := c.Canonicalize(); err != nil {
		return nil, err
	}
	return c, nil
}

// Kind returns the value's type.
func (v *Value) Kind() Kind { return v.kind }

// Raw returns a scalar's literal bytes (nil for objects and arrays).
func (v *Value) Raw() []byte { return v.raw }

// Str returns a string value's text.
func (v *Value) Str() (string, bool) {
	if v == nil || v.kind != String {
		return "", false
	}
	out, err := jsontext.AppendUnquote(nil, v.raw)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// Members returns an object's members in order. The slice is shared with the
// value: use RenameMember, Set and Delete to change it.
func (v *Value) Members() []Member {
	if v == nil {
		return nil
	}
	return v.members
}

// Items returns an array's elements in order (shared with the value).
func (v *Value) Items() []*Value {
	if v == nil {
		return nil
	}
	return v.items
}

// Get returns the named member of an object, or nil.
func (v *Value) Get(name string) *Value {
	if v == nil || v.kind != Object {
		return nil
	}
	for _, m := range v.members {
		if m.Name == name {
			return m.Value
		}
	}
	return nil
}

// Lookup follows a path of member names from v; nil if any step is missing.
func (v *Value) Lookup(path ...string) *Value {
	cur := v
	for _, name := range path {
		cur = cur.Get(name)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// Set replaces the named member's value, or appends the member.
func (v *Value) Set(name string, val *Value) {
	for i, m := range v.members {
		if m.Name == name {
			v.members[i].Value = val
			return
		}
	}
	v.members = append(v.members, Member{Name: name, Value: val})
}

// Delete removes the named member and reports whether it existed.
func (v *Value) Delete(name string) bool {
	for i, m := range v.members {
		if m.Name == name {
			v.members = append(v.members[:i], v.members[i+1:]...)
			return true
		}
	}
	return false
}

// RenameMember renames the i-th member.
func (v *Value) RenameMember(i int, name string) { v.members[i].Name = name }

// SetString turns v into a string value holding s.
func (v *Value) SetString(s string) {
	*v = Value{kind: String, raw: appendQuoted(nil, s)}
}

// NewString returns a string value.
func NewString(s string) *Value { return &Value{kind: String, raw: appendQuoted(nil, s)} }

// Clone returns a deep copy.
func (v *Value) Clone() *Value {
	if v == nil {
		return nil
	}
	c := &Value{kind: v.kind, raw: bytes.Clone(v.raw)}
	for _, m := range v.members {
		c.members = append(c.members, Member{Name: m.Name, Value: m.Value.Clone()})
	}
	for _, it := range v.items {
		c.items = append(c.items, it.Clone())
	}
	return c
}

// Walk calls fn for v and every value below it, depth first, with the path
// of member names and array indices ("[3]") leading to it.
func (v *Value) Walk(fn func(path []string, v *Value)) { v.walk(nil, fn) }

func (v *Value) walk(path []string, fn func([]string, *Value)) {
	fn(path, v)
	for _, m := range v.members {
		m.Value.walk(append(path[:len(path):len(path)], m.Name), fn)
	}
	for i, it := range v.items {
		it.walk(append(path[:len(path):len(path)], "["+strconv.Itoa(i)+"]"), fn)
	}
}

// FromAny converts decoded Go data (as produced by a plist or JSON decoder)
// into a Value. Map keys are sorted. Supported: nil, bool, string, integer
// and float types, []any, map[string]any.
func FromAny(x any) (*Value, error) {
	switch t := x.(type) {
	case nil:
		return &Value{kind: Null, raw: []byte("null")}, nil
	case bool:
		return &Value{kind: Bool, raw: []byte(strconv.FormatBool(t))}, nil
	case string:
		return NewString(t), nil
	case int:
		return &Value{kind: Number, raw: []byte(strconv.FormatInt(int64(t), 10))}, nil
	case int64:
		return &Value{kind: Number, raw: []byte(strconv.FormatInt(t, 10))}, nil
	case uint64:
		return &Value{kind: Number, raw: []byte(strconv.FormatUint(t, 10))}, nil
	case float64:
		return &Value{kind: Number, raw: jsontext.AppendFloat(nil, t, 64)}, nil
	case []any:
		v := &Value{kind: Array}
		for _, e := range t {
			c, err := FromAny(e)
			if err != nil {
				return nil, err
			}
			v.items = append(v.items, c)
		}
		return v, nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		v := &Value{kind: Object}
		for _, k := range keys {
			c, err := FromAny(t[k])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			v.members = append(v.members, Member{Name: k, Value: c})
		}
		return v, nil
	}
	return nil, fmt.Errorf("jsondoc: unsupported type %T", x)
}
