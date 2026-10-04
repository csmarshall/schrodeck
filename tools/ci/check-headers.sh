#!/usr/bin/env bash
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Fails if a source file does not start with the MPL-2.0 Exhibit A header (CLAUDE.md: "MPL-2.0 Exhibit A header on every source file"): the three exact lines must begin at line 1, or at line 2 when line 1 is a shebang, with either the "// " (Go, Swift) or "# " (shell) comment prefix. With no arguments it checks every tracked .go, .sh and .swift file and fails if it finds none, so a mis-scoped CI step cannot pass vacuously.
# Usage: tools/ci/check-headers.sh [file...]
unset TMOUT
set -euo pipefail

readonly LINE1='This Source Code Form is subject to the terms of the Mozilla Public'
readonly LINE2='License, v. 2.0. If a copy of the MPL was not distributed with this'
readonly LINE3='file, You can obtain one at https://mozilla.org/MPL/2.0/.'
missing=0
checked=0

# has_header <file> <comment-prefix> <first-header-line-number>
has_header() {
  local f=$1 prefix=$2 start=$3 got want
  got=$(sed -n "${start},$((start + 2))p" "$f")
  want=$(printf '%s %s\n%s %s\n%s %s' "$prefix" "$LINE1" "$prefix" "$LINE2" "$prefix" "$LINE3")
  [[ "$got" == "$want" ]]
}

check() {
  local f=$1 start=1 first
  checked=$((checked + 1))
  if [[ ! -f "$f" ]]; then
    echo "not a file: $f" >&2
    missing=$((missing + 1))
    return
  fi
  first=$(sed -n 1p "$f")
  if [[ "$first" == '#!'* ]]; then
    start=2
  fi
  if has_header "$f" '//' "$start" || has_header "$f" '#' "$start"; then
    return
  fi
  echo "missing or misplaced MPL-2.0 header: $f" >&2
  missing=$((missing + 1))
}

if [[ $# -gt 0 ]]; then
  for f in "$@"; do check "$f"; done
else
  while IFS= read -r f; do check "$f"; done < <(git ls-files '*.go' '*.sh' '*.swift')
  if [[ $checked -eq 0 ]]; then
    echo "check-headers: found 0 files to check (run from the repository root, with files tracked)" >&2
    exit 1
  fi
fi

if [[ $missing -gt 0 ]]; then
  echo "check-headers: $missing of $checked file(s) lack the header" >&2
  exit 1
fi
echo "check-headers: $checked file(s) ok"
