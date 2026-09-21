# bimcp

A minimal C# console application that discovers open Power BI Desktop Analysis Services instances
and reads their Tabular Object Model (TOM) metadata.

It prints discovered instances and databases, then prints visible tables, columns, data types,
measures, and DAX expressions from the first database. One directory per visible table is created
under `--output-path`. The process continues polling the model and emits newly added visible tables.

## Requirements

- Windows
- .NET 10 SDK
- Power BI Desktop open with a report loaded

## Build and run

```powershell
.\build.ps1
dotnet run -- --output-path .\test
```

## Development with automatic restart

```powershell
.\dev.ps1
```

`dev.ps1` runs `dotnet watch`, which rebuilds and restarts the application when C# source or project
files change.
