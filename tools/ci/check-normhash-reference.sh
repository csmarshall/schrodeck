#!/usr/bin/env bash
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Checks that the Go golden constants of deckformat/normhash are still what the independent Python reference (deckformat/normhash/testdata/refhash.py) computes for the same fixture on disk. For each golden fixture it writes the fixture with the TestWriteFixture* helper, runs the reference on it, and compares with the Go constant. Fails on any mismatch, on a reference that prints nothing or something that is not a sha256, and when a helper wrote no fixture (so a skipped helper cannot pass vacuously). Run from the repository root.
# Usage: tools/ci/check-normhash-reference.sh   (REFHASH_PYTHON overrides the interpreter, default python3)
unset TMOUT
set -euo pipefail

readonly TEST_FILE=deckformat/normhash/normhash_test.go
readonly REFERENCE=deckformat/normhash/testdata/refhash.py
readonly PYTHON=${REFHASH_PYTHON:-python3}
# <TestWriteFixture helper> <Go constant it pins>
readonly CASES=(
  "TestWriteFixtureForReference goldenXL"
  "TestWriteFixtureRelabelMissingOtherForReference goldenRelabelMissingOther"
)

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
failures=0

for c in "${CASES[@]}"; do
  read -r helper constant <<<"$c"
  want=$(sed -n "s/^const $constant = \"\\([0-9a-f]*\\)\"\$/\\1/p" "$TEST_FILE")
  if [[ ! "$want" =~ ^[0-9a-f]{64}$ ]]; then
    echo "check-normhash-reference: cannot read constant $constant from $TEST_FILE" >&2
    exit 1
  fi
  out="$scratch/$helper"
  mkdir "$out"
  (cd deckformat && NORMHASH_FIXTURE_OUT="$out" go test -count=1 ./normhash -run "^$helper\$" >/dev/null)
  folders=("$out"/*.sdProfile)
  if [[ ${#folders[@]} -ne 1 || ! -d ${folders[0]} ]]; then
    echo "check-normhash-reference: $helper wrote no single profile folder under $out" >&2
    exit 1
  fi
  got=$("$PYTHON" -I "$REFERENCE" "${folders[0]}")
  if [[ ! "$got" =~ ^[0-9a-f]{64}$ ]]; then
    echo "check-normhash-reference: the reference printed no sha256 for $constant: '$got'" >&2
    exit 1
  fi
  if [[ "$got" == "$want" ]]; then
    echo "ok    $constant = $got"
  else
    echo "FAIL  $constant: Go says $want, reference says $got"
    failures=$((failures + 1))
  fi
done
exit $((failures > 0 ? 1 : 0))
