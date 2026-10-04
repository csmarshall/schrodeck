// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package cli

import (
	"encoding/json"
	"io"
)

// SchemaVersion is contract E's version (docs/contracts/cli-json.md). Bump it when a field is removed, renamed or changes type; adding a field is not a breaking change.
const SchemaVersion = 1

// envelope is the one JSON object every command prints under --json.
type envelope struct {
	SchemaVersion int        `json:"schema_version"`
	Command       string     `json:"command"`
	OK            bool       `json:"ok"`
	Data          any        `json:"data,omitempty"`
	Error         *errorBody `json:"error,omitempty"`
}

type errorBody struct {
	Message string `json:"message"`
}

// writeJSON prints doc as a single line. HTML escaping is off so paths and URLs read the same as in text output.
func writeJSON(w io.Writer, doc envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}
