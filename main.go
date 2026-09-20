package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bimcp/tom"
)

const modelPollInterval = time.Second

func main() {
	var outputPath string
	var concurrencyTest bool
	var concurrencyIterations int
	var concurrencyWorkers string
	var concurrencyDetails bool
	flag.StringVar(&outputPath, "output-path", ".", "directory where table folders are created")
	flag.StringVar(&outputPath, "o", ".", "directory where table folders are created")
	flag.BoolVar(&concurrencyTest, "concurrency-test", false, "run the TOM concurrency benchmark suite, then exit")
	flag.IntVar(&concurrencyIterations, "concurrency-iterations", 1000, "TOM calls per worker in concurrency test mode")
	flag.StringVar(&concurrencyWorkers, "concurrency-workers", "2,4,8,16,32", "comma-separated worker counts for concurrency tests")
	flag.BoolVar(&concurrencyDetails, "concurrency-details", false, "print per-worker timing details")
	flag.Parse()

	client, err := tom.Open("")
	if err != nil {
		log.Fatal(err)
	}

	instances, err := client.Discover()
	if err != nil {
		log.Fatal(err)
	}
	if len(instances) == 0 {
		log.Fatal("no open Power BI Desktop instance found")
	}
	fmt.Printf("Instances count %d\n", len(instances))
	fmt.Printf("Using instance %s\n", instances[0].Endpoint)

	server, err := client.Connect(instances[0].Endpoint)
	if err != nil {
		log.Fatal(err)
	}
	defer server.Release()

	typedServer := tom.AsServer(server)
	databases, err := typedServer.Databases()
	if err != nil {
		log.Fatal(err)
	}
	databaseValue, err := databases.Index(0)
	if err != nil {
		log.Fatal(err)
	}
	database := tom.AsDatabase(databaseValue)
	if concurrencyTest {
		if err := testTOMConcurrency(database, concurrencyIterations, concurrencyWorkers, concurrencyDetails); err != nil {
			log.Fatal(err)
		}
		return
	}
	tables, err := loadTOMTables(database)
	if err != nil {
		log.Fatal(err)
	}

	for _, table := range tables {
		if err := emitTable(outputPath, table); err != nil {
			log.Fatal(err)
		}
	}

	fmt.Printf("Watching Power BI model with %d tables\n", len(tables))
	known := tableNames(tables)
	ticker := time.NewTicker(modelPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		tables, err := loadTOMTables(database)
		if err != nil {
			log.Printf("refresh Power BI model: %v", err)
			continue
		}
		for _, table := range newTables(known, tables) {
			if err := emitTable(outputPath, table); err != nil {
				log.Printf("detect table %q: %v", table.name, err)
			}
		}
		known = tableNames(tables)
	}
}

type tableMetadata struct {
	name                  string
	isHidden              bool
	isPrivate             bool
	showAsVariationsOnly  bool
	columns               []columnMetadata
	measures              []string
	calculatedExpressions []string
	associatedColumns     []columnReference
}

type columnMetadata struct {
	name                    string
	attributeHierarchyState string
	isAvailableInMDX        bool
	variations              []variationMetadata
}

type variationMetadata struct {
	name             string
	isDefault        bool
	defaultColumn    *columnReference
	defaultHierarchy *hierarchyReference
	relationship     []columnReference
}

type columnReference struct {
	table  string
	column string
}

type hierarchyReference struct {
	table     string
	hierarchy string
}

type timedCall struct {
	label     string
	startedAt time.Time
	endedAt   time.Time
	err       error
}

type timedRun struct {
	calls        []timedCall
	elapsed      time.Duration
	peakInFlight int
}

type concurrencyBenchmark struct {
	name   string
	labels []string
	call   func(index int) error
}

