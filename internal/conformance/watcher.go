// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package conformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/csmarshall/schrodeck/internal/ports"
)

// waitFor is how long a suite waits for an event: generous, derived from the debounce so a slow runner doesn't turn a pass into a flake.
func waitFor(debounce time.Duration) time.Duration { return 10*debounce + 2*time.Second }

// WatcherRecursive checks that a write three directory levels below the watched root produces an event. The directories exist before watching starts, so only the file write can trigger it.
func WatcherRecursive(tb TB, w ports.Watcher, root string, debounce time.Duration) {
	tb.Helper()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		tb.Errorf("mkdir: %v", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := w.Watch(ctx, []string{root}, debounce)
	if err != nil {
		tb.Errorf("Watch: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(deep, "deep.txt"), []byte("x"), 0o644); err != nil {
		tb.Errorf("write: %v", err)
		return
	}
	select {
	case <-ch:
	case <-time.After(waitFor(debounce)):
		tb.Errorf("no event for a write three levels deep within %s; contract A requires recursive watching", waitFor(debounce))
	}
}

// burstAttempts bounds how often a burst is retried when the runner was too slow to write it inside one debounce window.
const burstAttempts = 3

// WatcherBurstIsOneEvent checks that several writes inside one debounce window are delivered as exactly one event. A burst is only a burst if it was written quickly: when the writes themselves took half the debounce window or longer (a starved CI runner), the attempt is discarded and retried in a fresh directory instead of blaming the watcher.
func WatcherBurstIsOneEvent(tb TB, w ports.Watcher, root string, debounce time.Duration) {
	tb.Helper()
	for attempt := 0; attempt < burstAttempts; attempt++ {
		dir := filepath.Join(root, fmt.Sprintf("attempt%d", attempt))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Errorf("mkdir: %v", err)
			return
		}
		if tooSlow, done := burstAttempt(tb, w, dir, debounce); done || !tooSlow {
			return
		}
	}
	tb.Errorf("could not write a burst inside half a debounce window in %d attempts; the runner is too starved to judge", burstAttempts)
}

// burstAttempt runs one burst. done is true when it reached a verdict (reported through tb); tooSlow is true when the burst was not a burst.
func burstAttempt(tb TB, w ports.Watcher, dir string, debounce time.Duration) (tooSlow, done bool) {
	tb.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := w.Watch(ctx, []string{dir}, debounce)
	if err != nil {
		tb.Errorf("Watch: %v", err)
		return false, true
	}
	start := time.Now()
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), []byte("x"), 0o644); err != nil {
			tb.Errorf("write: %v", err)
			return false, true
		}
	}
	if time.Since(start) >= debounce/2 {
		return true, false
	}
	select {
	case <-ch:
	case <-time.After(waitFor(debounce)):
		tb.Errorf("no event for a burst of writes")
		return false, true
	}
	select {
	case ev := <-ch:
		tb.Errorf("a burst within one debounce window produced a second event %v", ev.Paths)
	case <-time.After(3 * debounce):
	}
	return false, true
}
