#!/usr/bin/env bash
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Fails if a Go file is not gofmt-formatted, or cannot be parsed. With no arguments it checks every tracked .go file and fails if it finds none, so a mis-scoped CI step cannot pass vacuously.
# Usage: tools/ci/check-gofmt.sh [file...]
unset TMOUT
set -euo pipefail

files=()
if [[ $# -gt 0 ]]; then
  files=("$@")
else
  while IFS= read -r f; do files+=("$f"); done < <(git ls-files '*.go')
  if [[ ${#files[@]} -eq 0 ]]; then
    echo "check-gofmt: found 0 files to check (run from the repository root, with files tracked)" >&2
    exit 1
  fi
fi

rc=0
unformatted=$(gofmt -l "${files[@]}" 2>"${TMPDIR:-/tmp}/check-gofmt.$$.err") || rc=$?
errors=$(cat "${TMPDIR:-/tmp}/check-gofmt.$$.err")
rm -f "${TMPDIR:-/tmp}/check-gofmt.$$.err"
if [[ $rc -ne 0 || -n $errors ]]; then
  echo "check-gofmt: gofmt could not process the files (exit $rc):" >&2
  echo "$errors" >&2
  exit 1
fi
if [[ -n $unformatted ]]; then
  echo "check-gofmt: these files need gofmt:" >&2
  echo "$unformatted" >&2
  exit 1
fi
echo "check-gofmt: ${#files[@]} file(s) formatted"