func testTOMConcurrency(database tom.Database, iterations int, workerSpec string, details bool) error {
	if iterations < 1 {
		return fmt.Errorf("concurrency iterations must be at least 1; got %d", iterations)
	}
	workerCounts, err := parseWorkerCounts(workerSpec)
	if err != nil {
		return err
	}
	if err := database.Refresh(); err != nil {
		return err
	}
	model, err := database.Model()
	if err != nil {
		return err
	}
	tableItems, err := model.TableItems(0)
	if err != nil {
		return err
	}

	var tableValues []tom.Table
	var tableLabels []string
	var columnCollections []tom.ColumnCollection
	var columns []tom.Column
	var columnLabels []string
	for _, table := range tableItems {
		table, err := table.Snapshot()
		if err != nil {
			return fmt.Errorf("read table properties: %w", err)
		}
		tableValues = append(tableValues, table)
		tableLabels = append(tableLabels, table.Name)
		columnCollection, err := table.Columns()
		if err != nil {
			return fmt.Errorf("get columns for table %q: %w", table.Name, err)
		}
		columnCollections = append(columnCollections, columnCollection)
		columnItems, err := table.ColumnItems(0)
		if err != nil {
			return fmt.Errorf("enumerate columns for table %q: %w", table.Name, err)
		}
		for _, column := range columnItems {
			snapshot, err := column.Snapshot()
			if err != nil {
				return fmt.Errorf("warm column snapshot for table %q: %w", table.Name, err)
			}
			columns = append(columns, column)
			columnLabels = append(columnLabels, table.Name+"."+snapshot.Name)
		}
	}
	if len(columns) < 2 {
		return fmt.Errorf("concurrency test requires at least two columns; found %d", len(columns))
	}

	benchmarks := []concurrencyBenchmark{
		{
			name:   "distinct column snapshot",
			labels: columnLabels,
			call: func(index int) error {
				_, err := columns[index%len(columns)].Snapshot()
				return err
			},
		},
		{
			name:   "shared column snapshot",
			labels: []string{columnLabels[0]},
			call: func(int) error {
				_, err := columns[0].Snapshot()
				return err
			},
		},
		{
			name:   "distinct column Name get",
			labels: columnLabels,
			call: func(index int) error {
				var name string
				return columns[index%len(columns)].TOMValue().Get("Name", &name)
			},
		},
		{
			name:   "table snapshot",
			labels: tableLabels,
			call: func(index int) error {
				_, err := tableValues[index%len(tableValues)].Snapshot()
				return err
			},
		},
		{
			name:   "column collection items",
			labels: tableLabels,
			call: func(index int) error {
				_, err := columnCollections[index%len(columnCollections)].Items(0)
				return err
			},
		},
		{
			name:   "mixed metadata reads",
			labels: columnLabels,
			call: func(index int) error {
				switch index % 3 {
				case 0:
					_, err := columns[index%len(columns)].Snapshot()
					return err
				case 1:
					var name string
					return columns[index%len(columns)].TOMValue().Get("Name", &name)
				default:
					_, err := columnCollections[index%len(columnCollections)].Items(0)
					return err
				}
			},
		},
	}

	fmt.Printf(
		"Testing %d warmed columns and %d tables with %d calls per worker\n",
		len(columns),
		len(tableValues),
		iterations,
	)
	fmt.Printf("%-27s %7s %12s %12s %9s %8s\n", "Case", "Workers", "Sequential", "Concurrent", "Speedup", "Peak")
	for _, benchmark := range benchmarks {
		for _, workers := range workerCounts {
			if err := runConcurrencyBenchmark(benchmark, workers, iterations, details); err != nil {
				return err
			}
		}
	}
	return nil
}

func runConcurrencyBenchmark(benchmark concurrencyBenchmark, workers, iterations int, details bool) error {
	labels := make([]string, workers)
	for index := range labels {
		labels[index] = benchmark.labels[index%len(benchmark.labels)]
	}
	call := func(index int) error {
		for range iterations {
			if err := benchmark.call(index); err != nil {
				return err
			}
		}
		return nil
	}
	sequential := timeCalls(labels, false, call)
	concurrent := timeCalls(labels, true, call)

	speedup := float64(sequential.elapsed) / float64(concurrent.elapsed)
	fmt.Printf(
		"%-27s %7d %12s %12s %8.2fx %8d\n",
		benchmark.name,
		workers,
		sequential.elapsed.Round(time.Microsecond),
		concurrent.elapsed.Round(time.Microsecond),
		speedup,
		concurrent.peakInFlight,
	)
	if details {
		printTimingRun("Concurrent worker details", concurrent, true)
	}
	return timingErrors(sequential, concurrent)
}

