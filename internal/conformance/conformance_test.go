// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package conformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmarshall/schrodeck/internal/ports"
	"github.com/csmarshall/schrodeck/internal/ports/fake"
)

// recorder is a TB that records failures instead of failing the test, so a test can assert that a suite DOES fail on a known-bad implementation.
type recorder struct{ failures []string }

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// requireFailed is the RED half of every known-bad test: the suite must have recorded a failure whose message contains wantSubstring, so a known-bad cannot pass by tripping some unrelated check. The failures are logged so the run shows WHY it failed.
func requireFailed(t *testing.T, rec *recorder, what, wantSubstring string) {
	t.Helper()
	if len(rec.failures) == 0 {
		t.Fatalf("suite passed %s", what)
	}
	matched := false
	for _, f := range rec.failures {
		t.Logf("suite correctly failed: %s", f)
		if strings.Contains(f, wantSubstring) {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("suite failed %s, but not for the intended reason: want a failure containing %q", what, wantSubstring)
	}
}

// harness timings are generous where waiting costs nothing on the good path (SettleMax, QuitTimeout) and tight only where the suite must observe a window (SettleQuiet).
func harness(t *testing.T, app ports.AppControl, dir string) AppHarness {
	t.Helper()
	return AppHarness{App: app, ProfilesDir: dir, QuitTimeout: 5 * time.Second, SettleQuiet: 200 * time.Millisecond, SettleMax: 15 * time.Second}
}

// --- AppControl ------------------------------------------------------------

func TestAppControlQuitOnFake(t *testing.T) {
	dir := t.TempDir()
	AppControlQuit(t, harness(t, fake.NewApp(dir), dir))
}

// writesAfterQuit violates contract A: Quit returns nil but the process keeps writing ProfilesDir. lateBy is how long after Quit returns the write lands; done is closed once it has.
type writesAfterQuit struct {
	*fake.App
	lateBy time.Duration
	done   chan struct{}
}

func (w writesAfterQuit) Quit(timeout time.Duration) error {
	if err := w.App.Quit(timeout); err != nil {
		return err
	}
	go func() {
		defer close(w.done)
		time.Sleep(w.lateBy)
		_ = os.WriteFile(filepath.Join(w.App.ProfilesDir, "late.json"), []byte("{}"), 0o644)
	}()
	return nil
}

func TestAppControlQuitCatchesWriteAfterQuit(t *testing.T) {
	dir := t.TempDir()
	h := harness(t, nil, dir)
	done := make(chan struct{})
	h.App = writesAfterQuit{App: fake.NewApp(dir), lateBy: h.SettleQuiet / 2, done: done} // half the suite's post-quit window
	rec := &recorder{}
	AppControlQuit(rec, h)
	requireFailed(t, rec, "an app that writes ProfilesDir after Quit returned nil", "ProfilesDir changed after Quit")
	select {
	case <-done:
	case <-time.After(h.SettleMax):
		t.Fatal("the late writer goroutine did not finish")
	}
}

// stillRunningAfterQuit violates contract A differently: Quit returns nil but Running still reports true.
type stillRunningAfterQuit struct{ *fake.App }

func (s stillRunningAfterQuit) Running() (bool, error) { return true, nil }

func TestAppControlQuitCatchesStillRunning(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	AppControlQuit(rec, harness(t, stillRunningAfterQuit{fake.NewApp(dir)}, dir))
	requireFailed(t, rec, "an app that is still running after Quit returned nil", "Running() = true after Quit")
}

func TestAppControlQuitTimeoutOnFake(t *testing.T) {
	app := fake.NewApp(t.TempDir())
	app.IgnoreQuit = true // set before Launch: the fake reads it under a mutex but this field is written without one
	AppControlQuitTimeout(t, app, 50*time.Millisecond)
}

// liesAboutQuit violates contract A: it reports success while still running.
type liesAboutQuit struct{ *fake.App }

func (l liesAboutQuit) Quit(timeout time.Duration) error {
	_ = l.App.Quit(timeout)
	return nil
}

func TestAppControlQuitTimeoutCatchesFalseSuccess(t *testing.T) {
	app := fake.NewApp(t.TempDir())
	app.IgnoreQuit = true
	rec := &recorder{}
	AppControlQuitTimeout(rec, liesAboutQuit{app}, 50*time.Millisecond)
	requireFailed(t, rec, "an app whose Quit returns nil while it is still running", "Quit returned nil for an app that did not exit")
}

// givesUpEarly violates the timeout half of Quit: it errors well before the timeout it was given.
type givesUpEarly struct{ *fake.App }

func (g givesUpEarly) Quit(timeout time.Duration) error { return fake.ErrQuitTimeout }

func TestAppControlQuitTimeoutCatchesEarlyGiveUp(t *testing.T) {
	app := fake.NewApp(t.TempDir())
	app.IgnoreQuit = true
	rec := &recorder{}
	AppControlQuitTimeout(rec, givesUpEarly{app}, 300*time.Millisecond)
	requireFailed(t, rec, "an app whose Quit gives up before its timeout", "before its")
}

// stopsAnywayButErrors violates the other half of Quit's guarantee: it waits out the timeout and reports failure, yet the app did stop. A caller told "still running" would then wait on, or wrongly avoid, a stopped app.
type stopsAnywayButErrors struct{ *fake.App }

func (s stopsAnywayButErrors) Quit(timeout time.Duration) error {
	time.Sleep(timeout)
	_ = s.App.Quit(timeout)
	return fake.ErrQuitTimeout
}

func TestAppControlQuitTimeoutCatchesStoppedDespiteError(t *testing.T) {
	rec := &recorder{}
	AppControlQuitTimeout(rec, stopsAnywayButErrors{fake.NewApp(t.TempDir())}, 50*time.Millisecond)
	requireFailed(t, rec, "an app that stopped although Quit returned an error", "app reported not running after Quit failed")
}

func TestAppControlLaunchSettleOnFake(t *testing.T) {
	dir := t.TempDir()
	AppControlLaunchSettle(t, harness(t, fake.NewApp(dir), dir))
}

// settlesImmediately violates contract A: WaitSettled returns as soon as the process is up, without waiting for ProfilesDir to go quiet. The good fake's WaitSettled cannot be seen to fail on its own (its Launch writes synchronously), so this known-bad is the evidence that the suite can fail at all.
type settlesImmediately struct{ *fake.App }

func (s settlesImmediately) WaitSettled(quietFor, max time.Duration) error { return nil }

func TestAppControlLaunchSettleCatchesNoWait(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	AppControlLaunchSettle(rec, harness(t, settlesImmediately{fake.NewApp(dir)}, dir))
	requireFailed(t, rec, "an app whose WaitSettled ignores ongoing writes", "most recent ProfilesDir write was only")
}

// A correct implementation must pass even when the suite's own writer stalls between starting a write and landing it (a starved runner): the write has not happened yet, so it cannot count as the most recent one. The stall is injected through the suite's writeStall hook.
func TestAppControlLaunchSettleToleratesWriterStall(t *testing.T) {
	dir := t.TempDir()
	h := harness(t, fake.NewApp(dir), dir)
	h.writeStall = func(i int) {
		if i == 2 {
			time.Sleep(2 * h.SettleQuiet)
		}
	}
	AppControlLaunchSettle(t, h)
}

// --- Watcher ---------------------------------------------------------------

const debounce = 200 * time.Millisecond

func TestWatcherConformanceOnFake(t *testing.T) {
	w := fake.Watcher{Interval: 10 * time.Millisecond}
	WatcherRecursive(t, w, t.TempDir(), debounce)
	WatcherBurstIsOneEvent(t, w, t.TempDir(), debounce)
}

func TestWatcherConformanceRepeated(t *testing.T) {
	w := fake.Watcher{Interval: 10 * time.Millisecond}
	for i := 0; i < 5; i++ {
		WatcherBurstIsOneEvent(t, w, t.TempDir(), debounce)
	}
}

// flatSignature describes the direct entries of paths (size and mtime for files), the way a non-recursive implementation would see them.
func flatSignature(paths []string) map[string]string {
	m := map[string]string{}
	for _, p := range paths {
		entries, _ := os.ReadDir(p)
		for _, e := range entries {
			if info, err := e.Info(); err == nil && !e.IsDir() {
				m[filepath.Join(p, e.Name())] = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
			} else {
				m[filepath.Join(p, e.Name())] = "dir"
			}
		}
	}
	return m
}

// topLevelOnly violates contract A: it does not watch recursively.
type topLevelOnly struct{}

func (topLevelOnly) Watch(ctx context.Context, paths []string, debounce time.Duration) (<-chan ports.Event, error) {
	prev := flatSignature(paths)
	ch := make(chan ports.Event, 4)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
				cur := flatSignature(paths)
				if fmt.Sprint(cur) != fmt.Sprint(prev) {
					prev = cur
					select {
					case ch <- ports.Event{Paths: paths}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return ch, nil
}

func TestWatcherRecursiveCatchesTopLevelOnly(t *testing.T) {
	rec := &recorder{}
	WatcherRecursive(rec, topLevelOnly{}, t.TempDir(), debounce)
	requireFailed(t, rec, "a watcher that only sees the top level", "no event for a write three levels deep")
}

// perWrite violates contract A: no debounce, one event per changed file.
type perWrite struct{}

func (perWrite) Watch(ctx context.Context, paths []string, debounce time.Duration) (<-chan ports.Event, error) {
	prev := flatSignature(paths)
	ch := make(chan ports.Event, 64)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
				cur := flatSignature(paths)
				var names []string
				for name, sig := range cur {
					if prev[name] != sig {
						names = append(names, name)
					}
				}
				sort.Strings(names)
				prev = cur
				for _, name := range names {
					select {
					case ch <- ports.Event{Paths: []string{name}}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return ch, nil
}

func TestWatcherBurstCatchesPerWriteEvents(t *testing.T) {
	rec := &recorder{}
	WatcherBurstIsOneEvent(rec, perWrite{}, t.TempDir(), debounce)
	requireFailed(t, rec, "a watcher that sends one event per write", "second event")
}

// silentWatcher violates contract A: it is accepted but never delivers an event.
type silentWatcher struct{}

func (silentWatcher) Watch(ctx context.Context, paths []string, debounce time.Duration) (<-chan ports.Event, error) {
	ch := make(chan ports.Event)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func TestWatcherBurstCatchesSilentWatcher(t *testing.T) {
	rec := &recorder{}
	WatcherBurstIsOneEvent(rec, silentWatcher{}, t.TempDir(), debounce)
	requireFailed(t, rec, "a watcher that never emits", "no event for a burst")
}

// --- HostIdentity ----------------------------------------------------------

func TestHostIdentityOnFake(t *testing.T) {
	HostIdentityStable(t, fake.HostIdentity{Hardware: "HW-TEST-0001", User: "alice", Friendly: "test mac"})
}

// flappingIdentity violates contract A: the hardware id differs on every call.
type flappingIdentity struct {
	mu *sync.Mutex
	n  *int
}

func (f flappingIdentity) HardwareID() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.n++
	return fmt.Sprintf("HW-%d", *f.n), nil
}
func (f flappingIdentity) UserName() string     { return "alice" }
func (f flappingIdentity) FriendlyName() string { return "test mac" }

func TestHostIdentityCatchesUnstableID(t *testing.T) {
	rec := &recorder{}
	n := 0
	HostIdentityStable(rec, flappingIdentity{&sync.Mutex{}, &n})
	requireFailed(t, rec, "a hardware id that changes between calls", "HardwareID changed")
}

// emptyHardwareID violates contract A: no hardware id (the user name is fine, so only the hardware-id check can catch it).
type emptyHardwareID struct{}

func (emptyHardwareID) HardwareID() (string, error) { return "", nil }
func (emptyHardwareID) UserName() string            { return "alice" }
func (emptyHardwareID) FriendlyName() string        { return "test mac" }

func TestHostIdentityCatchesEmptyHardwareID(t *testing.T) {
	rec := &recorder{}
	HostIdentityStable(rec, emptyHardwareID{})
	requireFailed(t, rec, "an identity with an empty hardware id", "HardwareID is empty")
}

// emptyUserName violates contract A: no user name (the hardware id is fine, so only the user-name check can catch it).
type emptyUserName struct{}

func (emptyUserName) HardwareID() (string, error) { return "HW-TEST-0001", nil }
func (emptyUserName) UserName() string            { return "" }
func (emptyUserName) FriendlyName() string        { return "test mac" }

func TestHostIdentityCatchesEmptyUserName(t *testing.T) {
	rec := &recorder{}
	HostIdentityStable(rec, emptyUserName{})
	requireFailed(t, rec, "an identity with an empty user name", "UserName is empty")
}
