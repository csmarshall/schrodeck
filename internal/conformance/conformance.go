// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package conformance is contract A's conformance suite: tests written once against the port interfaces and run against every implementation (the fakes here, each real connector on its own OS's CI runner). Every suite has a known-bad implementation in this package's tests that it must fail. The StoreSync and filesystem-guarantee suites arrive with the milestones that introduce those behaviors (M2 and M3).
package conformance

// TB is the part of testing.TB the suites use. *testing.T satisfies it; the suite's own tests pass a recorder to prove a suite fails on bad input. Suites report with Errorf and return early instead of calling Fatalf.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
}
