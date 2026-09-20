package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

func TestEmitTableUsesOutputPath(t *testing.T) {
	outputPath := t.TempDir()

	if err := emitTable(outputPath, tableMetadata{name: "financials"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(outputPath, "financials")); err != nil {
		t.Fatalf("stat financials output directory: %v", err)
	}
}

func TestAssociateTablesLinksVariationHierarchyToSourceColumn(t *testing.T) {
	tables := []tableMetadata{
		{
			name: "financials",
			columns: []columnMetadata{{
				name: "Date",
				variations: []variationMetadata{{
					name:             "Variation",
					isDefault:        true,
					defaultHierarchy: &hierarchyReference{table: "LocalDateTable_8aa93495-4c36-404c-b2a5-723254079f25", hierarchy: "Date Hierarchy"},
				}},
			}},
		},
		{name: "LocalDateTable_8aa93495-4c36-404c-b2a5-723254079f25", isHidden: true},
	}

	associated := associateTables(tables)

	got := associated[1].associatedColumns
	want := columnReference{table: "financials", column: "Date"}
	if len(got) != 1 || !sameColumnReference(got[0], want) {
		t.Fatalf("associated columns = %#v, want %#v", got, want)
	}
	if role := associated[1].tomRole(); role != "hidden TOM table" {
		t.Fatalf("tomRole() = %q, want hidden TOM table", role)
	}
}

func TestAssociateTablesLinksCalculatedTableExpressionToSourceColumn(t *testing.T) {
	tables := []tableMetadata{
		{name: "financials", columns: []columnMetadata{{name: "Date"}}},
		{
			name:                  "LocalDateTable_8aa93495-4c36-404c-b2a5-723254079f25",
			isHidden:              true,
			showAsVariationsOnly:  true,
			calculatedExpressions: []string{"CALENDAR(MIN('financials'[Date]), MAX('financials'[Date]))"},
		},
	}

	associated := associateTables(tables)

	got := associated[1].associatedColumns
	want := columnReference{table: "financials", column: "Date"}
	if len(got) != 1 || !sameColumnReference(got[0], want) {
		t.Fatalf("associated columns = %#v, want %#v", got, want)
	}
}

func TestDAXColumnReferencesHandlesEscapedTableNamesAndDuplicates(t *testing.T) {
	references := daxColumnReferences("MIN('Bob''s Sales'[Order Date]) + MAX('Bob''s Sales'[Order Date]) + MAX(financials[Close]]])")

	want := []columnReference{
		{table: "Bob's Sales", column: "Order Date"},
		{table: "financials", column: "Close]"},
	}
	if len(references) != len(want) {
		t.Fatalf("daxColumnReferences() = %#v, want %#v", references, want)
	}
	for index := range want {
		if !sameColumnReference(references[index], want[index]) {
			t.Fatalf("daxColumnReferences() = %#v, want %#v", references, want)
		}
	}
}

func TestAssociateTablesDoesNotReverseOrdinaryCalculatedTableDependencies(t *testing.T) {
	tables := []tableMetadata{
		{name: "financials", columns: []columnMetadata{{name: "Date"}}},
		{
			name:                  "Summary",
			calculatedExpressions: []string{"SUMMARIZE('financials', 'financials'[Date])"},
		},
	}

	associated := associateTables(tables)

	if got := associated[1].associatedColumns; len(got) != 0 {
		t.Fatalf("associated columns = %#v, want none", got)
	}
}

func TestTimeCallsRunsAllConcurrentCalls(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	done := make(chan timedRun, 1)

	go func() {
		done <- timeCalls([]string{"a", "b", "c"}, true, func(int) error {
			started <- struct{}{}
			<-release
			return nil
		})
	}()

	for range 3 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("concurrent calls did not all start")
		}
	}
	close(release)
	run := <-done

	if run.peakInFlight != 3 {
		t.Fatalf("peak in-flight calls = %d, want 3", run.peakInFlight)
	}
}

func TestParseWorkerCountsSortsAndDeduplicates(t *testing.T) {
	got, err := parseWorkerCounts("8, 2,4,2")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{2, 4, 8}
	if len(got) != len(want) {
		t.Fatalf("parseWorkerCounts() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("parseWorkerCounts() = %v, want %v", got, want)
		}
	}
}

func TestParseWorkerCountsRejectsInvalidValues(t *testing.T) {
	for _, spec := range []string{"", "0", "2,nope"} {
		if _, err := parseWorkerCounts(spec); err == nil {
			t.Fatalf("parseWorkerCounts(%q) succeeded, want error", spec)
		}
	}
}
