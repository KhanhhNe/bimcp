package main

import "testing"

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
