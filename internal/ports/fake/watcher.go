// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package fake

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	"github.com/csmarshall/schrodeck/internal/ports"
)

// Watcher polls real directories recursively. It honors contract A's semantics (recursive, debounced, hints only) without any OS API, so core tests and the conformance suite can run it anywhere.
type Watcher struct {
	// Interval between polls; 10ms when zero.
	Interval time.Duration
}

type fileState struct {
	size    int64
	modTime time.Time
	dir     bool
}

func (w Watcher) interval() time.Duration {
	if w.Interval <= 0 {
		return 10 * time.Millisecond
	}
	return w.Interval
}

// Watch emits one Event per quiet period: changes are collected until none has been seen for debounce, then delivered together.
func (w Watcher) Watch(ctx context.Context, paths []string, debounce time.Duration) (<-chan ports.Event, error) {
	prev, err := scan(paths)
	if err != nil {
		return nil, err
	}
	ch := make(chan ports.Event, 16)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(w.interval())
		defer ticker.Stop()
		pending := map[string]bool{}
		var lastChange time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				cur, err := scan(paths)
				if err != nil {
					continue // events are hints; the next poll retries
				}
				for _, p := range changed(prev, cur) {
					pending[p] = true
					lastChange = now
				}
				prev = cur
				if len(pending) > 0 && now.Sub(lastChange) >= debounce {
					ev := ports.Event{}
					for p := range pending {
						ev.Paths = append(ev.Paths, p)
					}
					sort.Strings(ev.Paths)
					pending = map[string]bool{}
					select {
					case ch <- ev:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return ch, nil
}

func scan(roots []string) (map[string]fileState, error) {
	states := map[string]fileState{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			states[path] = fileState{size: info.Size(), modTime: info.ModTime(), dir: d.IsDir()}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return states, nil
}

// changed ignores directory modification times on purpose: a directory's mtime changes when a direct child is added, which would let a non-recursive implementation look recursive for shallow writes.
func changed(a, b map[string]fileState) []string {
	var out []string
	for p, sb := range b {
		sa, ok := a[p]
		if !ok || (!sb.dir && (sa.size != sb.size || !sa.modTime.Equal(sb.modTime))) {
			out = append(out, p)
		}
	}
	for p := range a {
		if _, ok := b[p]; !ok {
			out = append(out, p)
		}
	}
	return out
}
