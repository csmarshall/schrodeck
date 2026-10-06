// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package decks turns the app's device records and profiles into this
// host's deck list (ADR 0003): which decks exist, their geometry, and whether
// each can be a destination. It is platform-neutral; connectors call it.
package decks

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/ports"
)

// RecordKey is the member every device record carries with its key in the
// app's prefs Devices dictionary (contract A, AppPrefs.DeviceRecords).
const RecordKey = "_key"

// RecordRaw marks a Devices entry that is not a device record (observed: one
// string-valued entry); its value is kept under this member for observation.
const RecordRaw = "_raw"

// TypeInfo is one row of Elgato's DeviceType table (R8, documented): the
// device's name, its key count (action slots, excluding dials) and its dial
// count. Variable marks a type whose key count the user or the host chooses
// (Mobile, Voyager, the virtual deck). R8 does not document how the keys are
// arranged, so Columns and Rows stay zero until an observation supplies them
// (docs/references.md), and Validate checks that Columns × Rows equals the
// documented Keys: the grid split is observed, its product is documented.
type TypeInfo struct {
	Name          string
	Keys, Dials   int
	Variable      bool
	Columns, Rows int
}

// DeviceTypes is Elgato's DeviceType enumeration with the documented key and
// dial counts (R8). Only the Columns/Rows split is ever added here, and only
// with an evidence row.
var DeviceTypes = map[int]TypeInfo{
	0:  {Name: "Stream Deck", Keys: 15},
	1:  {Name: "Stream Deck Mini", Keys: 6},
	2:  {Name: "Stream Deck XL", Keys: 32},
	3:  {Name: "Stream Deck Mobile", Variable: true},
	4:  {Name: "Corsair GKeys", Keys: 6},
	5:  {Name: "Stream Deck Pedal", Keys: 3},
	6:  {Name: "Corsair Voyager", Variable: true},
	7:  {Name: "Stream Deck +", Keys: 8, Dials: 4},
	8:  {Name: "SCUF Controller", Keys: 5},
	9:  {Name: "Stream Deck Neo", Keys: 8},
	10: {Name: "Stream Deck Studio", Keys: 32, Dials: 2},
	11: {Name: "Virtual Stream Deck", Variable: true},
	12: {Name: "Galleon 100 SD", Keys: 12, Dials: 2},
	13: {Name: "Stream Deck + XL", Keys: 36, Dials: 6},
}

// ProductTypes maps a physical deck's USB (vendor, product), read from its
// device key, to its DeviceType. Nothing on disk records the DeviceType, so
// each row is an observation (M1 Task 12) with an evidence row; an unmapped
// product has no geometry, so its deck cannot be a destination (fail closed,
// ADRs 0003 and 0030). Virtual decks are in scope (ADR 0003) but have a
// user-chosen grid whose stored location is unknown (U8); until U8 is settled
// they have no geometry either.
var ProductTypes = map[[2]int]int{}

// KnownGeometry derives the (vendor, product) → geometry table that
// Enumerate uses from ProductTypes and DeviceTypes. Products whose type has
// no observed grid are left out.
func KnownGeometry() map[[2]int]ports.Geometry {
	out := map[[2]int]ports.Geometry{}
	for product, typ := range ProductTypes {
		ti, ok := DeviceTypes[typ]
		if !ok || ti.Columns <= 0 || ti.Rows <= 0 {
			continue
		}
		out[product] = ports.Geometry{Columns: ti.Columns, Rows: ti.Rows, Dials: ti.Dials}
	}
	return out
}

// Validate checks the two tables against each other and against R8: every
// mapped product names a known DeviceType that has a grid, and every grid
// multiplies to the documented key count of a fixed-size type.
func Validate(types map[int]TypeInfo, products map[[2]int]int) error {
	for id, ti := range types {
		if ti.Columns == 0 && ti.Rows == 0 {
			continue
		}
		if ti.Columns <= 0 || ti.Rows <= 0 {
			return fmt.Errorf("DeviceType %d (%s): grid %d×%d is incomplete", id, ti.Name, ti.Columns, ti.Rows)
		}
		if !ti.Variable && ti.Columns*ti.Rows != ti.Keys {
			return fmt.Errorf("DeviceType %d (%s): grid %d×%d does not match the documented %d keys", id, ti.Name, ti.Columns, ti.Rows, ti.Keys)
		}
	}
	for product, typ := range products {
		ti, ok := types[typ]
		if !ok {
			return fmt.Errorf("product %v: DeviceType %d is not in R8's table", product, typ)
		}
		if ti.Columns <= 0 || ti.Rows <= 0 {
			return fmt.Errorf("product %v: DeviceType %d (%s) lacks columns/rows", product, typ, ti.Name)
		}
	}
	return nil
}

