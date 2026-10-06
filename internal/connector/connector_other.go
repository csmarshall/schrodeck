// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !darwin

// Package connector picks the OS connector for the running system.
package connector

import (
	"fmt"
	"runtime"

	"github.com/csmarshall/schrodeck/internal/host"
)

// New reports that no connector exists for this OS yet (contract A: a new
// OS is a new connector).
func New() (*host.Host, error) {
	return nil, fmt.Errorf("no schrodeck connector for %s yet", runtime.GOOS)
}