func parseWorkerCounts(spec string) ([]int, error) {
	var result []int
	seen := make(map[int]struct{})
	for _, part := range strings.Split(spec, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < 1 {
			return nil, fmt.Errorf("invalid concurrency worker count %q", part)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, errors.New("at least one concurrency worker count is required")
	}
	sort.Ints(result)
	return result, nil
}

func timeCalls(labels []string, concurrent bool, call func(index int) error) timedRun {
	result := timedRun{calls: make([]timedCall, len(labels))}
	startedAt := time.Now()
	if !concurrent {
		for index, label := range labels {
			result.calls[index] = executeTimedCall(label, func() error { return call(index) })
		}
		result.elapsed = time.Since(startedAt)
		return result
	}

	start := make(chan struct{})
	var ready sync.WaitGroup
	var complete sync.WaitGroup
	var inFlight atomic.Int64
	var peakInFlight atomic.Int64
	ready.Add(len(labels))
	complete.Add(len(labels))
	for index, label := range labels {
		go func() {
			defer complete.Done()
			ready.Done()
			<-start
			current := inFlight.Add(1)
			updatePeak(&peakInFlight, current)
			result.calls[index] = executeTimedCall(label, func() error { return call(index) })
			inFlight.Add(-1)
		}()
	}
	ready.Wait()
	startedAt = time.Now()
	close(start)
	complete.Wait()
	result.elapsed = time.Since(startedAt)
	result.peakInFlight = int(peakInFlight.Load())
	return result
}

func updatePeak(peak *atomic.Int64, candidate int64) {
	for {
		current := peak.Load()
		if candidate <= current || peak.CompareAndSwap(current, candidate) {
			return
		}
	}
}

func executeTimedCall(label string, call func() error) timedCall {
	result := timedCall{label: label, startedAt: time.Now()}
	result.err = call()
	result.endedAt = time.Now()
	return result
}

func printTimingRun(name string, run timedRun, details bool) {
	var sum time.Duration
	for _, call := range run.calls {
		sum += call.endedAt.Sub(call.startedAt)
	}
	fmt.Printf("%s: wall=%s, sum of call durations=%s\n", name, run.elapsed, sum)
	if !details {
		return
	}

	origin := earliestCallStart(run.calls)
	for _, call := range run.calls {
		status := "ok"
		if call.err != nil {
			status = call.err.Error()
		}
		fmt.Printf(
			"  %-40s start=+%-10s end=+%-10s duration=%-10s %s\n",
			call.label,
			call.startedAt.Sub(origin),
			call.endedAt.Sub(origin),
			call.endedAt.Sub(call.startedAt),
			status,
		)
	}
}

func earliestCallStart(calls []timedCall) time.Time {
	earliest := calls[0].startedAt
	for _, call := range calls[1:] {
		if call.startedAt.Before(earliest) {
			earliest = call.startedAt
		}
	}
	return earliest
}

func timingErrors(runs ...timedRun) error {
	var messages []string
	for _, run := range runs {
		for _, call := range run.calls {
			if call.err != nil {
				messages = append(messages, fmt.Sprintf("%s: %v", call.label, call.err))
			}
		}
	}
	if len(messages) > 0 {
		return fmt.Errorf("column snapshot failures: %s", strings.Join(messages, "; "))
	}
	return nil
}

func loadTOMTables(database tom.Database) ([]tableMetadata, error) {
	if err := database.Refresh(); err != nil {
		return nil, err
	}
	model, err := database.Model()
	if err != nil {
		return nil, err
	}
	return readTOMTables(model)
}

func readTOMTables(model tom.Model) ([]tableMetadata, error) {
	tableItems, err := model.TableItems(0)
	if err != nil {
		return nil, err
	}

	result := make([]tableMetadata, 0, len(tableItems))
	for _, table := range tableItems {
		table, err = table.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("read table properties: %w", err)
		}
		name := table.Name
		columnItems, err := table.ColumnItems(0)
		if err != nil {
			return nil, fmt.Errorf("enumerate columns for table %q: %w", name, err)
		}
		measureItems, err := table.MeasureItems(0)
		if err != nil {
			return nil, fmt.Errorf("enumerate measures for table %q: %w", name, err)
		}

		metadata := tableMetadata{
			name:                 name,
			isHidden:             table.IsHidden,
			isPrivate:            table.IsPrivate,
			showAsVariationsOnly: table.ShowAsVariationsOnly,
		}
		for _, column := range columnItems {
			column, err = column.Snapshot()
			if err != nil {
				return nil, fmt.Errorf("read column properties for table %q: %w", name, err)
			}
			columnName := column.Name
			columnMetadata, err := readTOMColumn(name, columnName, column)
			if err != nil {
				return nil, err
			}
			metadata.columns = append(metadata.columns, columnMetadata)
		}
		for _, measure := range measureItems {
			measure, err := measure.Snapshot()
			if err != nil {
				return nil, fmt.Errorf("read measure properties for table %q: %w", name, err)
			}
			metadata.measures = append(metadata.measures, measure.Name)
		}
		metadata.calculatedExpressions, err = readCalculatedExpressions(name, table)
		if err != nil {
			return nil, err
		}
		result = append(result, metadata)
	}
	return associateTables(result), nil
}

