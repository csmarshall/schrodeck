// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command portcheck fails when internal/ports and contract A disagree.
// Usage: go run ./tools/portcheck/cmd/portcheck -doc docs/contracts/os-connector.md -src internal/ports
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/csmarshall/schrodeck/tools/portcheck"
)

func main() {
	docPath := flag.String("doc", "docs/contracts/os-connector.md", "contract A document")
	srcDir := flag.String("src", "internal/ports", "package directory holding the port interfaces")
	flag.Parse()

	md, err := os.ReadFile(*docPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "portcheck:", err)
		os.Exit(2)
	}
	doc, err := portcheck.FromDoc(md)
	if err != nil {
		fmt.Fprintln(os.Stderr, "portcheck:", err)
		os.Exit(2)
	}
	src, err := portcheck.FromSource(*srcDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "portcheck:", err)
		os.Exit(2)
	}
	if len(doc) == 0 {
		fmt.Fprintln(os.Stderr, "portcheck: no ports found in", *docPath)
		os.Exit(2)
	}
	diffs := portcheck.Compare(doc, src)
	for _, d := range diffs {
		fmt.Fprintln(os.Stderr, d)
	}
	if len(diffs) > 0 {
		os.Exit(1)
	}
	ifaces, structs := portcheck.Count(doc)
	fmt.Printf("portcheck: %d ports and %d structs match contract A\n", ifaces, structs)
}
