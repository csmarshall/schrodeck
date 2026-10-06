// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmarshall/schrodeck/internal/cli"
)

// buildBinary compiles the real entry point so the tests below pin cmd/schrodeck/main.go's wiring, not just the in-process logger.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "schrodeck")
	build := exec.Command("go", "build", "-o", bin, "github.com/csmarshall/schrodeck/cmd/schrodeck")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func runBinary(t *testing.T, bin, logLevel string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "SCHRODECK_LOG_LEVEL="+logLevel)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		t.Fatalf("running binary: %v", err)
	}
	return out.String(), errb.String(), code
}

func TestBinaryDebugLogsStayOffStdout(t *testing.T) {
	bin := buildBinary(t)
	// version, not doctor: doctor would read this computer's real Stream Deck
	// install, which tests never do by default.
	stdout, stderr, code := runBinary(t, bin, "debug", "version", "--json")
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n"); len(lines) != 1 || !strings.HasPrefix(lines[0], `{"schema_version":1,`) {
		t.Fatalf("stdout is not exactly one contract E line: %q", stdout)
	}
	if !strings.Contains(stderr, "level=DEBUG") {
		t.Fatalf("debug level was not honored; stderr %q", stderr)
	}
}

func TestBinaryRejectsInvalidLogLevel(t *testing.T) {
	bin := buildBinary(t)
	stdout, stderr, code := runBinary(t, bin, "loud", "version")
	if code != cli.ExitUsage {
		t.Fatalf("exit %d, want %d", code, cli.ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty, got %q", stdout)
	}
	if !strings.Contains(stderr, "SCHRODECK_LOG_LEVEL") {
		t.Fatalf("stderr lacks an explanation: %q", stderr)
	}
}