func readTOMColumn(tableName, columnName string, column tom.Column) (columnMetadata, error) {
	metadata := columnMetadata{name: columnName, isAvailableInMDX: column.IsAvailableInMDX}

	attributeHierarchy, err := column.AttributeHierarchy()
	if err != nil {
		return metadata, fmt.Errorf("get attribute hierarchy for column %q.%q: %w", tableName, columnName, err)
	}
	if attributeHierarchy.TOMValue().Handle != 0 {
		attributeHierarchy, err = attributeHierarchy.Snapshot()
		if err != nil {
			return metadata, fmt.Errorf("read attribute hierarchy properties for column %q.%q: %w", tableName, columnName, err)
		}
		metadata.attributeHierarchyState = string(attributeHierarchy.State)
	}

	variationItems, err := column.VariationItems(0)
	if err != nil {
		return metadata, fmt.Errorf("enumerate variations for column %q.%q: %w", tableName, columnName, err)
	}
	for _, item := range variationItems {
		variation, err := readTOMVariation(tableName, columnName, item)
		if err != nil {
			return metadata, err
		}
		metadata.variations = append(metadata.variations, variation)
	}
	return metadata, nil
}

func readTOMVariation(tableName, columnName string, variation tom.Variation) (variationMetadata, error) {
	variation, err := variation.Snapshot()
	if err != nil {
		return variationMetadata{}, fmt.Errorf("read variation properties for column %q.%q: %w", tableName, columnName, err)
	}
	name := variation.Name
	metadata := variationMetadata{name: name, isDefault: variation.IsDefault}

	defaultColumn, err := variation.DefaultColumn()
	if err != nil {
		return metadata, fmt.Errorf("get default column for variation %q on %q.%q: %w", name, tableName, columnName, err)
	}
	if defaultColumn.TOMValue().Handle != 0 {
		reference, err := tomColumnReference(defaultColumn)
		if err != nil {
			return metadata, fmt.Errorf("read default column for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		metadata.defaultColumn = &reference
	}

	defaultHierarchy, err := variation.DefaultHierarchy()
	if err != nil {
		return metadata, fmt.Errorf("get default hierarchy for variation %q on %q.%q: %w", name, tableName, columnName, err)
	}
	if defaultHierarchy.TOMValue().Handle != 0 {
		defaultHierarchy, err = defaultHierarchy.Snapshot()
		if err != nil {
			return metadata, fmt.Errorf("read default hierarchy properties for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		hierarchyName := defaultHierarchy.Name
		hierarchyTable, err := defaultHierarchy.Table()
		if err != nil {
			return metadata, fmt.Errorf("get default hierarchy table for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		hierarchyTable, err = hierarchyTable.Snapshot()
		if err != nil {
			return metadata, fmt.Errorf("read default hierarchy table properties for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		metadata.defaultHierarchy = &hierarchyReference{table: hierarchyTable.Name, hierarchy: hierarchyName}
	}

	relationship, err := variation.Relationship()
	if err != nil {
		return metadata, fmt.Errorf("get relationship for variation %q on %q.%q: %w", name, tableName, columnName, err)
	}
	if relationship.TOMValue().Handle != 0 {
		relationship, err = relationship.Snapshot()
		if err != nil {
			return metadata, fmt.Errorf("read relationship properties for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		if relationship.Type != tom.RelationshipTypeSingleColumn {
			return metadata, nil
		}
		singleColumn := tom.AsSingleColumnRelationship(relationship.TOMValue())
		fromColumn, err := singleColumn.FromColumn()
		if err != nil {
			return metadata, fmt.Errorf("get relationship source for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		toColumn, err := singleColumn.ToColumn()
		if err != nil {
			return metadata, fmt.Errorf("get relationship target for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		for _, relatedColumn := range []tom.Column{fromColumn, toColumn} {
			reference, err := tomColumnReference(relatedColumn)
			if err != nil {
				return metadata, fmt.Errorf("read relationship column for variation %q on %q.%q: %w", name, tableName, columnName, err)
			}
			metadata.relationship = appendUniqueColumnReference(metadata.relationship, reference)
		}
	}
	return metadata, nil
}

func tomColumnReference(column tom.Column) (columnReference, error) {
	column, err := column.Snapshot()
	if err != nil {
		return columnReference{}, err
	}
	table, err := column.Table()
	if err != nil {
		return columnReference{}, err
	}
	table, err = table.Snapshot()
	if err != nil {
		return columnReference{}, err
	}
	return columnReference{table: table.Name, column: column.Name}, nil
}

func readCalculatedExpressions(tableName string, table tom.Table) ([]string, error) {
	partitionItems, err := table.PartitionItems(0)
	if err != nil {
		return nil, fmt.Errorf("enumerate partitions for table %q: %w", tableName, err)
	}
	var expressions []string
	for _, partition := range partitionItems {
		partition, err = partition.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("read partition properties for table %q: %w", tableName, err)
		}
		if partition.SourceType != tom.PartitionSourceTypeCalculated {
			continue
		}
		source, err := partition.Source()
		if err != nil {
			return nil, fmt.Errorf("get calculated partition source for table %q: %w", tableName, err)
		}
		calculatedSource, err := tom.AsCalculatedPartitionSource(source.TOMValue()).Snapshot()
		if err != nil {
			return nil, fmt.Errorf("read calculated partition source properties for table %q: %w", tableName, err)
		}
		expressions = append(expressions, calculatedSource.Expression)
	}
	return expressions, nil
}

func emitTable(outputPath string, table tableMetadata) error {
	tablePath := filepath.Join(outputPath, table.name)
	if err := os.MkdirAll(tablePath, 0755); err != nil {
		return fmt.Errorf("create folder for table %q: %w", table.name, err)
	}
	fmt.Printf("Table: %s [%s] (%d columns, %d measures)\n", table.name, table.tomRole(), len(table.columns), len(table.measures))
	for _, reference := range table.associatedColumns {
		fmt.Printf("  Used by: %s.%s\n", reference.table, reference.column)
	}
	for _, column := range table.columns {
		fmt.Printf("  Column: %s\n", column.name)
		if len(column.variations) > 0 && column.attributeHierarchyState != "" {
			fmt.Printf("    Attribute hierarchy: %s (available in MDX: %t)\n", column.attributeHierarchyState, column.isAvailableInMDX)
		}
		for _, variation := range column.variations {
			defaultMarker := ""
			if variation.isDefault {
				defaultMarker = " [default]"
			}
			fmt.Printf("    Variation: %s%s\n", variation.name, defaultMarker)
			if variation.defaultHierarchy != nil {
				fmt.Printf("      Default hierarchy: %s.%s\n", variation.defaultHierarchy.table, variation.defaultHierarchy.hierarchy)
			}
			if variation.defaultColumn != nil {
				fmt.Printf("      Default column: %s.%s\n", variation.defaultColumn.table, variation.defaultColumn.column)
			}
		}
	}
	for _, measureName := range table.measures {
		fmt.Printf("  Measure: %s\n", measureName)
	}
	return nil
}

func (table tableMetadata) tomRole() string {
	switch {
	case table.showAsVariationsOnly:
		return "variation-only TOM table"
	case table.isPrivate:
		return "private TOM table"
	case table.isHidden:
		return "hidden TOM table"
	default:
		return "main TOM table"
	}
}

var (
	quotedDAXColumnReferencePattern   = regexp.MustCompile(`'((?:''|[^'])+)'\[((?:\]\]|[^\]])+)\]`)
	unquotedDAXColumnReferencePattern = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\[((?:\]\]|[^\]])+)\]`)
)

func associateTables(tables []tableMetadata) []tableMetadata {
	tableIndexes := make(map[string]int, len(tables))
	for index := range tables {
		tableIndexes[strings.ToLower(tables[index].name)] = index
	}

	for sourceTableIndex := range tables {
		sourceTable := &tables[sourceTableIndex]
		for _, column := range sourceTable.columns {
			source := columnReference{table: sourceTable.name, column: column.name}
			for _, variation := range column.variations {
				if variation.defaultHierarchy != nil {
					associateColumnWithTable(tables, tableIndexes, variation.defaultHierarchy.table, source)
				}
				if variation.defaultColumn != nil {
					associateColumnWithTable(tables, tableIndexes, variation.defaultColumn.table, source)
				}
				for _, related := range variation.relationship {
					if !sameColumnReference(related, source) {
						associateColumnWithTable(tables, tableIndexes, related.table, source)
					}
				}
			}
		}
	}

	for tableIndex := range tables {
		if !tables[tableIndex].isAutoDateTable() {
			continue
		}
		for _, expression := range tables[tableIndex].calculatedExpressions {
			for _, reference := range daxColumnReferences(expression) {
				if _, exists := tableIndexes[strings.ToLower(reference.table)]; exists {
					tables[tableIndex].associatedColumns = appendUniqueColumnReference(tables[tableIndex].associatedColumns, reference)
				}
			}
		}
		sortColumnReferences(tables[tableIndex].associatedColumns)
	}
	return tables
}

func associateColumnWithTable(tables []tableMetadata, indexes map[string]int, tableName string, source columnReference) {
	index, exists := indexes[strings.ToLower(tableName)]
	if !exists || strings.EqualFold(tableName, source.table) {
		return
	}
	tables[index].associatedColumns = appendUniqueColumnReference(tables[index].associatedColumns, source)
}

func daxColumnReferences(expression string) []columnReference {
	var references []columnReference
	for _, match := range quotedDAXColumnReferencePattern.FindAllStringSubmatch(expression, -1) {
		reference := columnReference{
			table:  strings.ReplaceAll(match[1], "''", "'"),
			column: strings.ReplaceAll(match[2], "]]", "]"),
		}
		references = appendUniqueColumnReference(references, reference)
	}
	for _, match := range unquotedDAXColumnReferencePattern.FindAllStringSubmatch(expression, -1) {
		reference := columnReference{
			table:  match[1],
			column: strings.ReplaceAll(match[2], "]]", "]"),
		}
		references = appendUniqueColumnReference(references, reference)
	}
	sortColumnReferences(references)
	return references
}

func (table tableMetadata) isAutoDateTable() bool {
	lowerName := strings.ToLower(table.name)
	return table.showAsVariationsOnly ||
		strings.HasPrefix(lowerName, "localdatetable_") ||
		strings.HasPrefix(lowerName, "datetabletemplate_")
}

func appendUniqueColumnReference(references []columnReference, candidate columnReference) []columnReference {
	for _, reference := range references {
		if sameColumnReference(reference, candidate) {
			return references
		}
	}
	return append(references, candidate)
}

func sameColumnReference(left, right columnReference) bool {
	return strings.EqualFold(left.table, right.table) && strings.EqualFold(left.column, right.column)
}

func sortColumnReferences(references []columnReference) {
	sort.Slice(references, func(left, right int) bool {
		leftName := strings.ToLower(references[left].table + "\x00" + references[left].column)
		rightName := strings.ToLower(references[right].table + "\x00" + references[right].column)
		return leftName < rightName
	})
}

func tableNames(tables []tableMetadata) map[string]struct{} {
	names := make(map[string]struct{}, len(tables))
	for _, table := range tables {
		names[strings.ToLower(table.name)] = struct{}{}
	}
	return names
}

func newTables(known map[string]struct{}, tables []tableMetadata) []tableMetadata {
	result := make([]tableMetadata, 0)
	for _, table := range tables {
		if _, exists := known[strings.ToLower(table.name)]; !exists {
			result = append(result, table)
		}
	}
	return result
}
