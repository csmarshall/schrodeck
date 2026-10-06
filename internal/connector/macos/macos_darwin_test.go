// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build darwin

package macos

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmarshall/schrodeck/internal/conformance"
	"github.com/csmarshall/schrodeck/internal/identity"
)

func connector(t *testing.T) *Connector {
	t.Helper()
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHostIdentityConformance(t *testing.T) {
	conformance.HostIdentityStable(t, connector(t))
}

// Contract A: host_id is stable across two processes. The test re-runs its
// own binary as a helper that prints the host_id (never the hardware id).
func TestHostIDStableAcrossProcesses(t *testing.T) {
	if os.Getenv("SCHRODECK_HOSTID_HELPER") == "1" {
		id, err := identity.HostID(connector(t))
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(1)
		}
		fmt.Println("HOSTID", id)
		os.Exit(0)
	}
	mine, err := identity.HostID(connector(t))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestHostIDStableAcrossProcesses")
	cmd.Env = append(os.Environ(), "SCHRODECK_HOSTID_HELPER=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper: %v %s", err, out)
	}
	if !strings.Contains(string(out), "HOSTID "+mine) {
		t.Fatal("host_id differs between two processes")
	}
}

func TestPaths(t *testing.T) {
	c := connector(t)
	if filepath.Base(c.ProfilesDir()) != "ProfilesV3" || !strings.HasPrefix(c.StateDir(), c.Home()) {
		t.Fatalf("paths: %s %s", c.ProfilesDir(), c.StateDir())
	}
}

// Read-only checks against the real app; skipped where it is not installed
// (CI runners).
// liveEnv opts in to tests that read this Mac's real Stream Deck install
// (read-only). They are off by default so `go test ./...` on a development
// Mac never touches a personal install unless asked to.
const liveEnv = "SCHRODECK_LIVE"

func TestLiveReadOnly(t *testing.T) {
	if os.Getenv(liveEnv) != "1" {
		t.Skip("reads the real Stream Deck install; set " + liveEnv + "=1 to run")
	}
	c := connector(t)
	installed, err := c.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Skip("Stream Deck app not installed")
	}
	if _, err := c.Running(); err != nil {
		t.Fatal(err)
	}
	if v, err := c.AppVersion(); err != nil || v == "" {
		t.Fatalf("AppVersion = %q, %v", v, err)
	}
	if _, err := c.Decks(); err != nil {
		t.Fatalf("Decks: %v", err)
	}
}
