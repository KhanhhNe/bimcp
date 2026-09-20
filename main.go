package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"bimcp/tom"
)

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
	if err := database.Refresh(); err != nil {
		log.Fatal(err)
	}
	typedModel, err := database.Model()
	if err != nil {
		log.Fatal(err)
	}
	options, err := tom.NewSerializeOptions(client)
	if err != nil {
		log.Fatal(err)
	}
	serialized, err := tom.JsonSerializerSerializeDatabase(client, database, options)
	if err != nil {
		log.Fatal(err)
	}
	tables, err := readTOMTables(typedModel)
	if err != nil {
		log.Fatal(err)
	}
	if len(tables) == 0 {
		tables, err = readSerializedTables(serialized)
		if err != nil {
			log.Fatal(err)
		}
	}

	for _, table := range tables {
		if err := os.MkdirAll(table.name, 0755); err != nil {
			log.Fatalf("create folder for table %q: %v", table.name, err)
		}
		fmt.Printf("Table: %s (%d columns, %d measures)\n", table.name, len(table.columns), len(table.measures))
		for _, columnName := range table.columns {
			fmt.Printf("  Column: %s\n", columnName)
		}
		for _, measureName := range table.measures {
			fmt.Printf("  Measure: %s\n", measureName)
		}
	}

	fmt.Printf("Watching Power BI model with %d tables\n", len(tables))
	for {
	}
}

type tableMetadata struct {
	name     string
	columns  []string
	measures []string
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

func readSerializedTables(metadata string) ([]tableMetadata, error) {
	var document any
	if err := json.Unmarshal([]byte(metadata), &document); err != nil {
		return nil, fmt.Errorf("parse serialized TOM metadata: %w", err)
	}

	tables := findTableObjects(document)
	result := make([]tableMetadata, 0, len(tables))
	for _, table := range tables {
		name, _ := table["name"].(string)
		if name == "" {
			continue
		}
		result = append(result, tableMetadata{
			name:     name,
			columns:  objectNames(table["columns"]),
			measures: objectNames(table["measures"]),
		})
	}
	return result, nil
}

func findTableObjects(node any) []map[string]any {
	switch value := node.(type) {
	case map[string]any:
		if tables := objectList(value["tables"]); len(tables) > 0 {
			return tables
		}
		for _, child := range value {
			if tables := findTableObjects(child); len(tables) > 0 {
				return tables
			}
		}
	case []any:
		for _, child := range value {
			if tables := findTableObjects(child); len(tables) > 0 {
				return tables
			}
		}
	}
	return nil
}

func objectList(value any) []map[string]any {
	items, _ := value.([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}

func objectNames(value any) []string {
	objects := objectList(value)
	result := make([]string, 0, len(objects))
	for _, object := range objects {
		if name, ok := object["name"].(string); ok {
			result = append(result, name)
		}
	}
	return result
}
