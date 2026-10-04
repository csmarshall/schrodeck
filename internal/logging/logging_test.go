// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package logging

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"", slog.LevelInfo, false},
		{"debug", slog.LevelDebug, false},
		{"INFO", slog.LevelInfo, false},
		{" warn ", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"verbose", 0, true},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseLevel(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseLevelErrorNamesTheVariable(t *testing.T) {
	_, err := ParseLevel("loud")
	if err == nil || !strings.Contains(err.Error(), EnvLevel) {
		t.Fatalf("error %v should name %s so the user knows what to fix", err, EnvLevel)
	}
}

func TestNewFiltersBelowLevelAndKeepsContext(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(&buf, "warn")
	if err != nil {
		t.Fatal(err)
	}
	log.Info("hidden")
	log.Warn("shown", "component", "test")
	out := buf.String()
	if strings.Contains(out, "hidden") {
		t.Errorf("info record logged at warn level: %q", out)
	}
	for _, want := range []string{"level=WARN", "msg=shown", "component=test", "time="} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q lacks %q", out, want)
		}
	}
}

func TestTimestampIsUTCRFC3339(t *testing.T) {
	// Pin a non-UTC local zone so the test cannot pass merely because the host (a CI runner) is already on UTC.
	saved := time.Local
	time.Local = time.FixedZone("test-zone", -5*3600)
	t.Cleanup(func() { time.Local = saved })
	var buf bytes.Buffer
	log, err := New(&buf, "info")
	if err != nil {
		t.Fatal(err)
	}
	log.Info("x")
	if !regexp.MustCompile(`^time=\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z `).MatchString(buf.String()) {
		t.Fatalf("timestamp is not UTC RFC 3339: %q", buf.String())
	}
}

func TestDebugLevelEmitsDebugRecords(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(&buf, "debug")
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("detail", "component", "test")
	if !strings.Contains(buf.String(), "level=DEBUG") || !strings.Contains(buf.String(), "msg=detail") {
		t.Errorf("debug record not emitted at debug level: %q", buf.String())
	}
}
