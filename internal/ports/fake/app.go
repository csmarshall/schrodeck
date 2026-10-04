// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package fake

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrQuitTimeout is returned when the app is still running at Quit's timeout.
var ErrQuitTimeout = errors.New("fake app: still running at quit timeout")

// App simulates the Stream Deck app: launching rewrites a state file in ProfilesDir (as the real app rewrites manifests on launch), and a graceful quit flushes once and then never writes again.
type App struct {
	ProfilesDir string
	// IgnoreQuit simulates an app that does not exit when asked.
	IgnoreQuit bool

	mu       sync.Mutex
	running  bool
	launches int
}

// NewApp returns an installed, stopped app writing into profilesDir.
func NewApp(profilesDir string) *App { return &App{ProfilesDir: profilesDir} }

func (a *App) Installed() (bool, error) { return true, nil }

func (a *App) Running() (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running, nil
}

func (a *App) Launch() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = true
	a.launches++
	return a.writeState(fmt.Sprintf("launched %d", a.launches))
}

func (a *App) Quit(timeout time.Duration) error {
	a.mu.Lock()
	ignore := a.IgnoreQuit
	a.mu.Unlock()
	if ignore {
		time.Sleep(timeout)
		return ErrQuitTimeout
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.writeState("flushed on quit"); err != nil {
		return err
	}
	a.running = false
	return nil
}

func (a *App) WaitSettled(quietFor, max time.Duration) error {
	deadline := time.Now().Add(max)
	last, err := DirDigest(a.ProfilesDir)
	if err != nil {
		return err
	}
	quietSince := time.Now()
	for {
		running, _ := a.Running()
		if running && time.Since(quietSince) >= quietFor {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("fake app: not settled within %s", max)
		}
		time.Sleep(quietFor / 4)
		cur, err := DirDigest(a.ProfilesDir)
		if err != nil {
			return err
		}
		if cur != last {
			last, quietSince = cur, time.Now()
		}
	}
}

// writeState must be called with a.mu held.
func (a *App) writeState(s string) error {
	if err := os.MkdirAll(a.ProfilesDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.ProfilesDir, ".fake-app-state"), []byte(s), 0o644)
}
