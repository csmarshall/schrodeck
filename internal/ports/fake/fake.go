// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package fake provides test doubles for the ports in contract A. Core tests run against these on any OS; the conformance suite runs against them too, so a fake that drifts from the contract fails the same tests a real connector would.
package fake

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/csmarshall/schrodeck/internal/ports"
)

// Paths puts every location under Root.
type Paths struct{ Root string }

func (p Paths) AppDataRoot() string   { return filepath.Join(p.Root, "app") }
func (p Paths) ProfilesDir() string   { return filepath.Join(p.AppDataRoot(), "ProfilesV3") }
func (p Paths) PluginsDir() string    { return filepath.Join(p.AppDataRoot(), "Plugins") }
func (p Paths) IconPacksDir() string  { return filepath.Join(p.AppDataRoot(), "IconPacks") }
func (p Paths) StateDir() string      { return filepath.Join(p.Root, "state") }
func (p Paths) LogDir() string        { return filepath.Join(p.Root, "logs") }
func (p Paths) ConfigPointer() string { return filepath.Join(p.Root, "config", "config.toml") }
func (p Paths) Home() string          { return filepath.Join(p.Root, "home") }
func (p Paths) StoreCandidates() []string {
	return []string{filepath.Join(p.Root, "store")}
}

// HostIdentity returns fixed values.
type HostIdentity struct{ Hardware, User, Friendly string }

func (h HostIdentity) HardwareID() (string, error) {
	if h.Hardware == "" {
		return "", fmt.Errorf("fake host identity: no hardware id")
	}
	return h.Hardware, nil
}
func (h HostIdentity) UserName() string     { return h.User }
func (h HostIdentity) FriendlyName() string { return h.Friendly }

// Decks returns a fixed deck list.
type Decks struct {
	List []ports.Deck
	Err  error
}

func (d Decks) Decks() ([]ports.Deck, error) { return d.List, d.Err }

// Prefs returns fixed preferences.
type Prefs struct {
	Version  string
	Selected map[string]string
	Records  []map[string]any
}

func (p Prefs) AppVersion() (string, error) {
	if p.Version == "" {
		return "", fmt.Errorf("fake prefs: no app version")
	}
	return p.Version, nil
}

func (p Prefs) SelectedProfile(appDeviceID string) (string, error) {
	id, ok := p.Selected[appDeviceID]
	if !ok {
		return "", fmt.Errorf("fake prefs: no device %q", appDeviceID)
	}
	return id, nil
}

func (p Prefs) DeviceRecords() ([]map[string]any, error) { return p.Records, nil }

// Scheduler records the installed agent in memory.
type Scheduler struct {
	mu   sync.Mutex
	spec *ports.AgentSpec
}

func (s *Scheduler) Install(spec ports.AgentSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spec = &spec
	return nil
}

func (s *Scheduler) Uninstall() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spec = nil
	return nil
}

func (s *Scheduler) Status() (ports.AgentStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ports.AgentStatus{Installed: s.spec != nil, Running: s.spec != nil}, nil
}

// Notifier records what it was asked to deliver.
type Notifier struct {
	Avail bool
	mu    sync.Mutex
	sent  []ports.Notification
}

func (n *Notifier) Available() bool { return n.Avail }

func (n *Notifier) Notify(m ports.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, m)
	return nil
}

// Sent returns a copy of every notification delivered so far.
func (n *Notifier) Sent() []ports.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]ports.Notification(nil), n.sent...)
}

// StoreSync reports a fixed freshness.
type StoreSync struct {
	State     ports.Freshness
	Confirmed bool
}

func (s *StoreSync) ReadFreshness([]string) (ports.Freshness, error) { return s.State, nil }
func (s *StoreSync) PushConfirmed([]string) (bool, error)            { return s.Confirmed, nil }
func (s *StoreSync) EnsureDownloaded([]string) error                 { return nil }

// DirDigest is a content digest of every regular file below dir (relative path and bytes). A missing dir digests like an empty one.
func DirDigest(dir string) (string, error) {
	var lines []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == dir {
				return filepath.SkipDir
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		sum := sha256.Sum256(b)
		lines = append(lines, filepath.ToSlash(rel)+"\x00"+hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
