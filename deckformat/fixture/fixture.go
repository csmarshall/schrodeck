// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package fixture builds synthetic Stream Deck profiles for tests. Nothing
// here comes from a real machine: ids are made up, device ids carry the
// <deck> placeholder instead of a serial, and images are generated. The
// shapes (compact JSON with sorted keys, upper-case folder names, lower-case
// references, Images/ next to each page manifest) follow what was observed
// on a real Mac (docs/streamdeck-config-model.md, contract C).
package fixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing/fstest"
)

// Device is the placeholder device id every fixture is bound to: the shape
// of a real one (vendor 4057, product 143) with <deck> where the serial goes.
const Device = "@(1)[4057/143/<deck>]"

// Model is the Device.Model value used by fixtures.
const Model = "20GAT9902"

// Button is one key.
type Button struct {
	Slot      string // "col,row"
	ActionID  string
	Plugin    string // action UUID, e.g. com.elgato.streamdeck.system.open
	Settings  string // raw JSON object
	Title     string
	State     int
	Image     string // file name under the page's Images/, "" for none
	ImageSeed byte   // content of the generated image
	// MissingImage references Image without writing the file.
	MissingImage bool
}

// Page is one page.
type Page struct {
	ID      string // lower-case UUID used in references; its folder is upper-case
	Buttons []Button
	Encoder bool            // controller Type "Encoder" instead of "Keypad"
	Orphans map[string]byte // image files no button references: name -> seed
}

// Profile is one synthetic profile.
type Profile struct {
	ID            string // lower-case UUID; folder is upper-case + ".sdProfile"
	Name          string
	Model         string
	Device        string
	Pages         []Page // Pages.Pages order
	Default       Page   // the empty page Pages.Default points to (U2)
	Current       string // Pages.Current
	AppIdentifier *string
	// Extra files relative to the profile folder, e.g. to trip the allow-list.
	Extra map[string][]byte
}

// Folder is the profile's folder name.
func (p Profile) Folder() string { return strings.ToUpper(p.ID) + ".sdProfile" }

// XL returns the base fixture: two pages of Open and Hotkey buttons with
// images, plus the empty default page.
func XL() Profile {
	return Profile{
		ID:     "aaaaaaaa-0000-4000-8000-000000000001",
		Name:   "Fixture XL",
		Model:  Model,
		Device: Device,
		Pages: []Page{
			{ID: "aaaaaaaa-0000-4000-8000-0000000000a1", Buttons: []Button{
				{Slot: "0,0", ActionID: "11111111-0000-4000-8000-000000000001", Plugin: "com.elgato.streamdeck.system.open",
					Settings: `{"openInBrowser":true,"path":"/Users/<user>/bin/demo.sh"}`, Title: "Demo", Image: "IMG00000000000000000000000001.png", ImageSeed: 1},
				{Slot: "1,0", ActionID: "11111111-0000-4000-8000-000000000002", Plugin: "com.elgato.streamdeck.system.hotkey",
					Settings: `{"Coalesce":true,"Hotkeys":[{"KeyCmd":true,"NativeCode":0}]}`, Title: "Copy", Image: "IMG00000000000000000000000002.png", ImageSeed: 2},
			}},
			{ID: "aaaaaaaa-0000-4000-8000-0000000000a2", Buttons: []Button{
				{Slot: "7,3", ActionID: "11111111-0000-4000-8000-000000000003", Plugin: "com.elgato.streamdeck.page.next",
					Settings: `{}`, Title: "", Image: "IMG00000000000000000000000003.png", ImageSeed: 3},
			}},
		},
		Default: Page{ID: "aaaaaaaa-0000-4000-8000-0000000000d0"},
		Current: "aaaaaaaa-0000-4000-8000-0000000000a1",
	}
}

// newID derives a fresh lower-case UUID-shaped id from a salt and an old id,
// the way the app gives a copy new ids.
func newID(salt, old string) string {
	sum := sha256.Sum256([]byte(salt + ":" + old))
	h := hex.EncodeToString(sum[:16])
	return h[0:8] + "-" + h[8:12] + "-4" + h[13:16] + "-8" + h[17:20] + "-" + h[20:32]
}

