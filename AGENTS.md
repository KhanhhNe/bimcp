# Validation

Validate every change end to end. Do not add or run focused unit tests.

1. Run `./build.ps1` and confirm the native TOM bridge and generated Go wrappers build successfully.
2. Open Power BI Desktop with a report loaded.
3. Run `go run . --output-path ./test`.
4. Confirm the application discovers and connects to the open model, prints its visible tables, columns, measures, and creates the expected table directories under `test`.
5. Add a visible table in Power BI Desktop and confirm the running application detects and emits it.

When full end-to-end validation is blocked by a missing external prerequisite, report the blocker and the validation steps that remain unverified.