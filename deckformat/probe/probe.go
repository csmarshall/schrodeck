// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package probe runs the assertions of contracts B and C against a real
// install (ADR 0031's probe runner; `schrodeck doctor` is built on it). Each
// probe has a tier: read-only probes never write anything; restart-tier
// probes restart the app and arrive with M3.
package probe

import "context"

// Tier says what a probe may do.
type Tier int

// Tiers.
const (
	ReadOnly Tier = iota // reads files and prefs only
	Restart              // quits and relaunches the app (M3)
)

func (t Tier) String() string {
	if t == Restart {
		return "restart"
	}
	return "read-only"
}

// Status of one probe run.
type Status string

// Statuses. Info means "nothing is wrong, but here is something to know"
// (e.g. which buttons reference another profile).
const (
	Pass Status = "pass"
	Fail Status = "fail"
	Skip Status = "skip"
	Info Status = "info"
)

// Result is one probe's outcome.
type Result struct {
	ID       string   `json:"id"`
	Contract string   `json:"contract"`
	Tier     string   `json:"tier"`
	Status   Status   `json:"status"`
	Detail   string   `json:"detail,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

// Probe is one assertion check. Run returns status, a one-line detail and
// optional evidence lines.
type Probe struct {
	ID       string
	Contract string // "B" or "C"
	Tier     Tier
	Run      func(ctx context.Context) (Status, string, []string)
}

// RunAll runs every probe whose tier is at most max, in order; the others are
// reported as skipped.
func RunAll(ctx context.Context, probes []Probe, max Tier) []Result {
	out := make([]Result, 0, len(probes))
	for _, p := range probes {
		r := Result{ID: p.ID, Contract: p.Contract, Tier: p.Tier.String()}
		if p.Tier > max {
			r.Status, r.Detail = Skip, "needs the "+p.Tier.String()+" tier"
		} else {
			r.Status, r.Detail, r.Evidence = p.Run(ctx)
		}
		out = append(out, r)
	}
	return out
}

// Failed reports whether any result failed.
func Failed(rs []Result) bool {
	for _, r := range rs {
		if r.Status == Fail {
			return true
		}
	}
	return false
}
