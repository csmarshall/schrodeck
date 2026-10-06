// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/normhash"
	"github.com/csmarshall/schrodeck/deckformat/probe"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/deckformat/redact"
	"github.com/csmarshall/schrodeck/internal/decks"
	"github.com/csmarshall/schrodeck/internal/doctor"
	"github.com/csmarshall/schrodeck/internal/host"
	"github.com/csmarshall/schrodeck/internal/identity"
	"github.com/csmarshall/schrodeck/internal/ports"
)

func noArgs(name string, fs *flag.FlagSet, env Env, args []string) error {
	rest, err := parseFlags(fs, env, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageError{fmt.Sprintf("%s takes no arguments, got %q", name, rest)}
	}
	return nil
}

func newFlags(name string) *flag.FlagSet {
	return flag.NewFlagSet("schrodeck "+name, flag.ContinueOnError)
}

// loadProfiles reads ProfilesV3 through a read-only fs.FS.
func loadProfiles(h *host.Host) (profile.LoadResult, error) {
	return profile.LoadAll(os.DirFS(h.Paths.ProfilesDir()))
}

// listDecks returns the host's decks and, when the connector can report them
// (host.DeckLoader), the profile folders that failed to load during the same
// read, so a deck whose profile did not load is not mistaken for an unbound one.
func listDecks(h *host.Host) ([]ports.Deck, []error, error) {
	if dl, ok := h.Decks.(host.DeckLoader); ok {
		return dl.DecksWithLoadErrors()
	}
	ds, err := h.Decks.Decks()
	return ds, nil, err
}

// loadErrorKey identifies a load error by the folder it names, or by its
// message when it names none.
func loadErrorKey(e error) string {
	var fe *profile.FolderError
	if errors.As(e, &fe) {
		return "folder\x00" + fe.Folder
	}
	return "message\x00" + e.Error()
}

// mergeLoadErrors appends to errs each error of more whose folder errs does not
// already report. Two reads of the same directory normally agree; when they do
// not, every failure is kept (fail closed).
func mergeLoadErrors(errs, more []error) []error {
	seen := map[string]bool{}
	for _, e := range errs {
		seen[loadErrorKey(e)] = true
	}
	for _, e := range more {
		if k := loadErrorKey(e); !seen[k] {
			seen[k] = true
			errs = append(errs, e)
		}
	}
	return errs
}

// loadErrorText is a load error as one line that names its folder.
func loadErrorText(e error) string {
	var fe *profile.FolderError
	if errors.As(e, &fe) && !strings.Contains(e.Error(), fe.Folder) {
		return fe.Folder + ": " + e.Error()
	}
	return e.Error()
}

// redactor builds the redactor for this host, with the serials of every
// device id the host reports now (deviceIDs).
func redactor(env Env) (*redact.Redactor, error) {
	return redactorFor(env, deviceIDs(env))
}

// redactorFor builds a redactor from this host's user and host names and the
// serials of ids. Names under 3 characters are skipped (redact.New refuses
// them) and the generic rules still apply.
func redactorFor(env Env, ids []string) (*redact.Redactor, error) {
	var users, hosts []string
	if env.Host != nil {
		if u := env.Host.Identity.UserName(); len(u) >= 3 {
			users = append(users, u)
		}
		for _, n := range env.Host.HostNames {
			if len(n) >= 3 {
				hosts = append(hosts, n)
			}
		}
	}
	var serials []string
	for _, s := range redact.SerialsFrom(ids...) {
		if len(s) >= 3 {
			serials = append(serials, s)
		}
	}
	return redact.New(redact.Options{UserNames: users, HostNames: hosts, Serials: serials})
}

// deviceIDs lists every device id this host knows of: the prefs device keys
// and each profile's Device.UUID. Their serials are redacted wherever they
// appear, including outside a device id (a plugin setting, a prefs field).
func deviceIDs(env Env) []string {
	if env.Host == nil {
		return nil
	}
	var ids []string
	if recs, err := env.Host.Prefs.DeviceRecords(); err == nil {
		for _, r := range recs {
			if k, ok := r[decks.RecordKey].(string); ok {
				ids = append(ids, k)
			}
		}
	}
	if res, err := profile.LoadAll(os.DirFS(env.Host.Paths.ProfilesDir())); err == nil {
		for _, p := range res.Profiles {
			ids = append(ids, p.DeviceUUID())
		}
	}
	return ids
}

type versionData struct {
	SchrodeckVersion string `json:"schrodeck_version"`
}

