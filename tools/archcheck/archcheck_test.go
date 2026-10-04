// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package archcheck

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const mod = "example.com/root"

func pkg(path string, imports ...string) Package {
	return Package{ImportPath: path, Module: &Module{Path: mod}, Imports: imports}
}

func TestCoreViolations(t *testing.T) {
	pkgs := []Package{
		pkg(mod+"/internal/core", "fmt", "os", "path/filepath"),                                         // fine: os is allowed
		pkg(mod+"/internal/bad", "os/exec"),                                                             // OS-specific
		pkg(mod+"/internal/bad2", "golang.org/x/sys/unix"),                                              // OS-specific (prefix)
		pkg(mod+"/internal/bad3", mod+"/internal/connector/macos"),                                      // core importing a connector
		pkg(mod+"/internal/connector/macos", "os/exec"),                                                 // connectors are exempt
		pkg(mod+"/cmd/tool", "os/signal"),                                                               // commands are exempt
		pkg(mod+"/tools/x", "os/exec"),                                                                  // tools are exempt
		{ImportPath: mod + "/internal/t", Module: &Module{Path: mod}, TestImports: []string{"syscall"}}, // tests count too
		{ImportPath: "other.com/lib", Module: &Module{Path: "other.com"}, Imports: []string{"os/exec"}}, // other modules ignored
	}
	violators := map[string]bool{}
	for _, v := range CoreViolations(pkgs, mod) {
		violators[strings.SplitN(v, " imports ", 2)[0]] = true
	}
	want := []string{mod + "/internal/bad", mod + "/internal/bad2", mod + "/internal/bad3", mod + "/internal/t"}
	if len(violators) != len(want) {
		t.Errorf("violating packages %v, want exactly %v", violators, want)
	}
	for _, w := range want {
		if !violators[w] {
			t.Errorf("%s not reported", w)
		}
	}
}

