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
)

const modelPollInterval = time.Second

func main() {
	var outputPath string
	flag.StringVar(&outputPath, "output-path", ".", "directory where table folders are created")
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
				log.Printf("emit table %q: %v", table.name, err)
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
		table, err := tom.AsTable(item).Snapshot()
		if err != nil {
			return nil, fmt.Errorf("snapshot table: %w", err)
		}
		if table.IsHidden || table.IsPrivate {
			continue
		}
		columns, err := table.Columns()
		if err != nil {
			return nil, fmt.Errorf("get columns for table %q: %w", table.Name, err)
		}
		columnItems, err := columns.Items(0)
		if err != nil {
			return nil, fmt.Errorf("enumerate columns for table %q: %w", table.Name, err)
		}
		measures, err := table.Measures()
		if err != nil {
			return nil, fmt.Errorf("get measures for table %q: %w", table.Name, err)
		}
		measureItems, err := measures.Items(0)
		if err != nil {
			return nil, fmt.Errorf("enumerate measures for table %q: %w", table.Name, err)
		}

		metadata := tableMetadata{name: table.Name}
		for _, item := range columnItems {
			column, err := tom.AsColumn(item).Snapshot()
			if column.IsHidden {
				continue
			}
			dataType, err := readProperty[tom.DataType](column.TOMValue(), "DataType")
			if err != nil {
				return nil, fmt.Errorf("read column type for %q.%q: %w", table.Name, column.Name, err)
			}
			metadata.columns = append(metadata.columns, columnMetadata{name: column.Name, dataType: dataType})
		}
		for _, item := range measureItems {
			measure := tom.AsMeasure(item)
			measureName, err := readProperty[string](measure.TOMValue(), "Name")
			if err != nil {
				return nil, fmt.Errorf("read measure name for table %q: %w", table.Name, err)
			}
			isHidden, err := readProperty[bool](measure.TOMValue(), "IsHidden")
			if err != nil {
				return nil, fmt.Errorf("read measure visibility for %q.%q: %w", table.Name, measureName, err)
			}
			if isHidden {
				continue
			}
			expression, err := readProperty[string](measure.TOMValue(), "Expression")
			if err != nil {
				return nil, fmt.Errorf("read measure expression for %q.%q: %w", table.Name, measureName, err)
			}
			metadata.measures = append(metadata.measures, measureMetadata{name: measureName, expression: expression})
		}
		result = append(result, metadata)
	}
	return result, nil
}

func readProperty[T any](value tom.Value, name string) (T, error) {
	var result T
	err := value.Get(name, &result)
	return result, err
}

func emitTable(outputPath string, table tableMetadata) error {
	tablePath := filepath.Join(outputPath, table.name)
	if err := os.MkdirAll(tablePath, 0755); err != nil {
		return fmt.Errorf("create folder for table %q: %w", table.name, err)
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