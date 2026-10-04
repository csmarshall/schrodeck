// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package fake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/csmarshall/schrodeck/internal/ports"
)

// Compile-time proof that every fake satisfies its port.
var (
	_ ports.Paths            = Paths{}
	_ ports.HostIdentity     = HostIdentity{}
	_ ports.AppControl       = (*App)(nil)
	_ ports.DeviceEnumerator = Decks{}
	_ ports.AppPrefs         = Prefs{}
	_ ports.Watcher          = Watcher{}
	_ ports.Scheduler        = (*Scheduler)(nil)
	_ ports.Notifier         = (*Notifier)(nil)
	_ ports.StoreSync        = (*StoreSync)(nil)
)

func TestAppLifecycle(t *testing.T) {
	dir := t.TempDir()
	app := NewApp(dir)
	if err := app.Launch(); err != nil {
		t.Fatal(err)
	}
	if err := app.WaitSettled(20*time.Millisecond, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := app.Quit(time.Second); err != nil {
		t.Fatal(err)
	}
	if running, _ := app.Running(); running {
		t.Fatal("still running after Quit")
	}
}

func TestAppIgnoringQuitTimesOut(t *testing.T) {
	app := NewApp(t.TempDir())
	app.IgnoreQuit = true
	if err := app.Launch(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := app.Quit(50 * time.Millisecond)
	if !errors.Is(err, ErrQuitTimeout) {
		t.Fatalf("Quit error = %v, want ErrQuitTimeout", err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("Quit gave up before its timeout")
	}
	if running, _ := app.Running(); !running {
		t.Fatal("an app that ignored Quit must still be running")
	}
}

func TestWatcherSeesDeepWrite(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := Watcher{Interval: 5 * time.Millisecond}.Watch(ctx, []string{root}, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if len(ev.Paths) == 0 {
			t.Fatal("event with no paths")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event for a write three levels deep")
	}
	cancel()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range ch {
		}
	}()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher channel was not closed after cancel")
	}
}

func TestDirDigestChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	a, err := DirDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := DirDigest(dir)
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, _ := DirDigest(dir)
	if a == b || b == c {
		t.Fatalf("digest did not change: %s %s %s", a, b, c)
	}
}

func TestNotifierRecords(t *testing.T) {
	n := &Notifier{Avail: true}
	if err := n.Notify(ports.Notification{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if got := n.Sent(); len(got) != 1 || got[0].Title != "t" {
		t.Fatalf("Sent() = %v", got)
	}
}
