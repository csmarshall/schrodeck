// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/csmarshall/schrodeck/deckformat/jsondoc"
	"github.com/csmarshall/schrodeck/deckformat/observe"
	"github.com/csmarshall/schrodeck/deckformat/pathguard"
	"github.com/csmarshall/schrodeck/deckformat/profile"
	"github.com/csmarshall/schrodeck/deckformat/redact"
	"github.com/csmarshall/schrodeck/internal/decks"
)

// Settling: an observation snapshot is taken only once two reads this far
// apart are identical. This is a human-scale heuristic for "the app has
// finished writing after the click", not a measured app property (contract C
// P4 measures the app's own settle window in M3); it is a named setting
// because it cannot be derived.
const (
	settleInterval = 2 * time.Second
	settleTries    = 15
)

var observationName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func observeDir(env Env, name string) string {
	return filepath.Join(env.Host.Paths.StateDir(), "observe", name)
}

// snapshot takes a settled snapshot of the app's profiles and device records.
// The records are an object keyed by device key (decks.Keyed), as the app
// keeps them, so a report row's path names the device rather than a list
// index that shifts when a deck is added. Profiles that failed to load during
// the deck read are kept with the snapshot's own load errors.
func snapshot(ctx context.Context, env Env) (*observe.Snapshot, error) {
	h := env.Host
	take := func() (*observe.Snapshot, error) {
		// Checked before every try, so an interrupt (Ctrl-C) during settling
		// stops at the next snapshot instead of after all of them.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A prefs read that fails is an error, not a snapshot without prefs:
		// that would report every device record as removed or added.
		recs, err := h.Prefs.DeviceRecords()
		if err != nil {
			return nil, fmt.Errorf("reading the app's device records: %w", err)
		}
		devices := map[string]any{}
		for key, rec := range decks.Keyed(recs) {
			devices[key] = map[string]any(rec)
		}
		prefs, err := jsondoc.FromAny(map[string]any{"Devices": devices})
		if err != nil {
			return nil, err
		}
		version, _ := h.Prefs.AppVersion()
		s, err := observe.Take(os.DirFS(h.Paths.ProfilesDir()), prefs, version, now(h))
		if err != nil {
			return nil, err
		}
		if _, deckLoadErrs, err := listDecks(h); err == nil {
			s.LoadErrors = mergeSnapshotLoadErrors(s.LoadErrors, deckLoadErrs)
		}
		return s, nil
	}
	sleep := h.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	wait := func() {
		if ctx.Err() == nil {
			sleep(settleInterval)
		}
	}
	return observe.Settle(take, wait, settleTries)
}

// snapshotDeviceIDs lists the device ids a snapshot holds: each profile's
// Device.UUID and each prefs Devices member name.
func snapshotDeviceIDs(s *observe.Snapshot) []string {
	var ids []string
	for _, p := range s.Profiles {
		ids = append(ids, p.DeviceUUID())
	}
	if s.Prefs != nil {
		if devs := s.Prefs.Get("Devices"); devs != nil && devs.Kind() == jsondoc.Object {
			for _, m := range devs.Members() {
				ids = append(ids, m.Name)
			}
		}
	}
	return ids
}

// mergeSnapshotLoadErrors adds each error of more whose folder the snapshot
// does not already report.
func mergeSnapshotLoadErrors(have []observe.LoadError, more []error) []observe.LoadError {
	seen := map[string]bool{}
	for _, le := range have {
		seen[le.Folder] = true
	}
	for _, e := range more {
		le := observe.LoadError{Message: e.Error()}
		var fe *profile.FolderError
		if errors.As(e, &fe) {
			le.Folder = fe.Folder
		}
		if !seen[le.Folder] {
			seen[le.Folder] = true
			have = append(have, le)
		}
	}
	return have
}

// guardStateDir refuses a state dir that resolves inside the app's data
// (a misconfigured connector or a symlink): schrodeck's own writes and
// deletions there would land in the app's files. Each path below it that is
// then written or removed is checked too (guardStatePath), since a symlink
// below the state dir can point anywhere.
func guardStateDir(env Env) error {
	return guardStatePath(env, env.Host.Paths.StateDir())
}

// guardStatePath refuses a path under schrodeck's state dir whose resolved
// final target lies inside the app's data. Call it on the exact path about to
// be created, written or removed.
func guardStatePath(env Env, target string) error {
	if err := pathguard.RefuseInside(target, protectedRoots(env)...); err != nil {
		return fmt.Errorf("schrodeck's state path is refused: %w", err)
	}
	return nil
}