// CopyOf returns the same content with every instance id changed: profile id,
// page ids, ActionIDs and image file names, as the app does when it copies a
// page or profile (R20, contract C P9 and P11).
func CopyOf(p Profile, salt string) Profile {
	c := p
	c.ID = newID(salt, p.ID)
	ren := map[string]string{}
	page := func(pg Page) Page {
		out := pg
		out.ID = newID(salt, pg.ID)
		ren[pg.ID] = out.ID
		out.Buttons = nil
		for _, b := range pg.Buttons {
			nb := b
			nb.ActionID = newID(salt, b.ActionID)
			if b.Image != "" {
				nb.Image = "IMG" + strings.ReplaceAll(strings.ToUpper(newID(salt, b.Image)), "-", "")[:26] + ".png"
			}
			out.Buttons = append(out.Buttons, nb)
		}
		out.Orphans = nil
		for name, seed := range pg.Orphans {
			if out.Orphans == nil {
				out.Orphans = map[string]byte{}
			}
			out.Orphans["IMG"+strings.ToUpper(newID(salt, name))[:8]+".png"] = seed
		}
		return out
	}
	c.Pages = nil
	for _, pg := range p.Pages {
		c.Pages = append(c.Pages, page(pg))
	}
	c.Default = page(p.Default)
	if id, ok := ren[p.Current]; ok {
		c.Current = id
	}
	return c
}

// PNG returns a small, valid, deterministic PNG whose pixels depend on seed,
// so different seeds give different bytes.
func PNG(seed byte) []byte {
	sum := sha256.Sum256([]byte{seed})
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < 16; i++ {
		img.Set(i%4, i/4, color.RGBA{sum[(i*3)%32], sum[(i*3+1)%32], sum[(i*3+2)%32], 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// marshal encodes like the app: compact, keys sorted, no HTML escaping, no
// trailing newline.
func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func pageManifest(pg Page) []byte {
	actions := map[string]any{}
	for _, b := range pg.Buttons {
		state := map[string]any{"Title": b.Title}
		if b.Image != "" {
			state["Image"] = "Images/" + b.Image
		}
		actions[b.Slot] = map[string]any{
			"ActionID":    b.ActionID,
			"LinkedTitle": true,
			"Name":        path.Base(strings.ReplaceAll(b.Plugin, ".", "/")),
			"Plugin":      map[string]any{"Name": "Fixture", "UUID": b.Plugin, "Version": "1.0"},
			"Resources":   nil,
			"Settings":    json.RawMessage(b.Settings),
			"State":       b.State,
			"States":      []any{state},
			"UUID":        b.Plugin,
		}
	}
	typ := "Keypad"
	if pg.Encoder {
		typ = "Encoder"
	}
	return marshal(map[string]any{
		"Controllers": []any{map[string]any{"Actions": actions, "Type": typ}},
		"Icon":        "",
		"Name":        "",
	})
}

// FS returns the profile as a file system rooted like ProfilesV3.
func (p Profile) FS() fstest.MapFS {
	root := p.Folder()
	fsys := fstest.MapFS{}
	ids := []any{}
	for _, pg := range p.Pages {
		ids = append(ids, pg.ID)
	}
	top := map[string]any{
		"Device":  map[string]any{"Model": p.Model, "UUID": p.Device},
		"Name":    p.Name,
		"Pages":   map[string]any{"Current": p.Current, "Default": p.Default.ID, "Pages": ids},
		"Version": "3.0",
	}
	if p.AppIdentifier != nil {
		top["AppIdentifier"] = *p.AppIdentifier
	}
	fsys[root+"/manifest.json"] = &fstest.MapFile{Data: marshal(top)}
	fsys[root+"/Images"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
	for _, pg := range append(append([]Page{}, p.Pages...), p.Default) {
		dir := root + "/Profiles/" + strings.ToUpper(pg.ID)
		fsys[dir+"/manifest.json"] = &fstest.MapFile{Data: pageManifest(pg)}
		fsys[dir+"/Images"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
		for _, b := range pg.Buttons {
			if b.Image != "" && !b.MissingImage {
				fsys[dir+"/Images/"+b.Image] = &fstest.MapFile{Data: PNG(b.ImageSeed)}
			}
		}
		for name, seed := range pg.Orphans {
			fsys[dir+"/Images/"+name] = &fstest.MapFile{Data: PNG(seed)}
		}
	}
	for rel, data := range p.Extra {
		fsys[root+"/"+rel] = &fstest.MapFile{Data: data}
	}
	return fsys
}

// Merge combines file systems (later ones win on a clash).
func Merge(fss ...fstest.MapFS) fstest.MapFS {
	out := fstest.MapFS{}
	for _, f := range fss {
		for k, v := range f {
			out[k] = v
		}
	}
	return out
}

// WriteTo writes every regular file of fsys below dir.
func WriteTo(dir string, fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// PageFolder returns the on-disk folder name of the page with id.
func PageFolder(id string) string { return strings.ToUpper(id) }
