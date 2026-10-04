// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files from current output")

func testEnv(logTo io.Writer, checks ...Check) (Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	if logTo == nil {
		logTo = io.Discard
	}
	logger := slog.New(slog.NewTextHandler(logTo, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return Env{Stdout: &out, Stderr: &errb, Version: "test", Logger: logger, Checks: checks}, &out, &errb
}

func fixedCheck(id, status, detail string) Check {
	return Check{ID: id, Run: func(context.Context) CheckResult {
		return CheckResult{ID: id, Status: status, Detail: detail}
	}}
}

// compareGolden checks got against testdata/golden/<name>.json, rewriting it under -update.
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("stdout\n%s\nwant (golden %s)\n%s", got, path, want)
	}
}

func TestGoldenJSON(t *testing.T) {
	cases := []struct {
		golden   string
		args     []string
		checks   []Check
		wantCode int
	}{
		{"version", []string{"version", "--json"}, nil, ExitOK},
		{"status", []string{"status", "--json"}, nil, ExitOK},
		{"doctor-empty", []string{"doctor", "--json"}, nil, ExitOK},
		{"doctor-fail", []string{"doctor", "--json"},
			[]Check{fixedCheck("A1", StatusPass, ""), fixedCheck("A2", StatusFail, "broken on purpose")}, ExitFail},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			env, out, errb := testEnv(nil, c.checks...)
			code := Run(context.Background(), c.args, env)
			if code != c.wantCode {
				t.Fatalf("exit %d, want %d; stderr %q", code, c.wantCode, errb.String())
			}
			compareGolden(t, c.golden, out.Bytes())
		})
	}
}

// No M0 command can fail to run, so the error envelope is driven through the writer directly. The message carries characters HTML escaping would mangle.
func TestGoldenErrorEnvelope(t *testing.T) {
	var out bytes.Buffer
	doc := envelope{SchemaVersion: SchemaVersion, Command: "doctor", OK: false, Error: &errorBody{Message: "could not run <checks> & more"}}
	if err := writeJSON(&out, doc); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "error", out.Bytes())
}

func TestJSONStdoutIsOneLineEvenWithDebugLogs(t *testing.T) {
	for _, checks := range [][]Check{nil, {fixedCheck("A2", StatusFail, "x")}} {
		var logs bytes.Buffer
		env, out, _ := testEnv(&logs, checks...)
		Run(context.Background(), []string{"doctor", "--json"}, env)
		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(lines) != 1 {
			t.Fatalf("stdout has %d lines, want 1: %q", len(lines), out.String())
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &doc); err != nil {
			t.Fatalf("stdout is not one JSON document: %v", err)
		}
		if logs.Len() == 0 {
			t.Fatalf("debug logs were expected on the log writer; the test would not detect logs leaking to stdout otherwise")
		}
	}
}

func TestUsageErrors(t *testing.T) {
	cases := [][]string{
		nil,
		{"nope"},
		{"--json", "status"},
		{"status", "--bogus"},
		{"status", "extra-arg"},
	}
	for _, args := range cases {
		env, out, errb := testEnv(nil)
		code := Run(context.Background(), args, env)
		if code != ExitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, ExitUsage)
		}
		if out.Len() != 0 {
			t.Errorf("%q: usage errors must not print to stdout, got %q", args, out.String())
		}
		if errb.Len() == 0 {
			t.Errorf("%q: usage error printed no message", args)
		}
	}
}

func TestHelpExitsZero(t *testing.T) {
	env, _, errb := testEnv(nil)
	if code := Run(context.Background(), []string{"help"}, env); code != ExitOK {
		t.Fatalf("help: exit %d", code)
	}
	for _, name := range []string{"version", "status", "doctor"} {
		if !strings.Contains(errb.String(), name) {
			t.Errorf("help text lacks command %q", name)
		}
	}
}

func TestTextOutput(t *testing.T) {
	env, out, _ := testEnv(nil)
	if code := Run(context.Background(), []string{"status"}, env); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out.String(), "schrodeck test") {
		t.Fatalf("status text = %q", out.String())
	}
}

// Contract E's documented schema_version must equal the code's. The doc is prose, so it can't derive the value; this test is the next best thing.
func TestSchemaVersionDocumented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "cli-json.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("Current `schema_version`: **%d**", SchemaVersion)
	if !strings.Contains(string(doc), want) {
		t.Fatalf("docs/contracts/cli-json.md does not contain %q", want)
	}
}
