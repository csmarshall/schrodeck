// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package pathguard keeps writes out of protected directories, such as the
// Stream Deck app's data root. Every command that writes to a path a person
// typed checks the final target here first, and Create is the writer that does
// the check and the open together.
//
// Limits. Windows is compiled but untested, and M1 is macOS-only: SingleName and
// Resolve do not handle NTFS alternate data streams, reserved device names
// (CON, NUL, ...) or drive-relative paths (C:foo). On Linux the alias tests
// skip, because the file system does not alias those spellings, and the string
// fold layer over-refuses there (a name that only folds equal is refused although
// the directories differ), which is the safe direction. One race no path check
// closes: someone with write access renaming an ancestor of the target between
// the check and the open; the re-check after the open narrows it.
package pathguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// ErrInside means a write target lies inside a protected root.
var ErrInside = errors.New("pathguard: target is inside a protected directory")

// ErrUnresolvable means the path cannot be located without guessing (a dangling
// symlink, or a ".." after a component that does not exist yet), so it is
// refused rather than allowed.
var ErrUnresolvable = errors.New("pathguard: path cannot be resolved")

// ErrNoRoots means a guard was asked to protect nothing: no root, or an empty
// one (typically a data-root setting that was never filled in). It refuses
// rather than allowing every target.
var ErrNoRoots = errors.New("pathguard: no protected directory given")

// ErrNotName means a name that must be a single path element is not one.
var ErrNotName = errors.New("pathguard: not a single path element")

// RefuseInside returns ErrInside if target is, or would be created, inside any
// of roots. The path is first resolved the way the kernel does (see Resolve),
// then inside-ness is decided by file IDENTITY: every existing ancestor of the
// target, from the deepest one up to the top, is compared with each root using
// os.SameFile (device and inode). Identity is the authority because spelling is
// not: firmlinks (/System/Volumes/Data/...), case folding that strings.ToLower
// does not reproduce, and Unicode normalization all name one directory in
// several ways. A cheap string comparison under fold runs first and can
// only add refusals, never remove them. A root that does not exist yet is
// compared through its deepest existing ancestor (identity) plus its missing
// tail (NFC and case folding), since a write would create it.
//
// With no roots, or with any empty root, it returns ErrNoRoots (fail closed).
func RefuseInside(target string, roots ...string) error {
	if err := checkRoots(roots); err != nil {
		return err
	}
	t, err := Resolve(target)
	if err != nil {
		return err
	}
	var chain []ancestor // created lazily: only needed when a root is checked by identity
	for _, root := range roots {
		r, err := Resolve(root)
		if err != nil {
			return err
		}
		inside := fmt.Errorf("%w: %s is inside %s", ErrInside, target, root)
		if within(fold(r), fold(t)) {
			return inside
		}
		if chain == nil {
			if chain, err = ancestors(t); err != nil {
				return err
			}
		}
		ri, err := os.Stat(r)
		if err == nil {
			for _, a := range chain {
				if os.SameFile(a.info, ri) {
					return inside
				}
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// The root does not exist yet, so a write could create it. Find its
		// deepest existing ancestor and see whether the target passes through
		// the same directory (by identity) and continues with the root's missing
		// tail, spelled any way that folds to the same name.
		rootChain, err := ancestors(r)
		if err != nil {
			return err
		}
		if len(rootChain) == 0 {
			continue
		}
		rootAnc := rootChain[0]
		rootTail, ok := tailAfter(rootAnc.path, r)
		if !ok {
			continue
		}
		for _, a := range chain {
			if !os.SameFile(a.info, rootAnc.info) {
				continue
			}
			if tt, ok := tailAfter(a.path, t); ok && hasFoldedPrefix(tt, rootTail) {
				return inside
			}
		}
	}
	return nil
}

// checkRoots refuses a guard that would protect nothing.
func checkRoots(roots []string) error {
	if len(roots) == 0 {
		return ErrNoRoots
	}
	for i, r := range roots {
		if r == "" {
			return fmt.Errorf("%w: root %d of %d is empty", ErrNoRoots, i+1, len(roots))
		}
	}
	return nil
}

// ancestor is an existing directory on the way from a path up to the top.
type ancestor struct {
	path string
	info os.FileInfo
}

// ancestors returns the identity of every existing directory on the way from p
// up to the top, p itself included when it exists, deepest first.
func ancestors(p string) ([]ancestor, error) {
	var out []ancestor
	for cur := p; ; {
		fi, err := os.Stat(cur)
		switch {
		case err == nil:
			out = append(out, ancestor{cur, fi})
		case errors.Is(err, fs.ErrNotExist):
		default:
			return nil, err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return out, nil
		}
		cur = parent
	}
}

// tailAfter returns the path elements of full below prefix.
func tailAfter(prefix, full string) ([]string, bool) {
	rel, err := filepath.Rel(prefix, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, false
	}
	if rel == "." {
		return nil, true
	}
	return strings.Split(rel, string(filepath.Separator)), true
}

// fold is the one definition of "the same name" used by the string checks: NFC
// normalization, then FULL Unicode case folding (sharp s becomes "ss", the fi
// ligature "fi"), which is what APFS does. Simple folding (strings.EqualFold,
// strings.ToLower) misses those.
func fold(s string) string { return cases.Fold().String(norm.NFC.String(s)) }

// hasFoldedPrefix reports whether the elements of prefix are the leading
// elements of path under fold.
func hasFoldedPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, p := range prefix {
		if fold(path[i]) != fold(p) {
			return false
		}
	}
	return true
}

// SingleName returns ErrNotName unless name is one path element: not empty,
// not "." or "..", and without a separator.
func SingleName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: %q", ErrNotName, name)
	}
	return nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// Resolve returns the absolute path the kernel would reach for p: components
