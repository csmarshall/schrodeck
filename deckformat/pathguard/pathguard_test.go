// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefuseInside(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	if err := os.MkdirAll(filepath.Join(root, "ProfilesV3"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "out")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink outside the root that points into it.
	link := filepath.Join(base, "innocent")
	if err := os.Symlink(filepath.Join(root, "ProfilesV3"), link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		inside bool
	}{
		{root, true},
		{filepath.Join(root, "ProfilesV3", "x.md"), true},
		{filepath.Join(root, "ProfilesV3", "new", "deeper", "x.md"), true}, // does not exist yet
		{outside + "/../app/ProfilesV3/x.md", true},                        // "../" walk back in; built by concatenation so the ".." reaches the guard
		{filepath.Join(link, "x.md"), true},                                // symlinked directory
		{filepath.Join(link, "new", "x.md"), true},                         // symlink, then a missing part
		{link + "/../x.md", true},                                          // F3: the kernel resolves ".." physically, through the link
		{strings.ToUpper(filepath.Join(root, "PROFILESV3", "x.md")), true}, // letter case
		{filepath.Join(outside, "x.md"), false},
		{filepath.Join(base, "app-other", "x.md"), false}, // shares a prefix, not a parent
		{filepath.Join(base, "..app", "x.md"), false},
	}
	// Where a naive write to link+"/../x.md" lands: the kernel follows the link
	// first, so the file appears in the parent of the link's target (inside the
	// protected root), not beside the link.
	probe := link + "/../x.md"
	if err := os.WriteFile(probe, []byte("probe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "x.md")); err != nil {
		t.Errorf("naive write did not land inside the protected root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "x.md")); err == nil {
		t.Errorf("naive write landed beside the link, so this case proves nothing")
	}
	if err := os.Remove(filepath.Join(root, "x.md")); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		err := RefuseInside(c.target, "", root)
		if got := errors.Is(err, ErrInside); got != c.inside {
			t.Errorf("RefuseInside(%s) = %v, want inside=%v", c.target, err, c.inside)
		}
	}
}

func TestSingleName(t *testing.T) {
	for _, ok := range []string{"A.sdProfile", "x", "..x.sdProfile"} {
		if err := SingleName(ok); err != nil {
			t.Errorf("SingleName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "../A.sdProfile", "a/b.sdProfile", `a\b.sdProfile`} {
		if err := SingleName(bad); !errors.Is(err, ErrNotName) {
			t.Errorf("SingleName(%q) accepted", bad)
		}
	}
}

// A write through a dangling symlink creates the link's target, which Resolve
// cannot locate, so the guard must refuse rather than guess.
func TestRefuseInsideRefusesDanglingSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(base, "dangling")
	if err := os.Symlink(filepath.Join(root, "not-yet"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := RefuseInside(filepath.Join(dangling, "x.md"), root); !errors.Is(err, ErrUnresolvable) {
		t.Fatalf("a path through a dangling symlink into the root: %v", err)
	}
}

func TestResolveFollowsTheKernel(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "a", "b")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "l")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(link + "/../x/./y")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "a", "x", "y"); got != want {
		t.Fatalf("Resolve = %s, want %s", got, want)
	}
}

// C1: after a missing component, ".." returns to existing directories, and the
// symlink behind it must still be followed. The path cannot be located without
// guessing, so Resolve fails closed.
func TestRefuseInsideDotDotAfterMissingComponent(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "Settings", "app")
	if err := os.MkdirAll(filepath.Join(root, "ProfilesV3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "ProfilesV3"), filepath.Join(base, "innocent")); err != nil {
		t.Fatal(err)
	}
	target := base + "/nonexist/../innocent/x"
	if err := RefuseInside(target, root); !errors.Is(err, ErrUnresolvable) {
		t.Fatalf("RefuseInside(%s) = %v, want ErrUnresolvable", target, err)
	}
	// Where the kernel puts a write there: inside the root, through the link.
	if err := os.Mkdir(base+"/nonexist", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ProfilesV3", "x")); err != nil {
		t.Fatalf("expected the write to land inside the root: %v", err)
	}
}

// requireAlias skips when this file system does not treat variant as the same
// directory as real, since the alias is then not an alias at all.
func requireAlias(t *testing.T, variant string) {
	t.Helper()
	if _, err := os.Stat(variant); err != nil {
		t.Skipf("this file system does not alias %s: %v", variant, err)
	}
}

// C2: inside-ness is decided by file identity, so every spelling the file
// system treats as the same directory is refused. The non-ASCII names are byte
// escapes: U+017F (long s) case-folds to "s" on APFS, which strings.ToLower
// does not do.
func TestRefuseInsideAliases(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "Settings", "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	nfc := filepath.Join(base, "caf\xc3\xa9")
	if err := os.MkdirAll(nfc, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ target, root string }{
		"firmlink":    {"/System/Volumes/Data" + root + "/x", root},
		"long s fold": {filepath.Join(base, "Setting\xc5\xbf", "app", "x"), root},
		"NFD vs NFC":  {filepath.Join(base, "cafe\xcc\x81", "x"), nfc},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			requireAlias(t, filepath.Dir(c.target))
			if err := RefuseInside(c.target, c.root); !errors.Is(err, ErrInside) {
				t.Fatalf("RefuseInside(%q) = %v, want ErrInside", c.target, err)
			}
		})
	}
}
