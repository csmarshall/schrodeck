// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build darwin

package macos

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"howett.net/plist"

	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/host"
	"github.com/csmarshall/schrodeck/internal/ports"
)

// Facts about the macOS Stream Deck app (contract B, macOS section).
const (
	appBundle   = "/Applications/Elgato Stream Deck.app" // M5: version in its Info.plist
	appProcess  = "Stream Deck"                          // observed process name (`Stream Deck --runinbk`)
	prefsDomain = "com.elgato.StreamDeck"                // M2
	dataRel     = "Library/Application Support/com.elgato.StreamDeck"
)

// Connector implements the read-only ports M1 needs: Paths, HostIdentity,
// AppPrefs, DeviceEnumerator and the presence half of AppControl. Quit and
// Launch arrive with M3.
type Connector struct {
	home string
}

var (
	_ ports.Paths            = (*Connector)(nil)
	_ ports.HostIdentity     = (*Connector)(nil)
	_ ports.AppPrefs         = (*Connector)(nil)
	_ ports.DeviceEnumerator = (*Connector)(nil)
	_ host.AppPresence       = (*Connector)(nil)
	_ host.DeckLoader        = (*Connector)(nil)
)

// New returns the connector for the current user.
func New() (*Connector, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Connector{home: home}, nil
}

func (c *Connector) AppDataRoot() string  { return filepath.Join(c.home, dataRel) }
func (c *Connector) ProfilesDir() string  { return filepath.Join(c.AppDataRoot(), "ProfilesV3") }
func (c *Connector) PluginsDir() string   { return filepath.Join(c.AppDataRoot(), "Plugins") }
func (c *Connector) IconPacksDir() string { return filepath.Join(c.AppDataRoot(), "IconPacks") }
func (c *Connector) StateDir() string {
	return filepath.Join(c.home, "Library", "Application Support", "schrodeck")
}
func (c *Connector) LogDir() string { return filepath.Join(c.home, "Library", "Logs", "schrodeck") }
func (c *Connector) ConfigPointer() string {
	return filepath.Join(c.home, ".config", "schrodeck", "config.toml")
}
func (c *Connector) Home() string { return c.home }

// StoreCandidates lists Dropbox folders, then iCloud Drive, that exist.
func (c *Connector) StoreCandidates() []string {
	var out []string
	if info, err := os.ReadFile(filepath.Join(c.home, ".dropbox", "info.json")); err == nil {
		out = append(out, DropboxPaths(info)...)
	}
	icloud := filepath.Join(c.home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if st, err := os.Stat(icloud); err == nil && st.IsDir() {
		out = append(out, icloud)
	}
	return out
}

func (c *Connector) HardwareID() (string, error) {
	out, err := exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", fmt.Errorf("ioreg: %w", err)
	}
	return ParseIOPlatformUUID(string(out))
}

func (c *Connector) UserName() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

func (c *Connector) FriendlyName() string {
	out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// HostNames returns the names this Mac is known by, for redaction.
func (c *Connector) HostNames() []string {
	var names []string
	if n := c.FriendlyName(); n != "" {
		names = append(names, n)
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		names = append(names, strings.TrimSuffix(h, ".local"))
	}
	return names
}

func (c *Connector) Installed() (bool, error) {
	_, err := os.Stat(appBundle)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (c *Connector) Running() (bool, error) {
	err := exec.Command("/usr/bin/pgrep", "-x", appProcess).Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, fmt.Errorf("pgrep: %w", err)
}

func (c *Connector) AppVersion() (string, error) {
	data, err := os.ReadFile(filepath.Join(appBundle, "Contents", "Info.plist"))
	if err != nil {
		return "", err
	}
	var info struct {
		Version string `plist:"CFBundleShortVersionString"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return "", err
	}
	if info.Version == "" {
		return "", fmt.Errorf("the app's Info.plist has no CFBundleShortVersionString")
	}
	return info.Version, nil
}

// prefs reads the app's preferences through `defaults export`, which goes
// through cfprefsd and so sees what the running app last wrote, unlike a
// direct read of the .plist file. Read-only.
func (c *Connector) prefs() (map[string]any, error) {
	out, err := exec.Command("/usr/bin/defaults", "export", prefsDomain, "-").Output()
	if err != nil {
		return nil, fmt.Errorf("defaults export %s: %w", prefsDomain, err)
	}
	var m map[string]any
	if _, err := plist.Unmarshal(out, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *Connector) SelectedProfile(appDeviceID string) (string, error) {
	p, err := c.prefs()
	if err != nil {
		return "", err
	}
	return Preferred(p, appDeviceID)
}

func (c *Connector) DeviceRecords() ([]map[string]any, error) {
	p, err := c.prefs()
	if err != nil {
		return nil, err
	}
	return DeviceRecords(p)
}

func (c *Connector) Decks() ([]ports.Deck, error) {
	ds, _, err := c.DecksWithLoadErrors()
	return ds, err
}

// DecksWithLoadErrors is Decks plus one *profile.FolderError per profile folder
// that failed to load. Such a profile cannot be matched to a deck, so callers
// that report on the host (observe, doctor) should show these rather than let
// the deck look unbound.
func (c *Connector) DecksWithLoadErrors() ([]ports.Deck, []error, error) {
	recs, err := c.DeviceRecords()
	if err != nil {
		return nil, nil, err
	}
	res, err := profile.LoadAll(os.DirFS(c.ProfilesDir()))
	if err != nil {
		return nil, nil, err
	}
	return decks.Enumerate(recs, res.Profiles, decks.KnownGeometry()), res.Errors, nil
}
