//go:build windows

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bimcp/tom"

	"braces.dev/errtrace"
)

const modelPollInterval = time.Second

func main() {
	var outputPath string
	flag.StringVar(&outputPath, "output-path", ".", "directory where table folders are created")
	flag.Parse()

	client, err := tom.Open("")
	if err != nil {
		log.Fatalf("%+v", err)
	}

	instances, err := client.Discover()
	if err != nil {
		log.Fatalf("%+v", err)
	}
	if len(instances) == 0 {
		log.Fatal("no open Power BI Desktop instance found")
	}
	fmt.Printf("Instances count %d\n", len(instances))
	fmt.Printf("Using instance %s\n", instances[0].Endpoint)

	server, err := client.Connect(instances[0].Endpoint)
	if err != nil {
		log.Fatalf("%+v", err)
	}
	defer server.Release()

	typedServer := tom.AsServer(server)
	databases, err := typedServer.Databases()
	if err != nil {
		log.Fatalf("%+v", err)
	}
	databaseValue, err := databases.Index(0)
	if err != nil {
		log.Fatalf("%+v", err)
	}
	database := tom.AsDatabase(databaseValue)
	tables, err := loadTOMTables(database)
	if err != nil {
		log.Fatalf("%+v", err)
	}

	for _, table := range tables {
		if err := emitTable(outputPath, table); err != nil {
			log.Fatalf("%+v", err)
		}
	}

	fmt.Printf("Watching Power BI model with %d tables\n", len(tables))
	known := tableNames(tables)
	ticker := time.NewTicker(modelPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		tables, err := loadTOMTables(database)
		if err != nil {
			log.Printf("refresh Power BI model: %+v", err)
			continue
		}
		for _, table := range newTables(known, tables) {
			if err := emitTable(outputPath, table); err != nil {
				log.Printf("emit table %q: %+v", table.name, err)
			}
		}
		known = tableNames(tables)
	}
}

type tableMetadata struct {
	name     string
	columns  []columnMetadata
	measures []measureMetadata
}

type columnMetadata struct {
	name     string
	dataType tom.DataType
}

type measureMetadata struct {
	name       string
	expression string
}

func loadTOMTables(database tom.Database) ([]tableMetadata, error) {
	if err := database.Refresh(); err != nil {
		return nil, errtrace.Wrap(err)
	}
	model, err := database.Model()
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	return errtrace.Wrap2(readTOMTables(model))
}

func readTOMTables(model tom.Model) ([]tableMetadata, error) {
	tableItems, err := model.TableItems(0)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}

	result := make([]tableMetadata, 0, len(tableItems))
	for _, table := range tableItems {
		table, err = table.Snapshot()
		if err != nil {
			return nil, errtrace.Errorf("snapshot table: %w", err)
		}
		if table.IsHidden || table.IsPrivate {
			continue
		}
		columnItems, err := table.ColumnItems(0)
		if err != nil {
			return nil, errtrace.Errorf("enumerate columns for table %q: %w", table.Name, err)
		}
		measureItems, err := table.MeasureItems(0)
		if err != nil {
			return nil, errtrace.Errorf("enumerate measures for table %q: %w", table.Name, err)
		}

		metadata := tableMetadata{name: table.Name}
		for _, column := range columnItems {
			column, err = column.Snapshot()
			if err != nil {
				return nil, errtrace.Errorf("snapshot column for table %q: %w", table.Name, err)
			}
			if column.IsHidden {
				continue
			}
			metadata.columns = append(metadata.columns, columnMetadata{name: column.Name, dataType: column.DataType})
		}
		for _, measure := range measureItems {
			measure, err = measure.Snapshot()
			if err != nil {
				return nil, errtrace.Errorf("snapshot measure for table %q: %w", table.Name, err)
			}
			if measure.IsHidden {
				continue
			}
			metadata.measures = append(metadata.measures, measureMetadata{name: measure.Name, expression: measure.Expression})
		}
		result = append(result, metadata)
	}
	return result, nil
}
func emitTable(outputPath string, table tableMetadata) error {
	tablePath := filepath.Join(outputPath, table.name)
	if err := os.MkdirAll(tablePath, 0755); err != nil {
		return errtrace.Errorf("create folder for table %q: %w", table.name, err)
	}
	fmt.Printf("Table: %s\n", table.name)
	for _, column := range table.columns {
		fmt.Printf("  Column: %s (%s)\n", column.name, column.dataType)
	}
	for _, measure := range table.measures {
		fmt.Printf("  Measure: %s = %s\n", measure.name, measure.expression)
	}
	return nil
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
