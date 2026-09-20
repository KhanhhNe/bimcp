package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

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
	fmt.Printf("Power BI: %s\n", instances[0].Endpoint)

	server, err := client.Connect(instances[0].Endpoint)
	if err != nil {
		log.Fatal(err)
	}
	defer server.Release()

	typedServer := tom.AsServer(server)
	connectionState, err := typedServer.GetConnectionState(false)
	if err != nil {
		log.Fatal(err)
	}
	version, err := typedServer.Version()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("TOM server version: %v; state: %v\n", version, connectionState)

	databases, err := typedServer.Databases()
	if err != nil {
		log.Fatal(err)
	}
	database, err := databases.Index(0)
	if err != nil {
		log.Fatal(err)
	}
	typedDatabase := tom.AsDatabase(database)
	databaseName, err := typedDatabase.Name()
	if err != nil {
		log.Fatal(err)
	}

	if err := typedDatabase.Refresh(); err != nil {
		log.Fatal(err)
	}
	typedModel, err := typedDatabase.Model()
	if err != nil {
		log.Fatal(err)
	}
	var serializedText string
	options, optionsErr := tom.NewSerializeOptions(client)
	if optionsErr == nil {
		serialized, serializeErr := tom.JsonSerializerSerializeDatabase(client, typedDatabase, options)
		if serializeErr == nil {
			serializedText = serialized
			fmt.Printf("Serialized metadata: %d bytes; contains financials=%t\n",
				len(serialized), strings.Contains(strings.ToLower(serialized), `"financials"`))
		}
	}
	tables, err := typedModel.Tables()
	if err != nil {
		log.Fatal(err)
	}
	tableItems, err := tables.Items(0)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Database: %s\nTables (%d):\n", databaseName, len(tableItems))
	var financials tom.Value
	for _, item := range tableItems {
		table := tom.AsTable(item)
		name, err := table.Name()
		if err != nil {
			log.Fatal(err)
		}
		columns, err := table.Columns()
		if err != nil {
			log.Fatal(err)
		}
		count, err := columns.Count()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  - %s (%d columns)\n", name, count)
		if strings.EqualFold(name, "financials") {
			financials = item
		}
	}

	if financials.Handle == 0 {
		if serializedText != "" && printSerializedTable(serializedText, "financials") {
			return
		}
		log.Fatal(`table "financials" was not found in the connected model`)
	}

	table := tom.AsTable(financials)
	columns, err := table.Columns()
	if err != nil {
		log.Fatal(err)
	}
	columnItems, err := columns.Items(0)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nfinancials columns (%d):\n", len(columnItems))
	for _, item := range columnItems {
		column := tom.AsColumn(item)
		name, err := column.Name()
		if err != nil {
			log.Fatal(err)
		}
		dataType, err := column.DataType()
		if err != nil {
			log.Fatal(err)
		}
		hidden, err := column.IsHidden()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  - %s | type=%v | hidden=%v\n",
			name, dataType, hidden)
	}

	measures, err := table.Measures()
	if err != nil {
		log.Fatal(err)
	}
	measureItems, err := measures.Items(0)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nfinancials measures (%d):\n", len(measureItems))
	for _, item := range measureItems {
		measure := tom.AsMeasure(item)
		name, err := measure.Name()
		if err != nil {
			log.Fatal(err)
		}
		format, err := measure.FormatString()
		if err != nil {
			log.Fatal(err)
		}
		hidden, err := measure.IsHidden()
		if err != nil {
			log.Fatal(err)
		}
		expression, err := measure.Expression()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  - %s | format=%v | hidden=%v\n      %v\n",
			name, format, hidden, expression)
	}
}

func printSerializedTable(metadata, tableName string) bool {
	var document any
	if err := json.Unmarshal([]byte(metadata), &document); err != nil {
		log.Printf("parse serialized TOM metadata: %v", err)
		return false
	}
	table := findTable(document, tableName)
	if table == nil {
		return false
	}

	columns := objectList(table["columns"])
	fmt.Printf("\n%s columns (%d):\n", tableName, len(columns))
	for _, column := range columns {
		fmt.Printf("  - %v | type=%v | hidden=%v\n",
			column["name"], column["dataType"], boolValue(column["isHidden"]))
	}

	measures := objectList(table["measures"])
	fmt.Printf("\n%s measures (%d):\n", tableName, len(measures))
	for _, measure := range measures {
		fmt.Printf("  - %v | format=%v | hidden=%v\n      %v\n",
			measure["name"], measure["formatString"], boolValue(measure["isHidden"]), measure["expression"])
	}
	return true
}

func findTable(node any, tableName string) map[string]any {
	switch value := node.(type) {
	case map[string]any:
		if tables, ok := value["tables"]; ok {
			for _, table := range objectList(tables) {
				if name, ok := table["name"].(string); ok && strings.EqualFold(name, tableName) {
					return table
				}
			}
		}
		for _, child := range value {
			if table := findTable(child, tableName); table != nil {
				return table
			}
		}
	case []any:
		for _, child := range value {
			if table := findTable(child, tableName); table != nil {
				return table
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

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}
