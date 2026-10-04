// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command archcheck enforces schrodeck's import rules.
//
//	archcheck -mode core -dir .                          core is OS-free (ADR 0018)
//	archcheck -mode boundary -dir deckformat -forbid .   deckformat imports no schrodeck package (ADR 0031)
//	archcheck -print-goos                                the GOOS list, one per line, for CI's cross-build loop
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/csmarshall/schrodeck/tools/archcheck"
)

// goosList is the one home of the operating systems we keep buildable: a darwin-only file is invisible to `go list` on Linux, so every check lists once per OS, and CI's cross-build reads the list through -print-goos.
var goosList = []string{"darwin", "linux", "windows"}

func main() {
	mode := flag.String("mode", "", "core | boundary")
	dir := flag.String("dir", ".", "module directory to check")
	forbid := flag.String("forbid", ".", "boundary mode: directory of the module that must not be depended on")
	printGOOS := flag.Bool("print-goos", false, "print the GOOS list, one per line, and exit")
	flag.Parse()

	if *printGOOS {
		for _, goos := range goosList {
			fmt.Println(goos)
		}
		return
	}

	var violations []string
	switch *mode {
	case "core":
		module, err := archcheck.ModulePath(*dir)
		fail(err)
		violations = checkPerGOOS(*dir, []string{"./..."}, func(pkgs []archcheck.Package) []string {
			if !anyInModule(pkgs, module) {
				fail(fmt.Errorf("no package of module %s was listed in %s, so nothing was checked", module, *dir))
			}
			return archcheck.CoreViolations(pkgs, module)
		})
	case "boundary":
		forbidden, err := archcheck.ModulePath(*forbid)
		fail(err)
		violations = checkPerGOOS(*dir, []string{"-deps", "-test", "./..."}, func(pkgs []archcheck.Package) []string {
			if len(pkgs) == 0 {
				fail(fmt.Errorf("no package was listed in %s, so nothing was checked", *dir))
			}
			return archcheck.BoundaryViolations(pkgs, forbidden)
		})
	default:
		fmt.Fprintln(os.Stderr, "archcheck: -mode must be core or boundary")
		os.Exit(2)
	}
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v)
	}
	if len(violations) > 0 {
		os.Exit(1)
	}
	fmt.Printf("archcheck %s: ok (%v)\n", *mode, goosList)
}

// checkPerGOOS lists the packages selected by listArgs once per GOOS and returns what check reports for each, prefixed with the GOOS.
func checkPerGOOS(dir string, listArgs []string, check func([]archcheck.Package) []string) []string {
	var violations []string
	for _, goos := range goosList {
		pkgs, err := archcheck.List(dir, []string{"GOOS=" + goos}, listArgs...)
		fail(err)
		for _, v := range check(pkgs) {
			violations = append(violations, "GOOS="+goos+": "+v)
		}
	}
	return violations
}

func anyInModule(pkgs []archcheck.Package, module string) bool {
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Path == module {
			return true
		}
	}
	return false
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "archcheck:", err)
		os.Exit(2)
	}
}
