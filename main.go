package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"bimcp/tom"
)

const modelPollInterval = time.Second

func main() {
	var outputPath string
	flag.StringVar(&outputPath, "output-path", ".", "directory where table folders are created")
	flag.StringVar(&outputPath, "o", ".", "directory where table folders are created")
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
	tables, err := model.Tables()
	if err != nil {
		return nil, err
	}
	tableItems, err := tables.Items(0)
	if err != nil {
		return nil, err
	}

	result := make([]tableMetadata, 0, len(tableItems))
	for _, item := range tableItems {
		table := tom.AsTable(item)
		name, err := table.Name()
		if err != nil {
			return nil, err
		}
		isHidden, err := table.IsHidden()
		if err != nil {
			return nil, fmt.Errorf("get hidden state for table %q: %w", name, err)
		}
		isPrivate, err := table.IsPrivate()
		if err != nil {
			return nil, fmt.Errorf("get private state for table %q: %w", name, err)
		}
		showAsVariationsOnly, err := table.ShowAsVariationsOnly()
		if err != nil {
			return nil, fmt.Errorf("get variation-only state for table %q: %w", name, err)
		}
		columns, err := table.Columns()
		if err != nil {
			return nil, fmt.Errorf("get columns for table %q: %w", name, err)
		}
		columnItems, err := columns.Items(0)
		if err != nil {
			return nil, fmt.Errorf("enumerate columns for table %q: %w", name, err)
		}
		measures, err := table.Measures()
		if err != nil {
			return nil, fmt.Errorf("get measures for table %q: %w", name, err)
		}
		measureItems, err := measures.Items(0)
		if err != nil {
			return nil, fmt.Errorf("enumerate measures for table %q: %w", name, err)
		}

		metadata := tableMetadata{
			name:                 name,
			isHidden:             isHidden,
			isPrivate:            isPrivate,
			showAsVariationsOnly: showAsVariationsOnly,
		}
		for _, item := range columnItems {
			column := tom.AsColumn(item)
			columnName, err := column.Name()
			if err != nil {
				return nil, fmt.Errorf("get column name for table %q: %w", name, err)
			}
			columnMetadata, err := readTOMColumn(name, columnName, column)
			if err != nil {
				return nil, err
			}
			metadata.columns = append(metadata.columns, columnMetadata)
		}
		for _, item := range measureItems {
			measureName, err := tom.AsMeasure(item).Name()
			if err != nil {
				return nil, fmt.Errorf("get measure name for table %q: %w", name, err)
			}
			metadata.measures = append(metadata.measures, measureName)
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
	metadata := columnMetadata{name: columnName}
	isAvailableInMDX, err := column.IsAvailableInMDX()
	if err != nil {
		return metadata, fmt.Errorf("get MDX availability for column %q.%q: %w", tableName, columnName, err)
	}
	metadata.isAvailableInMDX = isAvailableInMDX

	attributeHierarchy, err := column.AttributeHierarchy()
	if err != nil {
		return metadata, fmt.Errorf("get attribute hierarchy for column %q.%q: %w", tableName, columnName, err)
	}
	if attributeHierarchy.TOMValue().Handle != 0 {
		state, err := attributeHierarchy.State()
		if err != nil {
			return metadata, fmt.Errorf("get attribute hierarchy state for column %q.%q: %w", tableName, columnName, err)
		}
		metadata.attributeHierarchyState = string(state)
	}

	variations, err := column.Variations()
	if err != nil {
		return metadata, fmt.Errorf("get variations for column %q.%q: %w", tableName, columnName, err)
	}
	if variations.TOMValue().Handle == 0 {
		return metadata, nil
	}
	variationItems, err := variations.Items(0)
	if err != nil {
		return metadata, fmt.Errorf("enumerate variations for column %q.%q: %w", tableName, columnName, err)
	}
	for _, item := range variationItems {
		variation, err := readTOMVariation(tableName, columnName, tom.AsVariation(item))
		if err != nil {
			return metadata, err
		}
		metadata.variations = append(metadata.variations, variation)
	}
	return metadata, nil
}

func readTOMVariation(tableName, columnName string, variation tom.Variation) (variationMetadata, error) {
	name, err := variation.Name()
	if err != nil {
		return variationMetadata{}, fmt.Errorf("get variation name for column %q.%q: %w", tableName, columnName, err)
	}
	isDefault, err := variation.IsDefault()
	if err != nil {
		return variationMetadata{}, fmt.Errorf("get variation state for column %q.%q: %w", tableName, columnName, err)
	}
	metadata := variationMetadata{name: name, isDefault: isDefault}

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
		hierarchyName, err := defaultHierarchy.Name()
		if err != nil {
			return metadata, fmt.Errorf("get default hierarchy name for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		hierarchyTable, err := defaultHierarchy.Table()
		if err != nil {
			return metadata, fmt.Errorf("get default hierarchy table for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		hierarchyTableName, err := hierarchyTable.Name()
		if err != nil {
			return metadata, fmt.Errorf("get default hierarchy table name for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		metadata.defaultHierarchy = &hierarchyReference{table: hierarchyTableName, hierarchy: hierarchyName}
	}

	relationship, err := variation.Relationship()
	if err != nil {
		return metadata, fmt.Errorf("get relationship for variation %q on %q.%q: %w", name, tableName, columnName, err)
	}
	if relationship.TOMValue().Handle != 0 {
		relationshipType, err := relationship.Type()
		if err != nil {
			return metadata, fmt.Errorf("get relationship type for variation %q on %q.%q: %w", name, tableName, columnName, err)
		}
		if relationshipType != tom.RelationshipTypeSingleColumn {
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
	columnName, err := column.Name()
	if err != nil {
		return columnReference{}, err
	}
	table, err := column.Table()
	if err != nil {
		return columnReference{}, err
	}
	tableName, err := table.Name()
	if err != nil {
		return columnReference{}, err
	}
	return columnReference{table: tableName, column: columnName}, nil
}

func readCalculatedExpressions(tableName string, table tom.Table) ([]string, error) {
	partitions, err := table.Partitions()
	if err != nil {
		return nil, fmt.Errorf("get partitions for table %q: %w", tableName, err)
	}
	if partitions.TOMValue().Handle == 0 {
		return nil, nil
	}
	partitionItems, err := partitions.Items(0)
	if err != nil {
		return nil, fmt.Errorf("enumerate partitions for table %q: %w", tableName, err)
	}
	var expressions []string
	for _, item := range partitionItems {
		partition := tom.AsPartition(item)
		sourceType, err := partition.SourceType()
		if err != nil {
			return nil, fmt.Errorf("get partition source type for table %q: %w", tableName, err)
		}
		if sourceType != tom.PartitionSourceTypeCalculated {
			continue
		}
		source, err := partition.Source()
		if err != nil {
			return nil, fmt.Errorf("get calculated partition source for table %q: %w", tableName, err)
		}
		expression, err := tom.AsCalculatedPartitionSource(source.TOMValue()).Expression()
		if err != nil {
			return nil, fmt.Errorf("get calculated partition expression for table %q: %w", tableName, err)
		}
		expressions = append(expressions, expression)
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
