#!/usr/bin/env bash
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Stores the owner's personal leak-scan patterns as the LEAK_SCAN_EXTRA repository secret. The value is read from a local file that is never committed, so the identifiers never appear in this public repository.
# Usage: tools/ci/set-leak-scan-secret.sh <file-with-one-PCRE-alternation>
unset TMOUT
set -euo pipefail

if [[ $# -ne 1 || ! -f $1 ]]; then
  echo "usage: $0 <file-with-one-PCRE-alternation>" >&2
  exit 2
fi
# $(...) strips trailing newlines, which an editor adds and which would otherwise split the git grep pattern; an empty pattern matches every line, and an embedded newline splits the alternation, so both are refused before anything is sent. The value is never printed.
value=$(cat "$1")
if [[ -z $value ]]; then
  echo "set-leak-scan-secret: $1 is empty; refusing to set a secret that would match every line" >&2
  exit 2
fi
if [[ $value == *$'\n'* ]]; then
  echo "set-leak-scan-secret: $1 holds more than one line; the secret must be a single PCRE alternation on one line" >&2
  exit 2
fi
printf '%s' "$value" | gh secret set LEAK_SCAN_EXTRA --repo csmarshall/schrodeck
echo "LEAK_SCAN_EXTRA set; the next CI run will report 'generic and personal patterns'"
