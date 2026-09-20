package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmitTableUsesOutputPath(t *testing.T) {
	outputPath := t.TempDir()

	if err := emitTable(outputPath, tableMetadata{name: "financials"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(outputPath, "financials")); err != nil {
		t.Fatalf("stat financials output directory: %v", err)
	}
}

func TestNewTablesDetectsTablesAddedAfterStartup(t *testing.T) {
	known := tableNames([]tableMetadata{{name: "Existing"}})
	tables := []tableMetadata{
		{name: "Existing"},
		{name: "financials"},
	}

	added := newTables(known, tables)

	if len(added) != 1 || added[0].name != "financials" {
		t.Fatalf("newTables() = %#v, want financials", added)
	}
}

func TestNewTablesMatchesNamesCaseInsensitively(t *testing.T) {
	known := tableNames([]tableMetadata{{name: "financials"}})

	added := newTables(known, []tableMetadata{{name: "Financials"}})

	if len(added) != 0 {
		t.Fatalf("newTables() = %#v, want no added tables", added)
	}
}
