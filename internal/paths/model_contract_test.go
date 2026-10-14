package paths

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The return-value model is valid only while production callers supply trusted roots.
func TestPhysicalPathModelCallersUseTrustedRoots(t *testing.T) {
	checked := 0
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join("..", "..", dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			alias := ""
			for _, imp := range file.Imports {
				pkg, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if pkg == "app/internal/paths" {
					alias = "paths"
					if imp.Name != nil {
						alias = imp.Name.Name
					}
					if alias == "." {
						t.Errorf("%s: dot imports defeat the model caller contract", path)
					}
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				target := false
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					owner, ok := selector.X.(*ast.Ident)
					target = ok && owner.Name == alias && selector.Sel.Name == "ResolveExistingUnder"
				} else if ident, ok := call.Fun.(*ast.Ident); ok {
					target = file.Name.Name == "paths" && ident.Name == "ResolveExistingUnder"
				}
				if !target {
					return true
				}
				checked++
				trusted := false
				if len(call.Args) == 2 {
					if root, ok := call.Args[0].(*ast.CallExpr); ok && len(root.Args) == 0 {
						if selector, ok := root.Fun.(*ast.SelectorExpr); ok {
							owner, ok := selector.X.(*ast.Ident)
							trusted = ok && owner.Name == alias && (selector.Sel.Name == "BackupDir" || selector.Sel.Name == "ExportDir")
						} else if ident, ok := root.Fun.(*ast.Ident); ok {
							trusted = file.Name.Name == "paths" && (ident.Name == "BackupDir" || ident.Name == "ExportDir")
						}
					}
				}
				if !trusted {
					t.Errorf("%s: physical-path model requires a reviewed BackupDir/ExportDir root", path)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked == 0 {
		t.Fatal("no production callers checked; source layout or contract needs review")
	}
}
