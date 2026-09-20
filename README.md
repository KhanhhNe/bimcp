# bimcp

`bimcp` exposes the Microsoft Analysis Services Tabular Object Model (TOM) to Go through a
native Windows DLL. The bridge preserves TOM object identity with handles and supports public properties, fields,
indexers, methods, constructors, static members, enumeration, and type metadata. `build.ps1` runs
the C# `TomGen` project against the exact TOM assembly used by the bridge and generates strongly
typed Go wrappers for every closed exported TOM class, interface, struct, and enum.

Generated properties use their C# names as Go getter methods plus `SetX` setters. Methods retain
their C# names. Overloads receive readable `With...` suffixes and pass their complete CLR parameter
signature to the bridge, preventing runtime overload ambiguity.

## Build and run

Power BI Desktop must be open with a report loaded.

```powershell
.\build.ps1
go run .
```

The example discovers the open Power BI Desktop Analysis Services workspace, connects to it,
reads `Server.Databases[0].Model.Tables`, and prints each table and its column count.
Use `--output-path` (or `-o`) to choose where table folders are created:

```powershell
go run . --output-path .\test
```

The default DLL path is `tom\bin\tombridge.dll`. Set `TOM_BRIDGE_DLL` to load another build.

Generated wrappers are written to `tom\generated.go`:

```go
typedModel := tom.AsModel(model)
name, err := typedModel.Name()
tables, err := typedModel.Tables()
err = typedModel.SaveChanges()
```

## Development

Requirements are Windows x64, Go 1.25+, the .NET 10 SDK, and Visual Studio C++ build tools.

1. Edit `tom\TomBridge` for runtime marshaling or DLL behavior.
2. Edit `tom\TomGen` for generated Go API shape and CLR-to-Go type mappings.
3. Run `.\build.ps1` after either project changes. It publishes the native DLL, regenerates
   `tom\generated.go`, and formats the generated source.
4. Run `go test .\...` and `go run .` with Power BI Desktop open.

Do not edit `tom\generated.go` manually. Keep the `Microsoft.AnalysisServices` package versions in
`TomBridge.csproj` and `TomGen.csproj` synchronized.

### Generated documentation

`TomGen` reads the XML documentation shipped in the `Microsoft.AnalysisServices` NuGet package.
Generated Go types and members include the official summary, remarks, parameter descriptions,
return description, documented exceptions, and a Microsoft Learn link. When Microsoft does not
ship XML documentation for a member, `TomGen` emits a reflection-derived description and keeps the
Learn link so every generated declaration remains discoverable in Go tooling.

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