var physicalKey = regexp.MustCompile(`^@\((\d+)\)\[(\d+)/(\d+)/([^\]]*)\]$`)

// ParseKey splits a device key. Virtual decks are "@(0)[]" (R12).
func ParseKey(key string) (virtual bool, vendor, product int, serial string, ok bool) {
	if key == "@(0)[]" {
		return true, 0, 0, "", true
	}
	m := physicalKey.FindStringSubmatch(key)
	if m == nil {
		return false, 0, 0, "", false
	}
	vendor, _ = strconv.Atoi(m[2])
	product, _ = strconv.Atoi(m[3])
	return false, vendor, product, m[4], true
}

// Enumerate builds the deck list from device records and loaded profiles.
// Records without a parseable key or marked RecordRaw are skipped.
func Enumerate(records []map[string]any, profiles []*profile.Profile, known map[[2]int]ports.Geometry) []ports.Deck {
	var out []ports.Deck
	for _, rec := range records {
		if _, raw := rec[RecordRaw]; raw {
			continue
		}
		key, _ := rec[RecordKey].(string)
		virtual, vendor, product, serial, ok := ParseKey(key)
		if !ok {
			continue
		}
		d := ports.Deck{AppDeviceID: key, Virtual: virtual}
		if !virtual {
			d.Geometry = known[[2]int{vendor, product}]
		}
		if serial != "" {
			sum := sha256.Sum256([]byte(serial))
			d.SerialHash = hex.EncodeToString(sum[:])[:12]
		}
		for _, p := range profiles {
			if p.DeviceUUID() == key {
				d.ManifestDeviceID = key
				d.Model = p.DeviceModel()
				break
			}
		}
		out = append(out, d)
	}
	return out
}

// Status is a deck plus what schrodeck concludes about it.
type Status struct {
	ports.Deck
	GeometryKnown bool
	// KeyUnique: only a deck whose key is unique on this host can be a
	// destination (ADR 0026, review F31).
	KeyUnique bool
}

// Destination reports whether a member copy may be installed on the deck.
func (s Status) Destination() bool { return s.GeometryKnown && s.KeyUnique }

// Annotate adds GeometryKnown and KeyUnique.
func Annotate(ds []ports.Deck) []Status {
	count := map[string]int{}
	for _, d := range ds {
		count[d.AppDeviceID]++
	}
	out := make([]Status, 0, len(ds))
	for _, d := range ds {
		out = append(out, Status{Deck: d, GeometryKnown: d.Geometry.Columns > 0 && d.Geometry.Rows > 0, KeyUnique: count[d.AppDeviceID] == 1})
	}
	return out
}

// Unmatched lists what the deck list cannot tie together: profile folders
// whose Device.UUID equals no device key, and device keys no profile is bound
// to (records marked RecordRaw are not devices and are ignored). Both are
// counted by the U9 observation (is AppDeviceID always ManifestDeviceID?);
// "equal for N of N" means both lists are empty.
func Unmatched(records []map[string]any, profiles []*profile.Profile) (profilesWithoutDeck, decksWithoutProfile []string) {
	keys := map[string]bool{}
	for _, rec := range records {
		if _, raw := rec[RecordRaw]; raw {
			continue
		}
		if key, ok := rec[RecordKey].(string); ok {
			keys[key] = true
		}
	}
	bound := map[string]bool{}
	for _, p := range profiles {
		id := p.DeviceUUID()
		bound[id] = true
		if !keys[id] {
			profilesWithoutDeck = append(profilesWithoutDeck, p.Folder)
		}
	}
	for key := range keys {
		if !bound[key] {
			decksWithoutProfile = append(decksWithoutProfile, key)
		}
	}
	sort.Strings(profilesWithoutDeck)
	sort.Strings(decksWithoutProfile)
	return profilesWithoutDeck, decksWithoutProfile
}

// Keyed presents device records as an object keyed by RecordKey, the form the
// observation reports use. Each record still carries its RecordKey member.
// Records without a string RecordKey are dropped; the connector always sets it.
func Keyed(records []map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(records))
	for _, rec := range records {
		if key, ok := rec[RecordKey].(string); ok {
			out[key] = rec
		}
	}
	return out
}
