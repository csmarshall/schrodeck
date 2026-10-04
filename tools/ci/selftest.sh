#!/usr/bin/env bash
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Self-tests for the CI detectors in tools/ci/. Each detector is run against a known-good input (must pass) and at least one known-bad input (must fail). A detector that has never been seen to fail has not been tested. Every case runs; the script exits non-zero if any verdict was wrong.
# Usage: tools/ci/selftest.sh   (from the repository root)
unset TMOUT
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
failures=0
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

# expect <wanted-exit-code> <description> <command...>
expect() {
  local want=$1 desc=$2
  shift 2
  local got=0
  "$@" >"$scratch/last.out" 2>&1 || got=$?
  if [[ $got -eq $want ]]; then
    echo "ok    $desc"
  else
    echo "FAIL  $desc (exit $got, want $want)"
    sed 's/^/      | /' "$scratch/last.out"
    failures=$((failures + 1))
  fi
}

# require_detector <script-name>: a missing detector must not let the want-1 cases pass by accident (bash 3.2 reports a failed exec as exit 1 here, not 127).
require_detector() {
  if [[ ! -x "$here/$1" ]]; then
    echo "selftest: $here/$1 is missing or not executable" >&2
    exit 1
  fi
}
require_detector check-headers.sh
require_detector check-gofmt.sh
require_detector leak-scan.sh
require_detector set-leak-scan-secret.sh

# in_dir <dir> <command...>: run a command from inside a directory.
in_dir() {
  local dir=$1
  shift
  (cd "$dir" && "$@")
}

header_go() {
  printf '%s\n' \
    '// This Source Code Form is subject to the terms of the Mozilla Public' \
    '// License, v. 2.0. If a copy of the MPL was not distributed with this' \
    '// file, You can obtain one at https://mozilla.org/MPL/2.0/.'
}

header_sh() {
  printf '%s\n' \
    '# This Source Code Form is subject to the terms of the Mozilla Public' \
    '# License, v. 2.0. If a copy of the MPL was not distributed with this' \
    '# file, You can obtain one at https://mozilla.org/MPL/2.0/.'
}

# --- check-headers.sh with explicit files ------------------------------------
{ header_go; printf 'package good\n'; } >"$scratch/good.go"
printf 'package bad\n' >"$scratch/bad.go"
{ printf '#!/usr/bin/env bash\n'; header_sh; } >"$scratch/good.sh"
# Header present but not at the top: must fail now that placement is enforced.
{ printf 'package misplaced\n\n'; header_go; } >"$scratch/misplaced.go"
# Shebang file whose header is on line 3 instead of line 2.
{ printf '#!/usr/bin/env bash\n\n'; header_sh; } >"$scratch/misplaced.sh"
# Header cut short after its first line.
{ header_go | sed -n 1p; printf 'package truncated\n'; } >"$scratch/truncated.go"
# Header whose second line is altered.
{ header_go | sed -n 1p; printf '// License, v. 3.0.\n'; header_go | sed -n 3p; printf 'package altered\n'; } >"$scratch/altered.go"

expect 0 "check-headers passes a Go file with the header" "$here/check-headers.sh" "$scratch/good.go"
expect 0 "check-headers passes a script with shebang + header" "$here/check-headers.sh" "$scratch/good.sh"
expect 1 "check-headers fails a Go file without the header" "$here/check-headers.sh" "$scratch/bad.go"
expect 1 "check-headers fails when one of several files lacks it" "$here/check-headers.sh" "$scratch/good.go" "$scratch/bad.go"
expect 1 "check-headers fails a Go file whose header is not at the top" "$here/check-headers.sh" "$scratch/misplaced.go"
expect 1 "check-headers fails a script whose header is below line 2" "$here/check-headers.sh" "$scratch/misplaced.sh"
expect 1 "check-headers fails a truncated header" "$here/check-headers.sh" "$scratch/truncated.go"
expect 1 "check-headers fails an altered header" "$here/check-headers.sh" "$scratch/altered.go"

# --- check-headers.sh with no arguments (the mode CI runs) --------------------
# Each case is its own throwaway git repository; the checker lists tracked files, so they are staged with git add.
repo_good="$scratch/repo-good"
repo_bad="$scratch/repo-bad"
repo_empty="$scratch/repo-empty"
for r in "$repo_good" "$repo_bad" "$repo_empty"; do
  mkdir -p "$r"
  git -C "$r" init -q
done
cp "$scratch/good.go" "$scratch/good.sh" "$repo_good/"
cp "$scratch/good.go" "$scratch/bad.go" "$repo_bad/"
printf 'not a source file\n' >"$repo_empty/notes.txt"
git -C "$repo_good" add good.go good.sh
git -C "$repo_bad" add good.go bad.go
git -C "$repo_empty" add notes.txt

expect 0 "check-headers (no args) passes a repo of headed files" in_dir "$repo_good" "$here/check-headers.sh"
expect 1 "check-headers (no args) fails a repo with an unheaded file" in_dir "$repo_bad" "$here/check-headers.sh"
expect 1 "check-headers (no args) fails when it finds 0 files to check" in_dir "$repo_empty" "$here/check-headers.sh"