func runVersion(_ context.Context, env Env, args []string) (result, error) {
	if err := noArgs("version", newFlags("version"), env, args); err != nil {
		return result{}, err
	}
	return result{data: versionData{SchrodeckVersion: env.Version}, text: fmt.Sprintf("schrodeck %s\n", env.Version)}, nil
}

type appStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
}

type hostStatus struct {
	HostID   string    `json:"host_id"`
	App      appStatus `json:"app"`
	Decks    int       `json:"decks"`
	Profiles int       `json:"profiles"`
}

type statusData struct {
	SchrodeckVersion string      `json:"schrodeck_version"`
	Host             *hostStatus `json:"host,omitempty"`
}

func runStatus(_ context.Context, env Env, args []string) (result, error) {
	if err := noArgs("status", newFlags("status"), env, args); err != nil {
		return result{}, err
	}
	d := statusData{SchrodeckVersion: env.Version}
	var text strings.Builder
	fmt.Fprintf(&text, "schrodeck %s\n", env.Version)
	if h := env.Host; h != nil {
		hs, err := hostSummary(h)
		if err != nil {
			return result{}, err
		}
		d.Host = &hs
		fmt.Fprintf(&text, "host %s · Stream Deck app %s (installed %v, running %v) · %d deck(s) · %d profile(s)\n",
			hs.HostID, orUnknown(hs.App.Version), hs.App.Installed, hs.App.Running, hs.Decks, hs.Profiles)
	}
	text.WriteString("No setups yet: syncing arrives in a later milestone.\n")
	return result{data: d, text: text.String()}, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func hostSummary(h *host.Host) (hostStatus, error) {
	id, err := identity.HostID(h.Identity)
	if err != nil {
		return hostStatus{}, err
	}
	hs := hostStatus{HostID: id}
	if hs.App.Installed, err = h.App.Installed(); err != nil {
		return hostStatus{}, err
	}
	if hs.App.Running, err = h.App.Running(); err != nil {
		return hostStatus{}, err
	}
	if v, err := h.Prefs.AppVersion(); err == nil {
		hs.App.Version = v
	}
	if ds, _, err := listDecks(h); err == nil {
		hs.Decks = len(ds)
	}
	if res, err := loadProfiles(h); err == nil {
		hs.Profiles = len(res.Profiles)
	}
	return hs, nil
}

type deckInfo struct {
	Key         string `json:"key"`
	Model       string `json:"model,omitempty"`
	Columns     int    `json:"columns,omitempty"`
	Rows        int    `json:"rows,omitempty"`
	Dials       int    `json:"dials,omitempty"`
	Virtual     bool   `json:"virtual"`
	Destination bool   `json:"destination"`
	Why         string `json:"why,omitempty"`
}

type profileInfo struct {
	Folder        string `json:"folder"`
	Name          string `json:"name"`
	Device        string `json:"device"`
	Pages         int    `json:"pages"`
	Hash          string `json:"hash,omitempty"`
	HashError     string `json:"hash_error,omitempty"`
	AppIdentifier string `json:"app_identifier,omitempty"`
}

type inventoryData struct {
	HostID      string        `json:"host_id"`
	AppVersion  string        `json:"app_version,omitempty"`
	NormVersion int           `json:"norm_version"`
	Fingerprint string        `json:"fingerprint,omitempty"`
	Decks       []deckInfo    `json:"decks"`
	Profiles    []profileInfo `json:"profiles"`
	LoadErrors  []string      `json:"load_errors,omitempty"`
	Unmatched   *unmatched    `json:"unmatched,omitempty"`
}

// unmatched is what the deck list cannot tie together (the U9 observation):
// profiles bound to no device key, and device keys no profile is bound to.
type unmatched struct {
	Profiles []string `json:"profiles,omitempty"`
	Decks    []string `json:"decks,omitempty"`
}

func runInventory(_ context.Context, env Env, args []string) (result, error) {
	fs := newFlags("inventory")
	showIDs := fs.Bool("show-ids", false, "print device ids unredacted (they contain deck serials)")
	if err := noArgs("inventory", fs, env, args); err != nil {
		return result{}, err
	}
	h := env.Host
	if h == nil {
		return result{}, errNoHost
	}
	r, err := redactor(env)
	if err != nil {
		return result{}, err
	}
	show := func(s string) string {
		if *showIDs {
			return s
		}
		return r.String(s)
	}
	id, err := identity.HostID(h.Identity)
	if err != nil {
		return result{}, err
	}
	d := inventoryData{HostID: id, NormVersion: normhash.NormVersion, Decks: []deckInfo{}, Profiles: []profileInfo{}}
	d.AppVersion, _ = h.Prefs.AppVersion()

	ds, deckLoadErrs, err := listDecks(h)
	if err != nil {
		return result{}, err
	}
	for _, s := range decks.Annotate(ds) {
		di := deckInfo{Key: show(s.AppDeviceID), Model: s.Model, Columns: s.Geometry.Columns, Rows: s.Geometry.Rows, Dials: s.Geometry.Dials, Virtual: s.Virtual, Destination: s.Destination()}
		switch {
		case !s.KeyUnique:
			di.Why = "another deck on this computer has the same key"
		case !s.GeometryKnown:
			di.Why = "geometry not verified for this model yet"
		}
		d.Decks = append(d.Decks, di)
	}

	res, err := loadProfiles(h)
	if err != nil {
		return result{}, err
	}
	for _, e := range mergeLoadErrors(res.Errors, deckLoadErrs) {
		d.LoadErrors = append(d.LoadErrors, show(loadErrorText(e)))
	}
	sort.Slice(res.Profiles, func(i, j int) bool { return res.Profiles[i].Folder < res.Profiles[j].Folder })
	for _, p := range res.Profiles {
		pi := profileInfo{Folder: p.Folder, Name: show(p.Name()), Device: show(p.DeviceUUID()), Pages: len(p.Pages)}
		if hsh, err := normhash.Hash(p); err == nil {
			pi.Hash = hsh
		} else {
			pi.HashError = show(err.Error())
		}
		if app, ok := p.AppIdentifier(); ok {
			pi.AppIdentifier = show(app)
		}
		d.Profiles = append(d.Profiles, pi)
	}
	if len(res.Profiles) > 0 {
		if schema, err := profile.SchemaOf(res.Profiles); err == nil {
			d.Fingerprint, _ = schema.Digest()
		}
	}
	if recs, err := h.Prefs.DeviceRecords(); err == nil {
		profs, keys := decks.Unmatched(recs, res.Profiles)
		for i := range keys {
			keys[i] = show(keys[i])
		}
		if len(profs) > 0 || len(keys) > 0 {
			d.Unmatched = &unmatched{Profiles: profs, Decks: keys}
		}
	}

	var text strings.Builder
	fmt.Fprintf(&text, "host %s · app %s · norm_version %d · fingerprint %s\n\ndecks:\n", d.HostID, orUnknown(d.AppVersion), d.NormVersion, short(d.Fingerprint))
	for _, di := range d.Decks {
		grid := "?"
		if di.Columns > 0 {
			grid = fmt.Sprintf("%dx%d", di.Columns, di.Rows)
		}
		fmt.Fprintf(&text, "  %-28s %-10s %-5s destination=%v %s\n", di.Key, di.Model, grid, di.Destination, di.Why)
	}
	text.WriteString("\nprofiles:\n")
	for _, pi := range d.Profiles {
		fmt.Fprintf(&text, "  %-48s %-24q pages=%d hash=%s %s\n", pi.Folder, pi.Name, pi.Pages, short(pi.Hash), pi.HashError)
	}
	for _, e := range d.LoadErrors {
		fmt.Fprintf(&text, "  NOT LOADED: %s\n", e)
	}
	if u := d.Unmatched; u != nil {
		for _, f := range u.Profiles {
			fmt.Fprintf(&text, "  UNMATCHED profile (its Device.UUID is no device key): %s\n", f)
		}
		for _, k := range u.Decks {
			fmt.Fprintf(&text, "  UNMATCHED deck (no profile is bound to it): %s\n", k)
		}
	}
	return result{data: d, text: text.String(), failed: len(d.LoadErrors) > 0}, nil
}

func short(s string) string {
	if s == "" {
		return "-"
	}
	return doctor.Short(s)
}

type doctorData struct {
	Tier        string `json:"tier"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Accepted    bool   `json:"accepted_now,omitempty"`
	// AcceptRefused says why --accept-fingerprint did not record anything.
	AcceptRefused string         `json:"accept_refused,omitempty"`
	Checks        []probe.Result `json:"checks"`
}

func runDoctor(ctx context.Context, env Env, args []string) (result, error) {
	fs := newFlags("doctor")
	accept := fs.Bool("accept-fingerprint", false, "record the current format fingerprint as known-good for this host (ADR 0015)")
	if err := noArgs("doctor", fs, env, args); err != nil {
		return result{}, err
	}
	h := env.Host
	if h == nil {
		return result{}, errNoHost
	}
	r, err := redactor(env)
	if err != nil {
		return result{}, err
	}
	res, err := loadProfiles(h)
	if err != nil {
		res = profile.LoadResult{Errors: []error{err}}
	}
	if _, deckLoadErrs, err := listDecks(h); err == nil {
		res.Errors = mergeLoadErrors(res.Errors, deckLoadErrs)
	}
	probes := append(doctor.ContractB(h, res), probe.ContractC(probe.Install{Load: res, Home: h.Paths.Home()})...)

	d := doctorData{Tier: probe.ReadOnly.String()}
	// Every other check runs first: ADR 0015's "the user confirms the new
	// fingerprint as known-good" is only offered on an install that otherwise
	// passes, and with every profile loaded (a profile that failed to load is
	// missing from the fingerprint, so accepting would bless a partial view).
	d.Checks = probe.RunAll(ctx, probes, probe.ReadOnly)
	schema, digest, fpErr := fingerprint(res)
	if fpErr != nil {
		// No fingerprint (no profile loaded, or profiles that disagree on
		// Version, which P2 reports): FP fails rather than hiding the checks.
		d.Checks = append(d.Checks, probe.Result{ID: "FP", Contract: "C", Tier: probe.ReadOnly.String(), Status: probe.Fail, Detail: "no format fingerprint: " + fpErr.Error()})
		if *accept {
			d.AcceptRefused = "there is no fingerprint to accept: " + fpErr.Error()
		}
	} else {
		d.Fingerprint = digest
		// An unreadable known-good set fails FP (fail closed) without hiding
		// the other checks; an accept replaces it.
		known, knownErr := doctor.LoadKnown(h.Paths.StateDir())
		if knownErr != nil {
			known = doctor.Known{}
		}
		if *accept && !known.Contains(digest) {
			switch {
			case len(res.Errors) > 0:
				d.AcceptRefused = fmt.Sprintf("%d profile(s) did not load; fix them first, the fingerprint would leave them out", len(res.Errors))
			case probe.Failed(d.Checks):
				d.AcceptRefused = "another check failed; a fingerprint is accepted only when every other check passes"
			default:
				if err := guardStateDir(env); err != nil {
					return result{}, err
				}
				version, _ := h.Prefs.AppVersion()
				known.Accepted = append(known.Accepted, doctor.Accepted{Digest: digest, AppVersion: version, AcceptedAt: now(h), Schema: schema})
				if err := doctor.SaveKnown(h.Paths.StateDir(), known); err != nil {
					return result{}, err
				}
				d.Accepted = true
			}
		}
		if knownErr != nil && !d.Accepted {
			d.Checks = append(d.Checks, probe.Result{ID: "FP", Contract: "C", Tier: probe.ReadOnly.String(), Status: probe.Fail,
				Detail: "the known-good fingerprint set cannot be read: " + knownErr.Error() + "; review, then run `schrodeck doctor --accept-fingerprint` to replace it"})
		} else {
			d.Checks = append(d.Checks, probe.RunAll(ctx, []probe.Probe{doctor.Fingerprint(schema, digest, known)}, probe.ReadOnly)...)
		}
	}

	var text strings.Builder
	for i := range d.Checks {
		c := &d.Checks[i]
		c.Detail = r.String(c.Detail)
		for j := range c.Evidence {
			c.Evidence[j] = r.String(c.Evidence[j])
		}
		fmt.Fprintf(&text, "%-4s %-6s %s %s\n", c.Status, c.ID, c.Contract, c.Detail)
		for _, e := range c.Evidence {
			fmt.Fprintf(&text, "            %s\n", e)
		}
	}
	if d.Accepted {
		fmt.Fprintf(&text, "Recorded fingerprint %s as known-good for this host.\n", short(d.Fingerprint))
	}
	d.AcceptRefused = r.String(d.AcceptRefused)
	if d.AcceptRefused != "" {
		fmt.Fprintf(&text, "Fingerprint NOT accepted: %s.\n", d.AcceptRefused)
	}
	return result{data: d, text: text.String(), failed: probe.Failed(d.Checks)}, nil
}

// fingerprint is ADR 0015's format fingerprint over every loaded profile.
func fingerprint(res profile.LoadResult) (profile.Schema, string, error) {
	schema, err := profile.SchemaOf(res.Profiles)
	if err != nil {
		return profile.Schema{}, "", err
	}
	digest, err := schema.Digest()
	if err != nil {
		return profile.Schema{}, "", err
	}
	return schema, digest, nil
}

func now(h *host.Host) time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}
