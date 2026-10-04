// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package portcheck

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const docTwoPorts = "# A\n\n```go\ntype Paths interface {\n    Home() string\n    LogDir() string\n}\n```\n\ntext\n\n```go\ntype Notifier interface { Notify(n Notification) error }\ntype Notification struct { Title string }\n```\n"

func mustFromDoc(t *testing.T, md string) []Port {
	t.Helper()
	ports, err := FromDoc([]byte(md))
	if err != nil {
		t.Fatalf("FromDoc: %v", err)
	}
	return ports
}

func TestFromDoc(t *testing.T) {
	got := render(mustFromDoc(t, docTwoPorts))
	want := "Notification: Title string | Notifier: Notify(Notification) error | Paths: Home() string; LogDir() string"
	if got != want {
		t.Fatalf("FromDoc = %q, want %q", got, want)
	}
}

func TestFromDocRejectsUnparseableBlock(t *testing.T) {
	if _, err := FromDoc([]byte("```go\ntype Broken interface {\n```\n")); err == nil {
		t.Fatal("expected an error for a go block that does not parse")
	}
}

func writeSource(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ports.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCompareAgreement(t *testing.T) {
	doc := mustFromDoc(t, docTwoPorts)
	src, err := FromSource(writeSource(t, "package ports\ntype Paths interface { Home() string; LogDir() string }\ntype Notifier interface { Notify(note Notification) error }\ntype Notification struct{ Title string }\ntype Helper struct{ X int }\ntype unexported interface{ x() }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if diffs := Compare(doc, src); len(diffs) != 0 {
		t.Fatalf("expected agreement (a renamed parameter, a helper struct and an unexported interface are not differences), got %v", diffs)
	}
}

// Known-bad cases: each must produce exactly the expected difference, naming the port, method, signature or struct that drifted.
func TestCompareDetectsDrift(t *testing.T) {
	doc := mustFromDoc(t, docTwoPorts)
	const paths = "type Paths interface { Home() string; LogDir() string }\n"
	const notifier = "type Notifier interface { Notify(n Notification) error }\n"
	const notification = "type Notification struct{ Title string }\n"
	cases := []struct {
		name string
		code string
		want []string
	}{
		{
			"missing port",
			paths + notification,
			[]string{"interface Notifier is in contract A but not in internal/ports"},
		},
		{
			"extra port",
			paths + notifier + notification + "type Clock interface { Now() int }\n",
			[]string{"interface Clock is in internal/ports but not in contract A"},
		},
		{
			"missing method",
			"type Paths interface { Home() string }\n" + notifier + notification,
			[]string{"interface Paths: contract A [Home() string; LogDir() string], internal/ports [Home() string]"},
		},
		{
			"renamed method",
			"type Paths interface { Home() string; Logs() string }\n" + notifier + notification,
			[]string{"interface Paths: contract A [Home() string; LogDir() string], internal/ports [Home() string; Logs() string]"},
		},
		{
			"changed parameter list",
			paths + "type Notifier interface { Notify(ctx context.Context, n Notification) error }\n" + notification,
			[]string{"interface Notifier: contract A [Notify(Notification) error], internal/ports [Notify(context.Context, Notification) error]"},
		},
		{
			"changed result",
			"type Paths interface { Home() string; LogDir() (string, error) }\n" + notifier + notification,
			[]string{"interface Paths: contract A [Home() string; LogDir() string], internal/ports [Home() string; LogDir() (string, error)]"},
		},
		{
			"struct field type",
			paths + notifier + "type Notification struct{ Title []byte }\n",
			[]string{"struct Notification: contract A [Title string], internal/ports [Title []byte]"},
		},
		{
			"struct missing field",
			paths + notifier + "type Notification struct{}\n",
			[]string{"struct Notification: contract A [Title string], internal/ports []"},
		},
		{
			"documented struct gone",
			paths + notifier,
			[]string{"struct Notification is in contract A but not in internal/ports"},
		},
		{
			"kind changed",
			paths + notifier + "type Notification interface{ Title() string }\n",
			[]string{"Notification is a struct in contract A but a interface in internal/ports"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, err := FromSource(writeSource(t, "package ports\n"+tc.code))
			if err != nil {
				t.Fatal(err)
			}
			if got := Compare(doc, src); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Compare = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCount(t *testing.T) {
	ifaces, structs := Count(mustFromDoc(t, docTwoPorts))
	if ifaces != 2 || structs != 1 {
		t.Fatalf("Count = %d interfaces, %d structs, want 2 and 1", ifaces, structs)
	}
}

// The real check, also run by CI through main.go.
func TestRepositoryPortsMatchContractA(t *testing.T) {
	md, err := os.ReadFile(filepath.Join("..", "..", "docs", "contracts", "os-connector.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := FromDoc(md)
	if err != nil {
		t.Fatal(err)
	}
	src, err := FromSource(filepath.Join("..", "..", "internal", "ports"))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc) == 0 {
		t.Fatal("no ports found in contract A; the parser is broken, not the contract")
	}
	if diffs := Compare(doc, src); len(diffs) != 0 {
		t.Fatalf("internal/ports differs from docs/contracts/os-connector.md:\n%s", strings.Join(diffs, "\n"))
	}
}

func render(ps []Port) string {
	var parts []string
	for _, p := range ps {
		parts = append(parts, p.Name+": "+strings.Join(p.Members, "; "))
	}
	return strings.Join(parts, " | ")
}
