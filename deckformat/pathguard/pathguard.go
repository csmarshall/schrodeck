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

// ErrNotName means a name that must be a single path element is not one.
var ErrNotName = errors.New("pathguard: not a single path element")

// RefuseInside returns ErrInside if target is, or would be created, inside any
// of roots. Both sides go through Resolve, which follows the path the way the
// kernel does (symlinks and "../" segments in order, the missing tail appended),
// so neither a symlinked output directory nor a "../" after a symlink can reach
// a root. The comparison ignores letter case: macOS and Windows file systems
// usually do, and refusing a few more paths than necessary is the safe
// direction.
func RefuseInside(target string, roots ...string) error {
	t, err := Resolve(target)
	if err != nil {
		return err
	}
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
	}
	return nil
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
// appended as written. A dangling symlink is an error: a write through it would
// create its target, and Resolve cannot say where that is. Callers that write
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
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, comp)
		if !missing {
			if _, err := os.Lstat(next); err == nil {
				real, err := filepath.EvalSymlinks(next)
				if err != nil {
					return "", fmt.Errorf("pathguard: %s: %w", next, err)
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