// reportRedactor is the one redactor for an observation report. Its serials
// come from the two snapshots being reported, not from a fresh read of the
// host: a read that fails or differs must not let an id through.
func reportRedactor(env Env, before, after *observe.Snapshot) (*redact.Redactor, error) {
	return redactorFor(env, append(snapshotDeviceIDs(before), snapshotDeviceIDs(after)...))
}

// protectedRoots are the directories M1 never writes: the app data root and
// the profiles directory, both, in case a connector places them apart.
func protectedRoots(env Env) []string {
	return []string{env.Host.Paths.AppDataRoot(), env.Host.Paths.ProfilesDir()}
}

// refuseAppData refuses a write target inside the Stream Deck app's data
// (M1 never writes there). Both the app data root and the profiles directory
// are protected, in case a connector places them apart.
func refuseAppData(env Env, target string) error {
	if err := pathguard.RefuseInside(target, protectedRoots(env)...); err != nil {
		return usageError{err.Error()}
	}
	return nil
}

type observeData struct {
	Name     string          `json:"name"`
	Snapshot string          `json:"snapshot,omitempty"`
	Report   *observe.Report `json:"report,omitempty"`
	Written  string          `json:"written,omitempty"`
}

func runObserve(ctx context.Context, env Env, args []string) (res result, err error) {
	fs := newFlags("observe")
	out := fs.String("out", "", "stop: also write the Markdown report to this file")
	rest, err := parseFlags(fs, env, args)
	if err != nil {
		return result{}, err
	}
	if len(rest) != 2 || (rest[0] != "start" && rest[0] != "stop") {
		return result{}, usageError{"usage: schrodeck observe start|stop <name> [--out FILE]"}
	}
	verb, name := rest[0], rest[1]
	if !observationName.MatchString(name) {
		return result{}, usageError{"observation names are lower-case letters, digits and dashes, e.g. u2-new-profile"}
	}
	if env.Host == nil {
		return result{}, errNoHost
	}
	if *out != "" {
		// Checked before anything is read, so a bad --out costs nothing: the
		// observation stays started and can be stopped again.
		if verb != "stop" {
			return result{}, usageError{"--out is only for observe stop"}
		}
		if err := refuseAppData(env, *out); err != nil {
			return result{}, err
		}
		if _, err := os.Lstat(*out); err == nil {
			return result{}, usageError{fmt.Sprintf("--out %s already exists; reports are never overwritten", *out)}
		}
	}
	dir := observeDir(env, name)
	// The state dir and the observation's own dir, resolved: start creates
	// it, stop reads and removes it, and either through a symlink below the
	// state dir could reach the app's files.
	if err := guardStateDir(env); err != nil {
		return result{}, err
	}
	if err := guardStatePath(env, dir); err != nil {
		return result{}, err
	}

	if verb == "start" {
		if _, err := os.Stat(dir); err == nil {
			return result{}, fmt.Errorf("observation %q is already started; run `schrodeck observe stop %s` first", name, name)
		}
		s, err := snapshot(ctx, env)
		if err != nil {
			return result{}, err
		}
		if err := s.Save(filepath.Join(dir, "before")); err != nil {
			os.Remove(dir) // Save removed what it wrote; drop the now-empty parent so a retry can start
			return result{}, err
		}
		text := fmt.Sprintf("Snapshot taken (%d profiles). Do the one thing you want to observe in the Stream Deck app, wait a few seconds, then run:\n  schrodeck observe stop %s --out docs/observations/%s.md\n", len(s.Profiles), name, name)
		return result{data: observeData{Name: name, Snapshot: "taken"}, text: text}, nil
	}

	if _, err := os.Stat(dir); err != nil {
		return result{}, fmt.Errorf("no started observation %q: %w", name, err)
	}
	// The snapshots hold unredacted data, so from here on they are deleted on
	// every path, failures included: a stop that cannot report still must not
	// leave the raw copy behind. Start the observation again to retry.
	defer func() {
		rmErr := os.RemoveAll(dir)
		switch {
		case rmErr == nil:
		case err != nil:
			err = errors.Join(err, fmt.Errorf("the unredacted snapshots could not be deleted, remove %s by hand: %w", dir, rmErr))
		default:
			// The report is already out, so the command succeeded; the leftover
			// raw copy is said loudly instead.
			fmt.Fprintf(env.Stderr, "schrodeck observe: warning: the unredacted snapshots remain in %s and could not be deleted (%v); remove them by hand\n", dir, rmErr)
		}
	}()
	before, err := observe.Load(filepath.Join(dir, "before"))
	if err != nil {
		return result{}, fmt.Errorf("observation %q: the start snapshot does not load: %w", name, err)
	}
	after, err := snapshot(ctx, env)
	if err != nil {
		return result{}, err
	}
	// One redactor for the whole report, knowing the device ids of both
	// snapshots, so a deck present on only one side is redacted too.
	r, err := reportRedactor(env, before, after)
	if err != nil {
		return result{}, err
	}
	rep, err := observe.Compare(name, before, after, r)
	if err != nil {
		return result{}, err
	}
	md := rep.Markdown()
	d := observeData{Name: name, Report: &rep}
	if *out != "" {
		written, err := writeNewFile(env, *out, []byte(md))
		if err != nil {
			return result{}, err
		}
		d.Written = r.String(written)
	}
	return result{data: d, text: md}, nil
}

