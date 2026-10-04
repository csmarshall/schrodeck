// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/csmarshall/schrodeck/internal/ports"
	"github.com/csmarshall/schrodeck/internal/ports/fake"
)

// AppHarness is what the AppControl suites need from an implementation.
type AppHarness struct {
	App         ports.AppControl
	ProfilesDir string
	QuitTimeout time.Duration
	// SettleQuiet is also the window after Quit during which no write may appear; a real connector sets it to the settle window contract C P4 measured for its app version.
	SettleQuiet time.Duration
	SettleMax   time.Duration

	// writeStall, when set, runs inside AppControlLaunchSettle's writer between taking a write's timestamp and landing the write. Only this package's tests set it, to simulate a starved runner.
	writeStall func(write int)
}

// dirStaysQuiet reports whether dir's contents are unchanged across window. It is the one home of "has this directory been written to".
func dirStaysQuiet(dir string, window time.Duration) (bool, error) {
	before, err := fake.DirDigest(dir)
	if err != nil {
		return false, err
	}
	time.Sleep(window)
	after, err := fake.DirDigest(dir)
	if err != nil {
		return false, err
	}
	return before == after, nil
}

// AppControlQuit checks contract A's Quit guarantee: after Quit returns nil the app is not running and nothing writes ProfilesDir. The window is only as long as h.SettleQuiet, so a write that arrives later than that is not caught; real connectors must size it from contract C.
func AppControlQuit(tb TB, h AppHarness) {
	tb.Helper()
	if err := h.App.Launch(); err != nil {
		tb.Errorf("Launch: %v", err)
		return
	}
	if err := h.App.WaitSettled(h.SettleQuiet, h.SettleMax); err != nil {
		tb.Errorf("WaitSettled after Launch: %v", err)
		return
	}
	if err := h.App.Quit(h.QuitTimeout); err != nil {
		tb.Errorf("Quit: %v", err)
		return
	}
	running, err := h.App.Running()
	if err != nil {
		tb.Errorf("Running after Quit: %v", err)
		return
	}
	if running {
		tb.Errorf("Running() = true after Quit returned nil")
		return
	}
	quiet, err := dirStaysQuiet(h.ProfilesDir, h.SettleQuiet)
	if err != nil {
		tb.Errorf("digest ProfilesDir: %v", err)
		return
	}
	if !quiet {
		tb.Errorf("ProfilesDir changed after Quit returned nil; contract A: no app process may write it afterwards")
	}
}

// AppControlQuitTimeout checks that an app which won't exit makes Quit return an error no earlier than the timeout, and is left running.
func AppControlQuitTimeout(tb TB, stuck ports.AppControl, timeout time.Duration) {
	tb.Helper()
	if err := stuck.Launch(); err != nil {
		tb.Errorf("Launch: %v", err)
		return
	}
	start := time.Now()
	err := stuck.Quit(timeout)
	elapsed := time.Since(start)
	if err == nil {
		tb.Errorf("Quit returned nil for an app that did not exit")
		return
	}
	if elapsed < timeout {
		tb.Errorf("Quit gave up after %s, before its %s timeout", elapsed, timeout)
	}
	running, rerr := stuck.Running()
	if rerr != nil {
		tb.Errorf("Running after failed Quit: %v", rerr)
		return
	}
	if !running {
		tb.Errorf("app reported not running after Quit failed")
	}
}

// AppControlLaunchSettle checks contract A's "launch + settle": WaitSettled returns only once the app is running AND ProfilesDir has been quiet for the requested window. The suite writes ProfilesDir (a first write before WaitSettled is called, then every quarter window for three windows) and requires that when WaitSettled returns, the most recent write is at least one quiet window old. Each write is stamped with the time it started but only recorded once it has landed, so the check can only be lenient, never fail a correct implementation on a slow runner: a write that has not landed yet is not "the most recent", and if the writer stalls for longer than the window, returning early is correct and the check agrees.
func AppControlLaunchSettle(tb TB, h AppHarness) {
	tb.Helper()
	if err := h.App.Launch(); err != nil {
		tb.Errorf("Launch: %v", err)
		return
	}
	var mu sync.Mutex
	var lastWrite time.Time
	write := func(i int) {
		started := time.Now()
		if h.writeStall != nil {
			h.writeStall(i)
		}
		// The content differs on every write: settling is judged by content, not mtime.
		_ = os.WriteFile(filepath.Join(h.ProfilesDir, "busy.json"), []byte(fmt.Sprintf("write %d", i)), 0o644)
		// Only a write that has landed counts as "the most recent", and it is stamped with the time it started, so any timing error makes the check more lenient, never stricter.
		mu.Lock()
		lastWrite = started
		mu.Unlock()
	}
	write(0)
	done := make(chan struct{})
	go func() {
		defer close(done)
		end := time.Now().Add(3 * h.SettleQuiet)
		for i := 1; time.Now().Before(end); i++ {
			time.Sleep(h.SettleQuiet / 4)
			write(i)
		}
	}()
	err := h.App.WaitSettled(h.SettleQuiet, h.SettleMax)
	returned := time.Now()
	mu.Lock()
	quietFor := returned.Sub(lastWrite)
	mu.Unlock()
	select {
	case <-done:
	case <-time.After(h.SettleMax):
		tb.Errorf("writer goroutine did not finish")
	}
	if err != nil {
		tb.Errorf("WaitSettled: %v", err)
		return
	}
	if quietFor < h.SettleQuiet {
		tb.Errorf("WaitSettled returned while the most recent ProfilesDir write was only %s old; contract A: settled means quiet for %s", quietFor, h.SettleQuiet)
	}
}
