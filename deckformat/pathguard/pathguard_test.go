// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pathguard

import (
	"errors"
	"io/fs"
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
		err := RefuseInside(c.target, root)
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

// A root that does not exist yet can still be aliased: the write would create
// it. The deepest existing ancestor is compared by identity and the missing
// tail by case folding and NFC, so the long-s spelling of an absent root is
// refused, with or without a file system that folds case.
func TestRefuseInsideAbsentRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "Settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	longS := "Setting\xc5\xbf"
	t.Run("existing parent, absent leaf", func(t *testing.T) {
		requireAlias(t, filepath.Join(base, longS))
		target := filepath.Join(base, longS, "notyet", "x")
		if err := RefuseInside(target, filepath.Join(base, "Settings", "notyet")); !errors.Is(err, ErrInside) {
			t.Fatalf("RefuseInside(%q) = %v, want ErrInside", target, err)
		}
	})
	t.Run("everything absent", func(t *testing.T) {
		target := filepath.Join(base, "Absent"+longS[len(longS)-2:], "x")
		root := filepath.Join(base, "Absents")
		if err := RefuseInside(target, root); !errors.Is(err, ErrInside) {
			t.Fatalf("RefuseInside(%q) = %v, want ErrInside", target, err)
		}
	})
	// Full case folding (APFS), not simple: sharp s folds to "ss" and the fi
	// ligature to "fi", which strings.EqualFold does not do.
	for name, c := range map[string]struct{ spelled, real string }{
		"sharp s":     {"Stra\xc3\x9fe", "Strasse"},
		"fi ligature": {"Pro\xef\xac\x81les", "Profiles"},
	} {
		t.Run("full fold "+name, func(t *testing.T) {
			probe := filepath.Join(base, "probe-"+c.real)
			if err := os.Mkdir(probe, 0o755); err != nil {
				t.Fatal(err)
			}
			requireAlias(t, filepath.Join(base, "probe-"+c.spelled))
			target := filepath.Join(base, c.spelled, "x")
			if err := RefuseInside(target, filepath.Join(base, c.real)); !errors.Is(err, ErrInside) {
				t.Fatalf("RefuseInside(%q) = %v, want ErrInside", target, err)
			}
		})
	}
	t.Run("a different absent name is allowed", func(t *testing.T) {
		if err := RefuseInside(filepath.Join(base, "Other", "x"), filepath.Join(base, "Settings", "notyet")); err != nil {
			t.Fatalf("unrelated path refused: %v", err)
		}
		if err := RefuseInside(filepath.Join(base, "Settings", "notyet-too", "x"), filepath.Join(base, "Settings", "notyet")); err != nil {
			t.Fatalf("sibling sharing a prefix refused: %v", err)
		}
	})
}

// Create is the one way a command opens a new file at a path a person typed.
func TestCreate(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(protected, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "out")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	unchanged := func(t *testing.T) {
		t.Helper()
		got, err := os.ReadFile(protected)
		if err != nil || string(got) != "original" {
			t.Fatalf("the protected file changed: %q, %v", got, err)
		}
	}

	t.Run("creates a new file, creating parents, and returns the resolved path", func(t *testing.T) {
		f, got, err := Create(filepath.Join(outside, "a", "b", "new.txt"), root)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if want := filepath.Join(outside, "a", "b", "new.txt"); got != want {
			t.Errorf("resolved path = %q, want %q", got, want)
		}
		if _, err := f.WriteString("x"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("an existing file is never opened", func(t *testing.T) {
		existing := filepath.Join(outside, "existing.txt")
		if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		f, _, err := Create(existing, root)
		if err == nil {
			f.Close()
			t.Fatal("Create opened an existing file")
		}
		if !errors.Is(err, fs.ErrExist) {
			t.Errorf("err = %v, want fs.ErrExist", err)
		}
		if got, _ := os.ReadFile(existing); string(got) != "keep" {
			t.Errorf("existing file now %q", got)
		}
	})
	t.Run("a hard link to a protected file is not overwritten", func(t *testing.T) {
		link := filepath.Join(outside, "linked.json")
		if err := os.Link(protected, link); err != nil {
			t.Skipf("no hard links here: %v", err)
		}
		f, _, err := Create(link, root)
		if err == nil {
			f.Close()
			t.Fatal("Create opened a hard link to a protected file")
		}
		unchanged(t)
	})
	t.Run("a symlink into the root, then dotdot, is refused", func(t *testing.T) {
		sub := filepath.Join(root, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(outside, "into")
		if err := os.Symlink(sub, link); err != nil {
			t.Fatal(err)
		}
		f, _, err := Create(link+"/../x.txt", root)
		if err == nil {
			f.Close()
		}
		if !errors.Is(err, ErrInside) {
			t.Fatalf("err = %v, want ErrInside", err)
		}
		if _, err := os.Stat(filepath.Join(root, "x.txt")); err == nil {
			t.Error("a file was created inside the protected root")
		}
	})
	t.Run("a target inside a root is refused and creates nothing", func(t *testing.T) {
		target := filepath.Join(root, "deeper", "new.txt")
		f, _, err := Create(target, root)
		if err == nil {
			f.Close()
		}
		if !errors.Is(err, ErrInside) {
			t.Fatalf("err = %v, want ErrInside", err)
		}
		if _, err := os.Stat(filepath.Join(root, "deeper")); err == nil {
			t.Error("the parent directory was created inside the protected root")
		}
	})
	t.Run("every root is checked", func(t *testing.T) {
		other := filepath.Join(base, "other")
		if err := os.MkdirAll(other, 0o755); err != nil {
			t.Fatal(err)
		}
		f, _, err := Create(filepath.Join(other, "x.txt"), root, other)
		if err == nil {
			f.Close()
		}
		if !errors.Is(err, ErrInside) {
			t.Fatalf("err = %v, want ErrInside", err)
		}
	})
}

// I6b: a guard with nothing to guard is a caller bug (an unset data root), so
// it refuses instead of allowing every target.
func TestNoRootsFailsClosed(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "out", "x.txt")
	for name, roots := range map[string][]string{
		"no roots":           nil,
		"only an empty root": {""},
		"an empty root too":  {root, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if err := RefuseInside(target, roots...); !errors.Is(err, ErrNoRoots) {
				t.Errorf("RefuseInside with roots %q = %v, want ErrNoRoots", roots, err)
			}
			f, _, err := Create(target, roots...)
			if f != nil {
				f.Close()
			}
			if !errors.Is(err, ErrNoRoots) {
				t.Errorf("Create with roots %q = %v, want ErrNoRoots", roots, err)
			}
			if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Create with roots %q wrote %s: %v", roots, target, err)
			}
		})
	}
	// Known-good control: the same target with a real root is allowed.
	if err := RefuseInside(target, root); err != nil {
		t.Fatalf("control: RefuseInside with a real root = %v", err)
	}
}