// writeNewFile writes data to a new file at target through pathguard.Create,
// which resolves the path the way the kernel will, refuses it inside the app's
// data and opens it exclusively. It returns the path written.
func writeNewFile(env Env, target string, data []byte) (string, error) {
	f, written, err := pathguard.Create(target, protectedRoots(env)...)
	if err != nil {
		if errors.Is(err, pathguard.ErrInside) {
			return "", usageError{err.Error()}
		}
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return written, nil
}

type fixtureData struct {
	Folder  string   `json:"folder"`
	Out     string   `json:"out"`
	Strings []string `json:"strings"`
}

func runFixture(_ context.Context, env Env, args []string) (result, error) {
	fs := newFlags("fixture")
	src := fs.String("profile", "", "folder name of the profile to export, e.g. ABCD….sdProfile")
	out := fs.String("out", "", "empty directory to write the redacted fixture into")
	name := fs.String("name", "", "folder name for the exported profile (default: the source folder name)")
	rest, err := parseFlags(fs, env, args)
	if err != nil {
		return result{}, err
	}
	if len(rest) != 1 || rest[0] != "export" || *src == "" || *out == "" {
		return result{}, usageError{"usage: schrodeck fixture export --profile <folder> --out <dir> [--name <folder>]"}
	}
	if env.Host == nil {
		return result{}, errNoHost
	}
	if pathguard.SingleName(*src) != nil {
		return result{}, usageError{"--profile must be a single folder name inside the profiles directory"}
	}
	folder := *name
	if folder == "" {
		folder = *src
	}
	if pathguard.SingleName(folder) != nil || !strings.HasSuffix(folder, profile.Suffix) {
		return result{}, usageError{"--name must be a single folder name ending in " + profile.Suffix}
	}
	if err := refuseAppData(env, filepath.Join(*out, folder)); err != nil {
		return result{}, err
	}
	root := env.Host.Paths.ProfilesDir()
	p, err := profile.Load(os.DirFS(root), *src)
	if err != nil {
		return result{}, err
	}
	r, err := redactor(env)
	if err != nil {
		return result{}, err
	}
	written, err := redact.ExportFixture(p, r, *out, folder, protectedRoots(env)...)
	if err != nil {
		if errors.Is(err, redact.ErrOutputExists) || errors.Is(err, pathguard.ErrInside) {
			return result{}, usageError{err.Error()}
		}
		return result{}, err
	}
	// Re-load from the path ExportFixture returns: a UUID in the folder name is
	// pseudonymized, so the written folder can differ from the one asked for.
	exported, err := profile.Load(os.DirFS(filepath.Dir(written)), filepath.Base(written))
	if err != nil {
		return result{}, fmt.Errorf("the exported fixture does not load: %w", err)
	}
	strs := redact.Strings(exported)
	var text strings.Builder
	fmt.Fprintf(&text, "Wrote %s. Review every string below before committing it; the leak scan is a second line of defense, not the first:\n", r.String(written))
	for _, s := range strs {
		fmt.Fprintf(&text, "  %q\n", s)
	}
	return result{data: fixtureData{Folder: filepath.Base(written), Out: r.String(filepath.Dir(written)), Strings: strs}, text: text.String()}, nil
}
