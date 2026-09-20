package tom

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var (
	_ func(Model, int) ([]Table, error)   = Model.TableItems
	_ func(Table, int) ([]Column, error)  = Table.ColumnItems
	_ func(Table, int) ([]Measure, error) = Table.MeasureItems
)

func TestExportedDeclarationsHaveDocumentation(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*.go"))
	if err != nil {
		t.Fatal(err)
	}

	fileSet := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, declaration := range file.Decls {
			checkDeclarationDocumentation(t, fileSet, path, declaration)
		}
	}
}

func checkDeclarationDocumentation(t *testing.T, fileSet *token.FileSet, path string, declaration ast.Decl) {
	t.Helper()
	switch typed := declaration.(type) {
	case *ast.FuncDecl:
		if typed.Name.IsExported() && typed.Doc == nil {
			reportMissingDocumentation(t, fileSet, path, typed.Pos(), typed.Name.Name)
		}
	case *ast.GenDecl:
		for _, specification := range typed.Specs {
			switch spec := specification.(type) {
			case *ast.TypeSpec:
				if spec.Name.IsExported() && typed.Doc == nil && spec.Doc == nil {
					reportMissingDocumentation(t, fileSet, path, spec.Pos(), spec.Name.Name)
				}
				if spec.Name.IsExported() {
					checkTypeFields(t, fileSet, path, spec)
				}
			case *ast.ValueSpec:
				for _, name := range spec.Names {
					if name.IsExported() && typed.Doc == nil && spec.Doc == nil {
						reportMissingDocumentation(t, fileSet, path, name.Pos(), name.Name)
					}
				}
			}
		}
	}
}

func checkTypeFields(t *testing.T, fileSet *token.FileSet, path string, spec *ast.TypeSpec) {
	t.Helper()
	var fields *ast.FieldList
	switch typed := spec.Type.(type) {
	case *ast.StructType:
		fields = typed.Fields
	case *ast.InterfaceType:
		fields = typed.Methods
	default:
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			if name.IsExported() && field.Doc == nil && field.Comment == nil {
				reportMissingDocumentation(t, fileSet, path, name.Pos(), spec.Name.Name+"."+name.Name)
			}
		}
	}
}

func reportMissingDocumentation(t *testing.T, fileSet *token.FileSet, path string, position token.Pos, name string) {
	t.Helper()
	line := fileSet.Position(position).Line
	t.Errorf("%s:%d: exported declaration %s has no documentation", filepath.Base(path), line, name)
}
