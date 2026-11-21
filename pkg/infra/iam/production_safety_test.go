package iam

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionIAMHasNoEmbeddedCatalogOrLegacyMutationCalls(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, group := range file.Comments {
			for _, c := range group.List {
				if strings.Contains(c.Text, "go:embed") {
					t.Errorf("production IAM embeds a permission catalog: %s", path)
				}
			}
		}
		for _, imp := range file.Imports {
			if strings.Contains(imp.Path.Value, "platform-api/api/private") {
				t.Errorf("production CLI imports private API: %s", path)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.BasicLit:
				if strings.Contains(n.Value, "roles/compute.") || strings.Contains(n.Value, "roles/dns.") || strings.Contains(n.Value, "roles/storage.admin") || strings.Contains(n.Value, "iam-bindings.json") {
					t.Errorf("embedded broad permission fallback: %s", path)
				}
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && (strings.Contains(sel.Sel.Name, "Undelete") || sel.Sel.Name == "AddProjectIAMRoles") {
					t.Errorf("legacy mutation call is reachable: %s", path)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
