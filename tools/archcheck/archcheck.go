// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package archcheck enforces two import rules:
//   - the core (every package of the root module except cmd/, tools/ and internal/connector/) imports nothing OS-specific and no connector, command or tool package (ADR 0018);
//   - deckformat depends on no package of the schrodeck module (ADR 0031).
//
// Known limit: only GOOS varies between listings (not GOARCH or build tags), and cgo files are only seen where CGO_ENABLED=1.
package archcheck

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Module is the part of `go list -json`'s Module we need.
type Module struct{ Path string }

// Package is the part of `go list -json` output we need.
type Package struct {
	ImportPath   string
	Module       *Module
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// OSPackages may not be imported by core packages. A path matches an entry exactly or as a prefix followed by "/". "C" is cgo.
var OSPackages = []string{"os/exec", "os/signal", "os/user", "syscall", "golang.org/x/sys", "C"}

// exemptDirs are the root-module subtrees allowed to touch the OS.
var exemptDirs = []string{"/cmd/", "/tools/", "/internal/connector/"}

func matches(path, entry string) bool { return path == entry || strings.HasPrefix(path, entry+"/") }

func exempt(module, importPath string) bool {
	for _, d := range exemptDirs {
		if strings.HasPrefix(importPath+"/", module+d) {
			return true
		}
	}
	return false
}

// CoreViolations lists every forbidden import made by a core package of module.
func CoreViolations(pkgs []Package, module string) []string {
	var out []string
	for _, p := range pkgs {
		if p.Module == nil || p.Module.Path != module || exempt(module, p.ImportPath) {
			continue
		}
		all := append(append(append([]string{}, p.Imports...), p.TestImports...), p.XTestImports...)
		for _, imp := range all {
			bad := false
			for _, d := range OSPackages {
				if matches(imp, d) {
					bad = true
				}
			}
			if strings.HasPrefix(imp, module+"/") && exempt(module, imp) {
				bad = true
			}
			if bad {
				out = append(out, fmt.Sprintf("%s imports %s", p.ImportPath, imp))
			}
		}
	}
	sort.Strings(out)
	return dedupe(out)
}

// BoundaryViolations lists every package in a dependency closure that belongs to the forbidden module.
func BoundaryViolations(pkgs []Package, forbiddenModule string) []string {
	var out []string
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Path == forbiddenModule {
			out = append(out, fmt.Sprintf("depends on %s (module %s)", p.ImportPath, forbiddenModule))
		}
	}
	sort.Strings(out)
	return out
}

func dedupe(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// ModulePath reads the module path from dir/go.mod, ignoring a trailing comment.
func ModulePath(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			rest, _, _ = strings.Cut(rest, "//")
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", fmt.Errorf("%s/go.mod has no module line", dir)
}

// List runs `go list -json <args>` in dir with extra environment entries.
func List(dir string, env []string, args ...string) ([]Package, error) {
	cmd := exec.Command("go", append([]string{"list", "-json"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %v: %s", dir, err, stderr.String())
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []Package
	for {
		var p Package
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}
