package main

import (
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
