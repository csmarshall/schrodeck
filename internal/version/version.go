// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package version holds the build's version string. Release builds set it with -ldflags "-X github.com/csmarshall/schrodeck/internal/version.Version=<tag>" (ADR 0028); every other build reports "dev".
package version

// Version is the schrodeck version this binary was built from.
var Version = "dev"
