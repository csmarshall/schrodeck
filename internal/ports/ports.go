// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package ports declares what an operating system must provide for schrodeck to run on it. Contract A (docs/contracts/os-connector.md) is the single source of truth: the interfaces here mirror it exactly, and tools/portcheck fails CI when they differ. Read the contract for each port's invariants; they are not repeated here.
package ports

import (
	"context"
	"time"
)

// Paths says where things live.
type Paths interface {
	AppDataRoot() string
	ProfilesDir() string
	PluginsDir() string
	IconPacksDir() string
	StateDir() string
	LogDir() string
	ConfigPointer() string
	Home() string
	StoreCandidates() []string
}

// HostIdentity identifies this host and user. The raw hardware id is never logged or stored.
type HostIdentity interface {
	HardwareID() (string, error)
	UserName() string
	FriendlyName() string
}

// AppControl drives the Stream Deck app process. Quit returning nil guarantees no app process can write ProfilesDir afterwards.
type AppControl interface {
	Installed() (bool, error)
	Running() (bool, error)
	Quit(timeout time.Duration) error
	Launch() error
	WaitSettled(quietFor, max time.Duration) error
}

// Geometry is a deck's key grid plus dials.
type Geometry struct {
	Columns int
	Rows    int
	Dials   int
}

// Deck is one deck the app knows on this host.
type Deck struct {
	AppDeviceID      string
	ManifestDeviceID string
	Geometry         Geometry
	Model            string
	Virtual          bool
	// SerialHash is derived from the deck's serial: treat it as an identifier (never log, print or commit it), not as a harmless digest.
	SerialHash string
}

// DeviceEnumerator lists this host's decks from the app's own data.
type DeviceEnumerator interface {
	Decks() ([]Deck, error)
}

// AppPrefs reads the app's preferences. Read-only; the selected profile is never written (ADR 0019).
type AppPrefs interface {
	AppVersion() (string, error)
	SelectedProfile(appDeviceID string) (string, error)
	DeviceRecords() ([]map[string]any, error)
}

// Event is a coalesced change notification. Events are hints, never truth.
type Event struct {
	Paths []string
}

// Watcher reports changes below paths, recursively. The channel closes when ctx ends.
type Watcher interface {
	Watch(ctx context.Context, paths []string, debounce time.Duration) (<-chan Event, error)
}

// AgentSpec describes the resident per-user agent to install.
type AgentSpec struct {
	Executable   string
	Args         []string
	DeviceAttach bool
}

// AgentStatus is what doctor and install --check report about the agent.
type AgentStatus struct {
	Installed bool
	Running   bool
	Detail    string
}

// Scheduler installs and inspects the resident agent.
type Scheduler interface {
	Install(spec AgentSpec) error
	Uninstall() error
	Status() (AgentStatus, error)
}

// Severity grades a notification.
type Severity int

// Notification severities.
const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityError
)

// Notification is one user-visible message. Deduplication is the core's job.
type Notification struct {
	Title      string
	Body       string
	Severity   Severity
	Persistent bool
}

// Notifier delivers notifications.
type Notifier interface {
	Available() bool
	Notify(n Notification) error
}

// Freshness is the sync client's view of a set of store paths. The zero value is Unknown, so an unset value fails closed.
type Freshness int

// Freshness values.
const (
	Unknown Freshness = iota
	Fresh
	InFlight
	Conflict
)

// StoreSync asks the cloud sync client whether the shared folder is current.
type StoreSync interface {
	ReadFreshness(paths []string) (Freshness, error)
	PushConfirmed(paths []string) (bool, error)
	EnsureDownloaded(paths []string) error
}
