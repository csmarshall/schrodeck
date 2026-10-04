// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package cli

import (
	"context"
	"fmt"
	"strings"
)

// Check statuses reported by doctor.
const (
	StatusPass = "pass"
	StatusFail = "fail"
	StatusSkip = "skip"
)

// Check is one doctor check. M0 ships the wiring and no checks; M1 replaces this with the read-only probes of contracts B and C.
type Check struct {
	ID  string
	Run func(ctx context.Context) CheckResult
}

// CheckResult is one line of doctor's report.
type CheckResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func commands() []command {
	return []command{
		{name: "version", summary: "print the schrodeck version", run: runVersion},
		{name: "status", summary: "show what schrodeck manages on this computer (read-only)", run: runStatus},
		{name: "doctor", summary: "run read-only checks and report problems", run: runDoctor},
	}
}

type versionData struct {
	SchrodeckVersion string `json:"schrodeck_version"`
}

func noArgs(name string, args []string) error {
	if len(args) > 0 {
		return usageError{fmt.Sprintf("%s takes no arguments, got %q", name, args)}
	}
	return nil
}

func runVersion(_ context.Context, env Env, args []string) (result, error) {
	if err := noArgs("version", args); err != nil {
		return result{}, err
	}
	return result{data: versionData{SchrodeckVersion: env.Version}, text: fmt.Sprintf("schrodeck %s\n", env.Version)}, nil
}

func runStatus(_ context.Context, env Env, args []string) (result, error) {
	if err := noArgs("status", args); err != nil {
		return result{}, err
	}
	text := fmt.Sprintf("schrodeck %s\nNo setups yet: syncing arrives in a later milestone.\n", env.Version)
	return result{data: versionData{SchrodeckVersion: env.Version}, text: text}, nil
}

type doctorData struct {
	Checks []CheckResult `json:"checks"`
}

func runDoctor(ctx context.Context, env Env, args []string) (result, error) {
	if err := noArgs("doctor", args); err != nil {
		return result{}, err
	}
	data := doctorData{Checks: []CheckResult{}}
	failed := false
	var text strings.Builder
	for _, c := range env.Checks {
		r := c.Run(ctx)
		r.ID = c.ID
		data.Checks = append(data.Checks, r)
		if r.Status == StatusFail {
			failed = true
		}
		fmt.Fprintf(&text, "%-5s %s %s\n", r.Status, r.ID, r.Detail)
	}
	if len(env.Checks) == 0 {
		text.WriteString("No checks in this version.\n")
	}
	return result{data: data, text: text.String(), failed: failed}, nil
}
