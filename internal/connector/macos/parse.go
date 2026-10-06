// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package macos is the macOS connector (contract A). This file holds the
// pure parsing helpers; they build on every OS so Linux CI tests them too.
// The adapters that call macOS tools live in macos_darwin.go.
package macos

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/csmarshall/schrodeck/internal/decks"
)

var ioPlatformUUID = regexp.MustCompile(`"IOPlatformUUID" = "([^"]+)"`)

// ParseIOPlatformUUID extracts IOPlatformUUID from the output of
// `ioreg -rd1 -c IOPlatformExpertDevice`.
func ParseIOPlatformUUID(out string) (string, error) {
	m := ioPlatformUUID.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("ioreg output has no IOPlatformUUID")
	}
	return m[1], nil
}

// DropboxPaths returns the sync folders listed in ~/.dropbox/info.json,
// personal first.
func DropboxPaths(info []byte) []string {
	var accounts map[string]struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(info, &accounts); err != nil {
		return nil
	}
	var out []string
	for _, name := range []string{"personal", "business"} {
		if a, ok := accounts[name]; ok && a.Path != "" {
			out = append(out, a.Path)
		}
	}
	return out
}

// Plain converts plist-decoded values into JSON-friendly ones: data becomes
// "data:<length>:<first 12 hex of its sha256>", dates become RFC 3339 strings, containers are converted
// recursively. Everything else is returned as is.
func Plain(x any) any {
	switch t := x.(type) {
	case []byte:
		// Observation-only: the length and a short digest show whether the
		// blob changed, never its bytes, and stay deterministic.
		sum := sha256.Sum256(t)
		return fmt.Sprintf("data:%d:%s", len(t), hex.EncodeToString(sum[:])[:12])
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = Plain(v)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, v := range t {
			out[i] = Plain(v)
		}
		return out
	}
	return x
}

// DeviceRecords turns prefs["Devices"] into records sorted by key. Each
// record carries its key under decks.RecordKey. An entry that is not a
// dictionary is kept as {RecordKey: key, RecordRaw: value} so observations
// can see it, and the deck list skips it.
func DeviceRecords(prefs map[string]any) ([]map[string]any, error) {
	devs, ok := prefs["Devices"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the Stream Deck prefs have no Devices dictionary")
	}
	keys := make([]string, 0, len(devs))
	for k := range devs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		rec, ok := Plain(devs[k]).(map[string]any)
		if !ok {
			out = append(out, map[string]any{decks.RecordKey: k, decks.RecordRaw: Plain(devs[k])})
			continue
		}
		rec[decks.RecordKey] = k
		out = append(out, rec)
	}
	return out, nil
}

// Preferred returns Devices[id].ESDProfilesInfo.ESDProfilesPreferred (R14).
// Read-only; schrodeck never writes it (ADR 0019).
func Preferred(prefs map[string]any, id string) (string, error) {
	devs, _ := prefs["Devices"].(map[string]any)
	rec, _ := devs[id].(map[string]any)
	info, _ := rec["ESDProfilesInfo"].(map[string]any)
	s, ok := info["ESDProfilesPreferred"].(string)
	if !ok {
		return "", fmt.Errorf("no selected profile recorded for this deck")
	}
	return s, nil
}
