// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package identity derives schrodeck's identifiers: host_id (ADR 0010) and a
// member copy's copy_id and canonical folder (ADR 0026, contract D). Every
// value here is derived, never configured or stored as a magic constant.
package identity

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/csmarshall/schrodeck/internal/ports"
)

// namespaceURL is RFC 9562's predefined NameSpace_URL.
var namespaceURL = [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

// NamespaceURLName is the URL from which NAMESPACE_SCHRODECK is derived
// (contract D § identifiers). Changing it changes every copy_id and canonical
// folder, so it is versioned in its last path element.
const NamespaceURLName = "https://github.com/csmarshall/schrodeck/ns/v1"

// NamespaceSchrodeck is NAMESPACE_SCHRODECK = uuid5(NameSpace_URL, NamespaceURLName).
var NamespaceSchrodeck = UUID5(namespaceURL, NamespaceURLName)

// UUID5 returns the RFC 9562 name-based (SHA-1) UUID of name in namespace ns.
func UUID5(ns [16]byte, name string) [16]byte {
	h := sha1.New()
	h.Write(ns[:])
	h.Write([]byte(name))
	var u [16]byte
	copy(u[:], h.Sum(nil))
	u[6] = (u[6] & 0x0f) | 0x50 // version 5
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 9562 variant
	return u
}

// String renders a UUID in the canonical lower-case 8-4-4-4-12 form.
func String(u [16]byte) string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// HostIDLength is the number of hex characters of host_id (ADR 0010).
const HostIDLength = 12

// HostID derives host_id = sha256(HardwareID + ":" + UserName)[:12]. The
// hardware id itself is never returned, logged or stored.
func HostID(h ports.HostIdentity) (string, error) {
	hw, err := h.HardwareID()
	if err != nil {
		return "", fmt.Errorf("host identity: %w", err)
	}
	user := h.UserName()
	if hw == "" || user == "" {
		return "", fmt.Errorf("host identity: hardware id or user name is empty")
	}
	sum := sha256.Sum256([]byte(hw + ":" + user))
	return hex.EncodeToString(sum[:])[:HostIDLength], nil
}

// CopyID names one member copy in the store:
// uuid5(NAMESPACE_SCHRODECK, host_id + ":" + profile_id + ":" + deck_key).
func CopyID(hostID, profileID, deckKey string) string {
	return String(UUID5(NamespaceSchrodeck, hostID+":"+profileID+":"+deckKey))
}

// CanonicalFolder is the member copy's folder in ProfilesV3:
// uuid5(NAMESPACE_SCHRODECK, profile_id + ":" + deck_key), upper-case as the
// app names its folders, plus ".sdProfile".
func CanonicalFolder(profileID, deckKey string) string {
	return strings.ToUpper(String(UUID5(NamespaceSchrodeck, profileID+":"+deckKey))) + ".sdProfile"
}
