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
	if err := RefuseInside(filepath.Join(dangling, "x.md"), root); err == nil {
		t.Fatal("a path through a dangling symlink into the root was allowed")
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
