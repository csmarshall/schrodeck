// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package logging configures schrodeck's structured logs: every record has a UTC timestamp, a level and context attributes, and the level is set by the SCHRODECK_LOG_LEVEL environment variable.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// EnvLevel is the environment variable that sets the minimum log level.
const EnvLevel = "SCHRODECK_LOG_LEVEL"

// DefaultLevel applies when EnvLevel is unset or empty.
const DefaultLevel = slog.LevelInfo

// ParseLevel turns the text of EnvLevel into a level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return DefaultLevel, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("%s=%q: want debug, info, warn or error", EnvLevel, s)
}

// New returns a logger writing logfmt-style text records to w.
func New(w io.Writer, levelText string) (*slog.Logger, error) {
	level, err := ParseLevel(levelText)
	if err != nil {
		return nil, err
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: utcTime})
	return slog.New(h), nil
}

// utcTime renders the record time in UTC so logs from different Macs line up.
func utcTime(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey && len(groups) == 0 {
		a.Value = slog.StringValue(a.Value.Time().UTC().Format(time.RFC3339Nano))
	}
	return a
}
