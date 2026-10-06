// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package cli implements schrodeck's command line. Every command can print a
// JSON document instead of text (--json, anywhere after the command name);
// that document is contract E (docs/contracts/cli-json.md) and is what any UI
// consumes. Logs never go to stdout.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/csmarshall/schrodeck/internal/host"
)

// Exit codes.
const (
	ExitOK    = 0 // success
	ExitFail  = 1 // the command ran and reports failure
	ExitUsage = 2 // the command line was wrong
)

// Env is everything a command may touch. Tests build one with buffers and
// fakes.
type Env struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
	Logger  *slog.Logger
	// Host is nil when this OS has no connector; commands that need it say so.
	Host *host.Host
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

// errHelp is returned by parseFlags when -h was given.
var errHelp = errors.New("help requested")

// errNoHost is returned by commands that need a connector on an OS without one.
var errNoHost = errors.New("this command needs an OS connector, and there is none for this OS yet")

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
	rest, asJSON := extractJSON(args[1:])

	logger := env.Logger.With("component", "cli", "command", cmd.name)
	logger.Debug("command started")
	res, err := cmd.run(ctx, env, rest)
	if errors.Is(err, errHelp) {
		return ExitOK
	}
	if err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(env.Stderr, "schrodeck %s: %v\n", cmd.name, err)
			return ExitUsage
		}
		logger.Error("command failed", "error", err)
		if asJSON {
			if werr := writeJSON(env.Stdout, envelope{SchemaVersion: SchemaVersion, Command: cmd.name, OK: false, Error: &errorBody{Message: err.Error()}}); werr != nil {
				logger.Error("writing JSON output failed", "error", werr)
			}
		} else {
			fmt.Fprintf(env.Stderr, "schrodeck %s: %v\n", cmd.name, err)
		}
		return ExitFail
	}

	if asJSON {
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

// extractJSON removes the --json flag from anywhere in args.
func extractJSON(args []string) ([]string, bool) {
	var rest []string
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json", "-json", "--json=true", "-json=true":
			asJSON = true
		default:
			rest = append(rest, a)
		}
	}
	return rest, asJSON
}

// parseFlags parses fs from args, allowing flags after positional
// arguments, and returns the positional arguments. Errors are usage errors.
func parseFlags(fs *flag.FlagSet, env Env, args []string) ([]string, error) {
	fs.SetOutput(env.Stderr)
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, errHelp
			}
			return nil, usageError{err.Error()}
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: schrodeck <command> [arguments] [--json]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "inventory and doctor print this computer's real profile folder ids. They are")
	fmt.Fprintln(w, "fine to read on your screen; do not paste them into a repository or an issue.")
	fmt.Fprintln(w, "observe reports and fixture exports are redacted, but review them before committing.")
}

func lookup(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func commands() []command {
	return []command{
		{name: "version", summary: "print the schrodeck version", run: runVersion},
		{name: "status", summary: "show what schrodeck sees on this computer (read-only)", run: runStatus},
		{name: "inventory", summary: "list decks and profiles with their hashes (read-only)", run: runInventory},
		{name: "doctor", summary: "run read-only checks of the app and its file format", run: runDoctor},
		{name: "observe", summary: "observe what one action in the app changes on disk: start <name> | stop <name>", run: runObserve},
		{name: "fixture", summary: "export a redacted test fixture: export --profile <folder> --out <dir>", run: runFixture},
	}
}
