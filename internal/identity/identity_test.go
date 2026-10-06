// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package identity

import (
	"strings"
	"testing"

	"github.com/csmarshall/schrodeck/internal/ports/fake"
)

// Golden values were computed independently with Python's uuid and hashlib
// modules (not with this package):
//
//	ns = uuid.uuid5(uuid.NAMESPACE_URL, "https://github.com/csmarshall/schrodeck/ns/v1")
//	uuid.uuid5(ns, "0123456789ab:" + PROFILE + ":" + DECK)
//	uuid.uuid5(ns, PROFILE + ":" + DECK)
//	hashlib.sha256(b"HW-TEST-0001:alice").hexdigest()[:12]
const (
	profileID = "11111111-1111-4111-8111-111111111111"
	deckKey   = "@(1)[4057/143/<deck>]"
)

func TestNamespaceIsDerivedFromItsURL(t *testing.T) {
	if got := String(NamespaceSchrodeck); got != "5a7d742c-c29c-52c8-b996-ed8ccdcb83f8" {
		t.Fatalf("NAMESPACE_SCHRODECK = %s", got)
	}
}

func TestCopyID(t *testing.T) {
	if got := CopyID("0123456789ab", profileID, deckKey); got != "f0686204-f3d4-53e0-bf6b-f16dd1fd2232" {
		t.Fatalf("CopyID = %s", got)
	}
	// Each part of the key matters (setup, Mac, deck).
	base := CopyID("0123456789ab", profileID, deckKey)
	for _, other := range []string{
		CopyID("0123456789ac", profileID, deckKey),
		CopyID("0123456789ab", "21111111-1111-4111-8111-111111111111", deckKey),
		CopyID("0123456789ab", profileID, "@(1)[4057/99/<deck>]"),
	} {
		if other == base {
			t.Fatal("changing one part of (host, profile, deck) kept the copy_id")
		}
	}
}

func TestCanonicalFolder(t *testing.T) {
	if got := CanonicalFolder(profileID, deckKey); got != "BA653531-BDCB-5CA9-907A-EB0FF57A17DF.sdProfile" {
		t.Fatalf("CanonicalFolder = %s", got)
	}
}

func TestHostID(t *testing.T) {
	got, err := HostID(fake.HostIdentity{Hardware: "HW-TEST-0001", User: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "7db4c9ba0faa" {
		t.Fatalf("HostID = %s", got)
	}
	other, _ := HostID(fake.HostIdentity{Hardware: "HW-TEST-0001", User: "bob"})
	if other == got {
		t.Fatal("two users on one Mac must be two hosts (ADR 0010)")
	}
	if _, err := HostID(fake.HostIdentity{User: "alice"}); err == nil {
		t.Fatal("missing hardware id accepted")
	}
	// Contract D: host_id is lower-case text.
	if got != strings.ToLower(got) {
		t.Fatalf("host_id %q is not lower case", got)
	}
	// Known-bad: an empty user name would make every user on the Mac one host.
	if _, err := HostID(fake.HostIdentity{Hardware: "HW-TEST-0001"}); err == nil {
		t.Fatal("empty user name accepted")
	}
}