func TestBoundaryViolations(t *testing.T) {
	pkgs := []Package{
		{ImportPath: mod + "/sub/x", Module: &Module{Path: mod + "/sub"}},
		{ImportPath: "fmt"},
		{ImportPath: mod + "/internal/core", Module: &Module{Path: mod}},
	}
	got := BoundaryViolations(pkgs, mod)
	if len(got) != 1 || !strings.Contains(got[0], mod+"/internal/core") {
		t.Fatalf("BoundaryViolations = %v", got)
	}
	if v := BoundaryViolations(pkgs[:2], mod); len(v) != 0 {
		t.Fatalf("clean dependency set reported %v", v)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// End to end against real `go list`: a nested module that imports its parent must be reported (the ADR 0031 known-bad).
func TestBoundaryEndToEnd(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.27\n")
	write(t, filepath.Join(root, "p", "p.go"), "package p\n\nconst X = 1\n")
	write(t, filepath.Join(root, "sub", "go.mod"), "module example.com/root/sub\n\ngo 1.27\n\nrequire example.com/root v0.0.0\n\nreplace example.com/root => ../\n")
	write(t, filepath.Join(root, "sub", "s.go"), "package sub\n\nimport \"example.com/root/p\"\n\nconst Y = p.X\n")

	forbidden, err := ModulePath(root)
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := List(filepath.Join(root, "sub"), []string{"GOFLAGS=-mod=mod"}, "-deps", "./...")
	if err != nil {
		t.Fatal(err)
	}
	if v := BoundaryViolations(pkgs, forbidden); len(v) == 0 {
		t.Fatal("a nested module importing its parent was not reported")
	}
}

// End to end: a core package that imports os/exec only on darwin must be reported when listed for darwin, which is why main.go lists every GOOS.
func TestCoreEndToEndPerGOOS(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.27\n")
	write(t, filepath.Join(root, "internal", "core", "core.go"), "package core\n\nconst X = 1\n")
	write(t, filepath.Join(root, "internal", "core", "core_darwin.go"), "package core\n\nimport _ \"os/exec\"\n")
	for goos, wantViolation := range map[string]bool{"linux": false, "darwin": true} {
		pkgs, err := List(root, []string{"GOOS=" + goos}, "./...")
		if err != nil {
			t.Fatal(err)
		}
		got := CoreViolations(pkgs, "example.com/root")
		if (len(got) > 0) != wantViolation {
			t.Errorf("GOOS=%s: violations %v, want violation=%v", goos, got, wantViolation)
		}
	}
}

// runArchcheck builds the command and runs it (`go run` collapses every nonzero child exit to 1, which would hide the exit-code contract). The test's working directory is tools/archcheck.
func runArchcheck(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "archcheck")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/archcheck").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out.String(), errb.String(), exitErr.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), 0
}

// -print-goos is the single home of the GOOS list; ci.yml's cross-build loop reads it from here. The test asserts shape and validity rather than repeating the list.
func TestPrintGOOS(t *testing.T) {
	out, _, code := runArchcheck(t, "-print-goos")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	known, err := exec.Command("go", "tool", "dist", "list").Output()
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]bool{}
	for _, osArch := range strings.Fields(string(known)) {
		valid[strings.SplitN(osArch, "/", 2)[0]] = true
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if out == "" || !strings.HasSuffix(out, "\n") {
		t.Fatalf("-print-goos output %q is empty or lacks a final newline", out)
	}
	seen := map[string]bool{}
	for _, goos := range lines {
		if !valid[goos] {
			t.Errorf("%q is not a GOOS known to `go tool dist list`", goos)
		}
		if seen[goos] {
			t.Errorf("%q printed twice", goos)
		}
		seen[goos] = true
	}
}

func TestUsageErrorExitsTwo(t *testing.T) {
	if _, _, code := runArchcheck(t, "-mode", "nonsense"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// The tests below go through the built binary so main.go's wiring (per-GOOS loop, flags, exit codes) is itself covered by a known-bad and a known-good.

func TestBinaryCoreFlagsDarwinOnlyImport(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.27\n")
	write(t, filepath.Join(root, "internal", "core", "core.go"), "package core\n\nconst X = 1\n")
	write(t, filepath.Join(root, "internal", "core", "core_darwin.go"), "package core\n\nimport _ \"os/exec\"\n")
	_, stderr, code := runArchcheck(t, "-mode", "core", "-dir", root)
	if code != 1 || !strings.Contains(stderr, "GOOS=darwin:") {
		t.Fatalf("exit %d, stderr %q; want exit 1 naming GOOS=darwin", code, stderr)
	}
}

func TestBinaryCoreCleanModulePasses(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.27\n")
	write(t, filepath.Join(root, "internal", "core", "core.go"), "package core\n\nconst X = 1\n")
	stdout, stderr, code := runArchcheck(t, "-mode", "core", "-dir", root)
	if code != 0 || !strings.Contains(stdout, "archcheck core: ok") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want a clean pass", code, stdout, stderr)
	}
}

// The parent is imported only from a _test.go, so only the -test flag in main.go makes the boundary check see it.
func TestBinaryBoundaryFlagsTestOnlyImport(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.27\n")
	write(t, filepath.Join(root, "p", "p.go"), "package p\n\nconst X = 1\n")
	write(t, filepath.Join(root, "sub", "go.mod"), "module example.com/root/sub\n\ngo 1.27\n\nrequire example.com/root v0.0.0\n\nreplace example.com/root => ../\n")
	write(t, filepath.Join(root, "sub", "s.go"), "package sub\n\nconst Y = 1\n")
	write(t, filepath.Join(root, "sub", "s_test.go"), "package sub\n\nimport (\n\t\"testing\"\n\n\t\"example.com/root/p\"\n)\n\nfunc TestX(t *testing.T) { _ = p.X }\n")
	t.Setenv("GOFLAGS", "-mod=mod")
	_, stderr, code := runArchcheck(t, "-mode", "boundary", "-dir", filepath.Join(root, "sub"), "-forbid", root)
	if code != 1 || !strings.Contains(stderr, "example.com/root/p") {
		t.Fatalf("exit %d, stderr %q; want exit 1 naming the test-only import", code, stderr)
	}
}

// A check that lists nothing in the module proves nothing, so it must not pass.
func TestBinaryCoreVacuousPassIsUsageError(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.27\n")
	// Every file is excluded by a build constraint, leaving no package in the module for any GOOS.
	write(t, filepath.Join(root, "internal", "core", "core.go"), "//go:build ignore\n\npackage core\n\nimport _ \"os/exec\"\n")
	_, stderr, code := runArchcheck(t, "-mode", "core", "-dir", root)
	if code != 2 || !strings.Contains(stderr, "no package") {
		t.Fatalf("exit %d, stderr %q; want exit 2 explaining no packages were checked", code, stderr)
	}
}

func TestBinaryBoundaryEmptyListIsUsageError(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.27\n")
	write(t, filepath.Join(root, "sub", "go.mod"), "module example.com/root/sub\n\ngo 1.27\n")
	_, stderr, code := runArchcheck(t, "-mode", "boundary", "-dir", filepath.Join(root, "sub"), "-forbid", root)
	if code != 2 {
		t.Fatalf("exit %d, stderr %q; want exit 2 for an empty package list", code, stderr)
	}
}

func TestModulePathStripsTrailingComment(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/root // the root\n\ngo 1.27\n")
	got, err := ModulePath(dir)
	if err != nil || got != "example.com/root" {
		t.Fatalf("ModulePath = %q, %v; want example.com/root", got, err)
	}
}