# --- check-gofmt.sh with explicit files ---------------------------------------
{ header_go; printf 'package good\n\nfunc F() {}\n'; } >"$scratch/fmt_good.go"
{ header_go; printf 'package bad\nfunc  F( ){ }\n'; } >"$scratch/fmt_bad.go"
{ header_go; printf 'package broken\nfunc F( {\n'; } >"$scratch/fmt_syntax.go"
expect 0 "check-gofmt passes a formatted file" "$here/check-gofmt.sh" "$scratch/fmt_good.go"
expect 1 "check-gofmt fails an unformatted file" "$here/check-gofmt.sh" "$scratch/fmt_bad.go"
expect 1 "check-gofmt fails when one of several files is unformatted" "$here/check-gofmt.sh" "$scratch/fmt_good.go" "$scratch/fmt_bad.go"
expect 1 "check-gofmt fails a file that does not parse" "$here/check-gofmt.sh" "$scratch/fmt_syntax.go"

# --- check-gofmt.sh with no arguments (the mode CI runs) ----------------------
fmt_good_repo="$scratch/fmt-repo-good"
fmt_bad_repo="$scratch/fmt-repo-bad"
fmt_empty_repo="$scratch/fmt-repo-empty"
for r in "$fmt_good_repo" "$fmt_bad_repo" "$fmt_empty_repo"; do
  mkdir -p "$r"
  git -C "$r" init -q
done
cp "$scratch/fmt_good.go" "$fmt_good_repo/good.go"
cp "$scratch/fmt_good.go" "$fmt_bad_repo/good.go"
cp "$scratch/fmt_bad.go" "$fmt_bad_repo/bad.go"
printf 'not a source file\n' >"$fmt_empty_repo/notes.txt"
git -C "$fmt_good_repo" add good.go
git -C "$fmt_bad_repo" add good.go bad.go
git -C "$fmt_empty_repo" add notes.txt
expect 0 "check-gofmt (no args) passes a repo of formatted files" in_dir "$fmt_good_repo" "$here/check-gofmt.sh"
expect 1 "check-gofmt (no args) fails a repo with an unformatted file" in_dir "$fmt_bad_repo" "$here/check-gofmt.sh"
expect 1 "check-gofmt (no args) fails when it finds 0 files to check" in_dir "$fmt_empty_repo" "$here/check-gofmt.sh"

# --- leak-scan.sh -------------------------------------------------------------
# Each case is its own throwaway git repository, because the scanner reads tracked files only. Bad strings are assembled at runtime so this file itself never matches the scanner's patterns, and the stand-in "personal" pattern is an obviously fake word.
make_repo() { # make_repo <name> <file-content>
  local dir="$scratch/leak-repo-$1"
  mkdir -p "$dir"
  git -C "$dir" init -q
  printf '%s\n' "$2" >"$dir/content.txt"
  git -C "$dir" add content.txt
  echo "$dir"
}
clean=$(make_repo clean "docs use /Users/<user>/bin and @(1)[4057/143/<deck>]")
home=$(make_repo home "$(printf 'path: /Users/%s/bin/demo.sh' alice)")
device=$(make_repo device "$(printf 'id: @(1)[4057/143/%s]' AB12CD34EF)")
extra=$(make_repo extra "$(printf 'mentions %s here' zebra-marker)")
empty_leak_repo="$scratch/leak-repo-empty"
mkdir -p "$empty_leak_repo"
git -C "$empty_leak_repo" init -q
not_a_repo="$scratch/not-a-repo"
mkdir -p "$not_a_repo"
expect 0 "leak-scan passes placeholders (known-good)" in_dir "$clean" env LEAK_SCAN_EXTRA= "$here/leak-scan.sh"
expect 1 "leak-scan fails a real home path" in_dir "$home" env LEAK_SCAN_EXTRA= "$here/leak-scan.sh"
expect 1 "leak-scan fails a serial-bearing device id" in_dir "$device" env LEAK_SCAN_EXTRA= "$here/leak-scan.sh"
expect 0 "leak-scan without LEAK_SCAN_EXTRA misses a personal word" in_dir "$extra" env LEAK_SCAN_EXTRA= "$here/leak-scan.sh"
expect 1 "leak-scan with LEAK_SCAN_EXTRA catches it" in_dir "$extra" env LEAK_SCAN_EXTRA=zebra-marker "$here/leak-scan.sh"
expect 2 "leak-scan fails (2) when nothing is tracked" in_dir "$empty_leak_repo" env LEAK_SCAN_EXTRA= "$here/leak-scan.sh"
expect 2 "leak-scan fails (2) outside a git repository" in_dir "$not_a_repo" env LEAK_SCAN_EXTRA= GIT_CEILING_DIRECTORIES="$scratch" "$here/leak-scan.sh"

