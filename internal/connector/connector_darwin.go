// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build darwin

// Package connector picks the OS connector for the running system.
package connector

import (
	"time"

	"github.com/csmarshall/schrodeck/internal/connector/macos"
	"github.com/csmarshall/schrodeck/internal/host"
)

// New returns this computer's Host.
func New() (*host.Host, error) {
	c, err := macos.New()
	if err != nil {
		return nil, err
	}
	return &host.Host{Paths: c, Identity: c, Prefs: c, Decks: c, App: c, HostNames: c.HostNames(), Now: time.Now, Sleep: time.Sleep}, nil
}
