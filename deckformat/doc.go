// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package deckformat is a toolkit for reading, comparing and checking the Stream Deck app's on-disk profile format (ADR 0031). It is a separate Go module so that it can be extracted into its own repository; it must never import a package of the schrodeck module (a CI check enforces this).
//
// Interoperability only: it reads and writes the user's own configuration files and never decrypts Elgato's plugin bundles or redistributes Elgato code or assets.
package deckformat