// are followed left to right, a symlink is replaced by its target at the moment
// it is met, and ".." removes the previous PHYSICAL component. Lexically
// cleaning first would be wrong, because "link/../x" is the parent of link's
// target, not the parent of link. The part of p that does not exist yet is
// appended as written. Two things are ErrUnresolvable: a dangling symlink (a
// write through it would create its target, and Resolve cannot say where that
// is) and a ".." after a component that does not exist yet (the kernel would
// resume following real symlinks after it, so a lexical guess can be wrong). Callers that write
// must write to the returned path, not to p, so the check and the write cannot
// disagree.
func Resolve(p string) (string, error) {
	if p == "" {
		return "", errors.New("pathguard: empty path")
	}
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		p = wd + string(filepath.Separator) + p
	}
	vol := filepath.VolumeName(p)
	rest := p[len(vol):]
	cur := vol + string(filepath.Separator)
	missing := false // once a component is missing, everything after it is created fresh
	for _, comp := range strings.FieldsFunc(rest, func(r rune) bool { return r < 128 && os.IsPathSeparator(uint8(r)) }) {
		switch comp {
		case ".":
			continue
		case "..":
			if missing {
				return "", fmt.Errorf("%w: %s: \"..\" after a component that does not exist", ErrUnresolvable, p)
			}
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, comp)
		if !missing {
			if _, err := os.Lstat(next); err == nil {
				real, err := filepath.EvalSymlinks(next)
				if err != nil {
					return "", fmt.Errorf("%w: %s: %w", ErrUnresolvable, next, err)
				}
				cur = real
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return "", err
			}
			missing = true
		}
		cur = next
	}
	return cur, nil
}

// Create opens a new file at target for writing, refusing anything inside the
// protected roots, and returns the file with the resolved path it was created
// at. It is the one writer for a path a person typed: the target is resolved
// the way the kernel will (Resolve), checked (RefuseInside), its parent
// directories are created, and the file is opened O_WRONLY|O_CREATE|O_EXCL, so
// an existing file is never opened. That also covers a hard link: a name that
// is another name for a protected file exists, so it is refused rather than
// truncated. After the open the file is compared with the path (os.SameFile)
// and the parent directory is checked against the roots again; on a mismatch
// the file is closed and an error returned. It is not removed, because by then
// the path may no longer name it.
func Create(target string, roots ...string) (*os.File, string, error) {
	if err := checkRoots(roots); err != nil {
		return nil, "", err
	}
	resolved, err := Resolve(target)
	if err != nil {
		return nil, "", err
	}
	if err := RefuseInside(resolved, roots...); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(resolved, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, "", err
	}
	opened, err := f.Stat()
	if err == nil {
		var onDisk os.FileInfo
		if onDisk, err = os.Stat(resolved); err == nil && !os.SameFile(opened, onDisk) {
			err = fmt.Errorf("pathguard: %s changed while it was being created", resolved)
		}
	}
	if err == nil {
		err = RefuseInside(filepath.Dir(resolved), roots...)
	}
	if err != nil {
		f.Close()
		return nil, "", err
	}
	return f, resolved, nil
}
