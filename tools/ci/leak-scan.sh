#!/usr/bin/env bash
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Leak scan (ADR 0020): fails if a tracked file contains a personal identifier. Generic patterns are built in. The owner's personal identifiers are NOT in this public file: they come from $LEAK_SCAN_EXTRA (a PCRE alternation) that CI reads from a repository secret. Without it, only the generic patterns run and the scan says so on every run. Exit codes: 0 clean, 1 hit, 2 git error or nothing tracked to scan.
# Usage: tools/ci/leak-scan.sh   (from a repository root)
unset TMOUT
set -euo pipefail

# Generic patterns: home directories of real machines (docs use the /Users/<user> placeholder), and Stream Deck device ids that still carry a USB serial (fixtures use <deck>).
readonly GENERIC='/Users/(?!Shared/)[a-z]|@\([0-9]+\)\[[0-9]+/[0-9]+/[A-Za-z0-9]{6,}\]'

pattern=$GENERIC
label="generic patterns"
if [[ -n ${LEAK_SCAN_EXTRA:-} ]]; then
  pattern="$GENERIC|$LEAK_SCAN_EXTRA"
  label="generic and personal patterns"
else
  echo "leak-scan: WARNING: LEAK_SCAN_EXTRA is empty, so personal identifiers were NOT checked" >&2
fi

tracked=$(git ls-files | wc -l) || {
  echo "leak-scan: git ls-files failed (run from a repository root)" >&2
  exit 2
}
if [[ $((tracked + 0)) -eq 0 ]]; then
  echo "leak-scan: no tracked files to scan (run from the repository root, with files tracked)" >&2
  exit 2
fi

# git grep's stderr can echo the combined pattern (a malformed LEAK_SCAN_EXTRA does), so it is captured and only ever printed with the secret masked.
rc=0
errfile=$(mktemp)
trap 'rm -f "$errfile"' EXIT
hits=$(git grep -nIiP -e "$pattern" -- . ':(exclude)LICENSE' 2>"$errfile") || rc=$?
case $rc in
  0)
    printf '%s\n' "$hits" >&2
    echo "leak-scan: FAILED ($label)" >&2
    exit 1
    ;;
  1)
    echo "leak-scan: clean ($label, $((tracked + 0)) tracked files)"
    ;;
  *)
    echo "leak-scan: git grep failed (exit $rc)" >&2
    err=$(cat "$errfile")
    if [[ -n ${LEAK_SCAN_EXTRA:-} ]]; then
      err=${err//"$LEAK_SCAN_EXTRA"/<LEAK_SCAN_EXTRA>}
    fi
    printf '%s\n' "$err" >&2
    exit 2
    ;;
esac
