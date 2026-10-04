// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package portcheck compares the port interfaces and structs sketched in contract A (docs/contracts/os-connector.md) with the Go declarations in internal/ports. Contract A is the single source of truth for the port list (ADR 0024); this check is what keeps the code from drifting away from it.
package portcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Kind says what a declaration is.
type Kind string

const (
	Interface Kind = "interface"
	Struct    Kind = "struct"
)

// Port is one declared type from contract A or internal/ports: an interface with its method signatures, or a struct with its fields. Members are rendered without parameter names ("Watch(context.Context, []string, time.Duration) (<-chan Event, error)", "Model string") and sorted, so a changed parameter list or field type is a difference while a renamed parameter is not.
type Port struct {
	Name    string
	Kind    Kind
	Members []string
}

var goBlock = regexp.MustCompile("(?s)```go\n(.*?)```")

// FromDoc returns every interface AND struct declared in the document's ```go blocks.
func FromDoc(md []byte) ([]Port, error) {
	var ports []Port
	for i, m := range goBlock.FindAllSubmatch(md, -1) {
		src := append([]byte("package doc\n"), m[1]...)
		f, err := parser.ParseFile(token.NewFileSet(), fmt.Sprintf("block%d.go", i), src, 0)
		if err != nil {
			return nil, fmt.Errorf("go block %d does not parse: %w", i, err)
		}
		ports = append(ports, declarations(f, false)...)
	}
	return sortPorts(ports), nil
}

// FromSource returns every exported interface AND struct declared in the non-test Go files of dir.
func FromSource(dir string) ([]Port, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var ports []Port
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		ports = append(ports, declarations(f, true)...)
	}
	return sortPorts(ports), nil
}

func declarations(f *ast.File, exportedOnly bool) []Port {
	var ports []Port
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || (exportedOnly && !ts.Name.IsExported()) {
			return true
		}
		switch t := ts.Type.(type) {
		case *ast.InterfaceType:
			p := Port{Name: ts.Name.Name, Kind: Interface}
			for _, field := range t.Methods.List {
				ft, isFunc := field.Type.(*ast.FuncType)
				for _, name := range field.Names {
					if isFunc {
						p.Members = append(p.Members, name.Name+signature(ft))
					} else {
						p.Members = append(p.Members, name.Name+" "+types.ExprString(field.Type))
					}
				}
				if len(field.Names) == 0 { // an embedded interface
					p.Members = append(p.Members, types.ExprString(field.Type))
				}
			}
			sort.Strings(p.Members)
			ports = append(ports, p)
		case *ast.StructType:
			p := Port{Name: ts.Name.Name, Kind: Struct}
			for _, field := range t.Fields.List {
				typ := types.ExprString(field.Type)
				for _, name := range field.Names {
					p.Members = append(p.Members, name.Name+" "+typ)
				}
				if len(field.Names) == 0 { // an embedded field
					p.Members = append(p.Members, typ)
				}
			}
			sort.Strings(p.Members)
			ports = append(ports, p)
		}
		return true
	})
	return ports
}

// signature renders a method's parameter and result types without names.
func signature(ft *ast.FuncType) string {
	list := func(fl *ast.FieldList) []string {
		var out []string
		if fl == nil {
			return out
		}
		for _, field := range fl.List {
			typ := types.ExprString(field.Type)
			n := len(field.Names)
			if n == 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				out = append(out, typ)
			}
		}
		return out
	}
	sig := "(" + strings.Join(list(ft.Params), ", ") + ")"
	switch res := list(ft.Results); len(res) {
	case 0:
	case 1:
		sig += " " + res[0]
	default:
		sig += " (" + strings.Join(res, ", ") + ")"
	}
	return sig
}

func sortPorts(ps []Port) []Port {
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps
}

// Compare returns one line per difference between the documented and the implemented declarations, sorted; an empty result means they agree. Every interface must exist on both sides with the same method signatures. Every struct contract A declares must exist in internal/ports with the same fields; internal/ports may declare further helper structs (e.g. the argument types of a port) that contract A only names.
func Compare(doc, src []Port) []string {
	byName := func(ps []Port) map[string]Port {
		m := map[string]Port{}
		for _, p := range ps {
			m[p.Name] = p
		}
		return m
	}
	d, s := byName(doc), byName(src)
	var diffs []string
	for name, dp := range d {
		sp, ok := s[name]
		switch {
		case !ok:
			diffs = append(diffs, fmt.Sprintf("%s %s is in contract A but not in internal/ports", dp.Kind, name))
		case sp.Kind != dp.Kind:
			diffs = append(diffs, fmt.Sprintf("%s is a %s in contract A but a %s in internal/ports", name, dp.Kind, sp.Kind))
		case strings.Join(dp.Members, "; ") != strings.Join(sp.Members, "; "):
			diffs = append(diffs, fmt.Sprintf("%s %s: contract A [%s], internal/ports [%s]", dp.Kind, name, strings.Join(dp.Members, "; "), strings.Join(sp.Members, "; ")))
		}
	}
	for name, sp := range s {
		if _, ok := d[name]; !ok && sp.Kind == Interface {
			diffs = append(diffs, fmt.Sprintf("interface %s is in internal/ports but not in contract A", name))
		}
	}
	sort.Strings(diffs)
	return diffs
}

// Count returns how many interfaces and structs ps holds.
func Count(ps []Port) (interfaces, structs int) {
	for _, p := range ps {
		if p.Kind == Interface {
			interfaces++
		} else {
			structs++
		}
	}
	return interfaces, structs
}