# expect_output <description> <fixed-string> <want-present|want-absent> <command...>: the combined output must (not) contain the string.
expect_output() {
  local desc=$1 needle=$2 mode=$3 count
  shift 3
  count=$("$@" 2>&1 | grep -cF -- "$needle" || true)
  if [[ ($mode == want-present && $count -gt 0) || ($mode == want-absent && $count -eq 0) ]]; then
    echo "ok    $desc"
  else
    echo "FAIL  $desc ($mode, saw $count line(s) containing: $needle)"
    failures=$((failures + 1))
  fi
}
expect_output "leak-scan says plainly it ran generic-only without the secret" 'personal identifiers were NOT checked' want-present in_dir "$clean" env LEAK_SCAN_EXTRA= "$here/leak-scan.sh"
expect_output "leak-scan stays silent about skipped patterns when the secret is set" 'personal identifiers were NOT checked' want-absent in_dir "$clean" env LEAK_SCAN_EXTRA=zebra-marker "$here/leak-scan.sh"
expect_output "leak-scan with the secret set reports the personal-pattern verdict" '(generic and personal patterns' want-present in_dir "$clean" env LEAK_SCAN_EXTRA=zebra-marker "$here/leak-scan.sh"
# A malformed extra pattern makes git grep fail and echo the pattern; the secret must be masked. The want-present on the failure message keeps the want-absent from passing vacuously.
bad_extra='secretword('
expect 2 "leak-scan fails (2) on a malformed LEAK_SCAN_EXTRA" in_dir "$clean" env LEAK_SCAN_EXTRA="$bad_extra" "$here/leak-scan.sh"
expect_output "leak-scan reports the git grep failure for a malformed LEAK_SCAN_EXTRA" 'git grep failed' want-present in_dir "$clean" env LEAK_SCAN_EXTRA="$bad_extra" "$here/leak-scan.sh"
expect_output "leak-scan never echoes the LEAK_SCAN_EXTRA value when git grep fails" "$bad_extra" want-absent in_dir "$clean" env LEAK_SCAN_EXTRA="$bad_extra" "$here/leak-scan.sh"
expect_output "leak-scan names the masked placeholder in the git grep error" '<LEAK_SCAN_EXTRA>' want-present in_dir "$clean" env LEAK_SCAN_EXTRA="$bad_extra" "$here/leak-scan.sh"
expect_output "leak-scan reports generic-only in its verdict line" '(generic patterns' want-present in_dir "$clean" env LEAK_SCAN_EXTRA= "$here/leak-scan.sh"

# --- set-leak-scan-secret.sh (usage path only; the gh call needs credentials) --
expect 2 "set-leak-scan-secret prints usage and exits 2 without an argument" "$here/set-leak-scan-secret.sh"
expect 2 "set-leak-scan-secret exits 2 for a missing file" "$here/set-leak-scan-secret.sh" "$scratch/no-such-file"
# Validation happens before gh is invoked, so these need no credentials; the refusal cases run with no stub gh first on PATH, so a regression (validation skipped) fails with a different exit code instead of passing.
: >"$scratch/secret-empty"
printf '\n\n' >"$scratch/secret-blank-lines"
printf 'alpha|beta\ngamma\n' >"$scratch/secret-two-lines"
printf 'alpha|beta\n' >"$scratch/secret-one-line"
mkdir "$scratch/nogh-bin"
expect 2 "set-leak-scan-secret refuses an empty file" env PATH="$scratch/nogh-bin:/usr/bin:/bin" "$here/set-leak-scan-secret.sh" "$scratch/secret-empty"
expect 2 "set-leak-scan-secret refuses a file of only newlines" env PATH="$scratch/nogh-bin:/usr/bin:/bin" "$here/set-leak-scan-secret.sh" "$scratch/secret-blank-lines"
expect 2 "set-leak-scan-secret refuses a value with an embedded newline" env PATH="$scratch/nogh-bin:/usr/bin:/bin" "$here/set-leak-scan-secret.sh" "$scratch/secret-two-lines"
# Known-good: a fake gh on PATH records what it was sent, proving the trailing newline is stripped and the value reaches gh intact.
cat >"$scratch/nogh-bin/gh" <<'GH'
#!/usr/bin/env bash
cat >"$FAKE_GH_STDIN"
GH
chmod +x "$scratch/nogh-bin/gh"
expect 0 "set-leak-scan-secret sends a one-line file to gh" env FAKE_GH_STDIN="$scratch/gh-stdin" PATH="$scratch/nogh-bin:/usr/bin:/bin" "$here/set-leak-scan-secret.sh" "$scratch/secret-one-line"
if [[ "$(od -An -c "$scratch/gh-stdin" | tr -d ' ')" == 'alpha|beta' ]]; then
  echo "ok    set-leak-scan-secret strips the trailing newline before sending"
else
  echo "FAIL  set-leak-scan-secret sent unexpected bytes: $(od -An -c "$scratch/gh-stdin")"
  failures=$((failures + 1))
fi

if [[ $failures -gt 0 ]]; then
  echo "selftest: $failures detector verdict(s) wrong" >&2
  exit 1
fi
echo "selftest: all detector verdicts as expected"
