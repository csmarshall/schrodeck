// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package cli implements schrodeck's command line. Every command can print a JSON document instead of text (--json, after the command name); that document is contract E (docs/contracts/cli-json.md) and is what any UI consumes. Logs never go to stdout.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
)

// Exit codes.
const (
	ExitOK    = 0 // success
	ExitFail  = 1 // the command ran and reports failure
	ExitUsage = 2 // the command line was wrong
)

// Env is everything a command may touch. Tests build one with buffers.
type Env struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
	Logger  *slog.Logger
	Checks  []Check
}

// result is what a command produced.
type result struct {
	data   any    // the envelope's "data" member
	text   string // human-readable output
	failed bool   // ran, but reports failure: "ok": false and exit 1
}

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, env Env, args []string) (result, error)
}

// usageError marks a wrong command line (exit 2) as opposed to a failed run.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// Run executes one command line and returns the process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 {
		printUsage(env.Stderr)
		return ExitUsage
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage(env.Stderr)
		return ExitOK
	}
	cmd, ok := lookup(args[0])
	if !ok {
		fmt.Fprintf(env.Stderr, "schrodeck: unknown command %q\n\n", args[0])
		printUsage(env.Stderr)
		return ExitUsage
	}

	fs := flag.NewFlagSet("schrodeck "+cmd.name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "print one JSON document (contract E) instead of text")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}

	logger := env.Logger.With("component", "cli", "command", cmd.name)
	logger.Debug("command started")
	res, err := cmd.run(ctx, env, fs.Args())
	if err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(env.Stderr, "schrodeck %s: %v\n", cmd.name, err)
			return ExitUsage
		}
		logger.Error("command failed", "error", err)
		if *asJSON {
			if werr := writeJSON(env.Stdout, envelope{SchemaVersion: SchemaVersion, Command: cmd.name, OK: false, Error: &errorBody{Message: err.Error()}}); werr != nil {
				logger.Error("writing JSON output failed", "error", werr)
			}
		} else {
			fmt.Fprintf(env.Stderr, "schrodeck %s: %v\n", cmd.name, err)
		}
		return ExitFail
	}

	if *asJSON {
		if err := writeJSON(env.Stdout, envelope{SchemaVersion: SchemaVersion, Command: cmd.name, OK: !res.failed, Data: res.data}); err != nil {
			logger.Error("writing JSON output failed", "error", err)
			return ExitFail
		}
	} else {
		fmt.Fprint(env.Stdout, res.text)
	}
	logger.Debug("command finished", "ok", !res.failed)
	if res.failed {
		return ExitFail
	}
	return ExitOK
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: schrodeck <command> [--json] [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
}

func lookup(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}
