# bimcp

`bimcp` exposes the Microsoft Analysis Services Tabular Object Model (TOM) to Go through a
native Windows DLL. The bridge preserves TOM object identity with handles and supports public properties, fields,
indexers, methods, constructors, static members, enumeration, and type metadata. `build.ps1` runs
the C# `TomGen` project against the exact TOM assembly used by the bridge and generates strongly
typed Go wrappers for every closed exported TOM class, interface, struct, and enum.

Readable scalar instance properties are fields on each generated TOM wrapper. Call `Snapshot` to
populate all of them in one bridge call while preserving error handling at the interop boundary.
Object and collection properties remain lazy getter methods, and writable properties have `SetX`
setters. C# methods retain their names. Overloads receive readable `With...` suffixes and pass their
complete CLR parameter signature to the bridge, preventing runtime overload ambiguity.

```go
table, err = table.Snapshot()
fmt.Println(table.Name, table.IsHidden, table.IsPrivate)
```

## Build and run

Power BI Desktop must be open with a report loaded.

```powershell
.\build.ps1
go run . --output-path .\test
```

The build caches fingerprints for the native bridge and Go wrapper generator under `tom\obj`.
Unchanged stages are skipped on subsequent runs. Dependency restore is also skipped after
source-only changes, and the .NET MSBuild server is enabled for repeated CLI builds. Use
`.\build.ps1 -Force` to restore dependencies and rebuild both stages.

The example discovers the open Power BI Desktop Analysis Services workspace, connects to it,
reads `Server.Databases[0].Model.Tables`, and prints the tables visible in Power BI clients. For
each visible table it prints the visible columns and their data types, plus visible measures and
their DAX expressions. It creates one folder per table under `--output-path`. The process keeps
watching the model and prints newly added visible tables.

The default DLL path is `tom\bin\tombridge.dll`. Set `TOM_BRIDGE_DLL` to load another build.

Generated wrappers are written to `tom\generated.go`:

```go
typedModel := tom.AsModel(model)
typedModel, err = typedModel.Snapshot()
name := typedModel.Name
tables, err := typedModel.TableItems(0)
err = typedModel.SaveChanges()
```

See [`tom/README.md`](tom/README.md) for the complete list of handwritten bridge APIs and
systematic differences between the generated Go API and the original .NET TOM library.

Readable collection properties also get typed `<Item>Items(limit)` helpers, such as
`Model.TableItems`, `Table.ColumnItems`, and `Table.MeasureItems`. A positive limit stops
enumeration after that many items; zero returns the full collection.

## Development

Requirements are Windows x64, Go 1.25+, the .NET 10 SDK, and Visual Studio C++ build tools.

1. Edit `tom\TomBridge` for runtime marshaling or DLL behavior.
2. Edit `tom\TomGen` for generated Go API shape and CLR-to-Go type mappings.
3. Run `.\build.ps1` after either project changes. It publishes the native DLL, regenerates
   `tom\generated.go`, and formats the generated source.
4. With a report open in Power BI Desktop, run `go run . --output-path .\test` and validate
   model discovery, metadata output, table-directory creation, and detection of a newly added table.

Do not edit `tom\generated.go` manually. Keep the `Microsoft.AnalysisServices` package versions in
`TomBridge.csproj` and `TomGen.csproj` synchronized.

### Generated documentation

`TomGen` reads the XML documentation shipped in the `Microsoft.AnalysisServices` NuGet package.
Generated Go types and members include the official summary, remarks, type parameter and parameter
descriptions, value and return descriptions, documented exceptions, examples, deprecation notices,
and a Microsoft Learn link. When Microsoft omits documentation for part of a member, `TomGen`
supplements it with reflection-derived CLR type details. Links target the corresponding Learn type
or member page; enum values link to their containing enum because Learn does not publish separate
pages for individual enum values.

## Low-level mapping

`tom.Value` is a managed TOM object handle:

```go
model, err := database.GetValue("Model")
tables, err := model.GetValue("Tables")
sales, err := tables.Index("Sales")
err = sales.Set("Description", "Sales facts")
err = model.Invoke("SaveChanges", nil)
```

This maps directly to:

```csharp
Model model = database.Model;
TableCollection tables = model.Tables;
Table sales = tables["Sales"];
sales.Description = "Sales facts";
model.SaveChanges();
```

The bridge uses reflection so newly added TOM members remain callable without changing its C ABI.
Values crossing the DLL boundary are primitives, enum names, or managed object handles. Generic
methods and methods containing `ref`/`out` parameters retain variadic low-level wrappers because Go
has no direct CLR equivalent. Events, delegates, and arbitrary CLR object graphs require
purpose-built adapters and remain accessible through the low-level `Client.Call`/`Value.InvokeAny`
escape hatches.
