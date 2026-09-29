package doctor

import (
	"go/ast"
	"sort"
	"strings"
	"testing"
)

func TestRegistryCoversEveryExecdProgram(t *testing.T) {
	registered := map[string]bool{}
	for _, tool := range Tools {
		registered[tool.Name] = true
	}

	files := goFiles(t)
	if len(files) < 50 {
		t.Fatalf("scanned only %d files; the walk is not reaching the source", len(files))
	}
	seenDynamic := map[string]bool{}
	literals := map[string][]string{}

	for _, f := range files {
		rel, src := f.rel, f.file
		ast.Inspect(src, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if arg, ok := execArg(x); ok {
					record(t, rel, arg, registered, literals, seenDynamic)
				}
			case *ast.KeyValueExpr:

				if id, ok := x.Key.(*ast.Ident); !ok || id.Name != "Argv" {
					return true
				}
				if arg, ok := firstElem(x.Value); ok {
					record(t, rel, arg, registered, literals, seenDynamic)
				}
			case *ast.FuncDecl:

				if !strings.HasSuffix(x.Name.Name, "Argv") || x.Body == nil {
					return true
				}
				ast.Inspect(x.Body, func(m ast.Node) bool {
					cl, ok := m.(*ast.CompositeLit)
					if !ok {
						return true
					}
					if arg, ok := firstElem(cl); ok && !holdsArgvs(arg) {
						record(t, rel, arg, registered, literals, seenDynamic)
					}
					return true
				})
			}
			return true
		})
	}

	for site, tools := range dynamicPrograms {
		for _, name := range tools {
			if !registered[name] {
				t.Errorf("%s says it runs %q, which is not in Tools", site, name)
			}
		}
	}

	for site := range dynamicPrograms {
		if !seenDynamic[site] {
			t.Errorf("dynamicPrograms declares %q, but no such exec site was found;"+
				" delete the entry or fix the expression", site)
		}
	}

	if t.Failed() {
		var names []string
		for n := range literals {
			names = append(names, n)
		}
		sort.Strings(names)
		t.Logf("literal program names found in the source: %v", names)
	}
}
