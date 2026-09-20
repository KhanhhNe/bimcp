package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"bimcp/tom"
)

const modelPollInterval = time.Second

func main() {
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
		if err := emitTable(table); err != nil {
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
			if err := emitTable(table); err != nil {
				log.Printf("detect table %q: %v", table.name, err)
			}
		}
		known = tableNames(tables)
	}
}

type tableMetadata struct {
	name     string
	columns  []string
	measures []string
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

		metadata := tableMetadata{name: name}
		for _, item := range columnItems {
			columnName, err := tom.AsColumn(item).Name()
			if err != nil {
				return nil, fmt.Errorf("get column name for table %q: %w", name, err)
			}
			metadata.columns = append(metadata.columns, columnName)
		}
		for _, item := range measureItems {
			measureName, err := tom.AsMeasure(item).Name()
			if err != nil {
				return nil, fmt.Errorf("get measure name for table %q: %w", name, err)
			}
			metadata.measures = append(metadata.measures, measureName)
		}
		result = append(result, metadata)
	}
	return result, nil
}

func emitTable(table tableMetadata) error {
	if err := os.MkdirAll(table.name, 0755); err != nil {
		return fmt.Errorf("create folder for table %q: %w", table.name, err)
	}
	fmt.Printf("Table: %s (%d columns, %d measures)\n", table.name, len(table.columns), len(table.measures))
	for _, columnName := range table.columns {
		fmt.Printf("  Column: %s\n", columnName)
	}
	for _, measureName := range table.measures {
		fmt.Printf("  Measure: %s\n", measureName)
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
