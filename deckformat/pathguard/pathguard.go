// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package pathguard keeps writes out of protected directories, such as the
// Stream Deck app's data root. Every command that writes to a path a person
// typed checks the final target here first.
package pathguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrInside means a write target lies inside a protected root.
var ErrInside = errors.New("pathguard: target is inside a protected directory")

// ErrUnresolvable means the path cannot be located without guessing (a dangling
// symlink, or a ".." after a component that does not exist yet), so it is
// refused rather than allowed.
var ErrUnresolvable = errors.New("pathguard: path cannot be resolved")

// ErrNotName means a name that must be a single path element is not one.
var ErrNotName = errors.New("pathguard: not a single path element")

// RefuseInside returns ErrInside if target is, or would be created, inside any
// of roots. The path is first resolved the way the kernel does (see Resolve),
// then inside-ness is decided by file IDENTITY: every existing ancestor of the
// target, from the deepest one up to the top, is compared with each root using
// os.SameFile (device and inode). Identity is the authority because spelling is
// not: firmlinks (/System/Volumes/Data/...), case folding that strings.ToLower
// does not reproduce, and Unicode normalization all name one directory in
// several ways. A cheap case-insensitive string comparison runs first and can
// only add refusals, never remove them. A root that does not exist yet is
// compared by string only, since nothing can alias a directory that is absent.
func RefuseInside(target string, roots ...string) error {
	t, err := Resolve(target)
	if err != nil {
		return err
	}
	var chain []os.FileInfo // created lazily: only needed when a root exists
	for _, root := range roots {
		if root == "" {
			continue
		}
		r, err := Resolve(root)
		if err != nil {
			return err
		}
		if within(strings.ToLower(r), strings.ToLower(t)) {
			return fmt.Errorf("%w: %s is inside %s", ErrInside, target, root)
		}
		ri, err := os.Stat(r)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if chain == nil {
			if chain, err = ancestors(t); err != nil {
				return err
			}
		}
		for _, a := range chain {
			if os.SameFile(a, ri) {
				return fmt.Errorf("%w: %s is inside %s", ErrInside, target, root)
			}
		}
	}
	return nil
}

// ancestors returns the identity of every existing directory on the way from p
// up to the top, p itself included when it exists.
func ancestors(p string) ([]os.FileInfo, error) {
	var out []os.FileInfo
	for cur := p; ; {
		fi, err := os.Stat(cur)
		switch {
		case err == nil:
			out = append(out, fi)
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
