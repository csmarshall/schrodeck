// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package host bundles the connector ports one command run needs. The
// command line builds a Host from the OS connector (cmd/schrodeck); tests
// build one from fakes.
package host

import (
	"time"

	"github.com/csmarshall/schrodeck/internal/ports"
)

// AppPresence is the read-only part of ports.AppControl. M1 never quits or
// launches the app, so it asks for no more than this.
type AppPresence interface {
	Installed() (bool, error)
	Running() (bool, error)
}

// DeckLoader is a DeviceEnumerator that also returns one
// *profile.FolderError per profile folder that failed to load. Such a profile
// cannot be matched to a deck, so commands that report on the host show these
// errors rather than let the deck look unbound. A connector may implement it;
// commands fall back to Decks when it does not.
type DeckLoader interface {
	DecksWithLoadErrors() ([]ports.Deck, []error, error)
}

// Host is this computer as seen through its connector.
type Host struct {
	Paths    ports.Paths
	Identity ports.HostIdentity
	Prefs    ports.AppPrefs
	Decks    ports.DeviceEnumerator
	App      AppPresence
	// HostNames are this computer's names (friendly name, network name), for
	// redaction.
	HostNames []string
	Now       func() time.Time
	Sleep     func(time.Duration)
}
