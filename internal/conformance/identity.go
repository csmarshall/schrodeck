// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package conformance

import "github.com/csmarshall/schrodeck/internal/ports"

// HostIdentityStable checks that the hardware id is present and identical across two calls, and that the user name is present.
//
// Contract A also requires the id to be stable across two processes and to differ between two users on one host. Neither is checkable against a fake, so both are deferred to M1, where the real identity lands: each real connector's own test then runs a helper subprocess for the two-process case and a second OS user for the two-users case.
//
// The suite never prints the hardware id itself (contract A: never logged).
func HostIdentityStable(tb TB, h ports.HostIdentity) {
	tb.Helper()
	a, err := h.HardwareID()
	if err != nil {
		tb.Errorf("HardwareID: %v", err)
		return
	}
	b, err := h.HardwareID()
	if err != nil {
		tb.Errorf("HardwareID (second call): %v", err)
		return
	}
	if a == "" {
		tb.Errorf("HardwareID is empty")
	}
	if a != b {
		tb.Errorf("HardwareID changed between two calls")
	}
	if h.UserName() == "" {
		tb.Errorf("UserName is empty")
	}
}
