using System.Text;
using Microsoft.AnalysisServices.Tabular;

const int ModelPollIntervalMilliseconds = 1_000;

var outputPath = GetOutputPath(args);
var instances = DiscoverPowerBiInstances();

if (instances.Count == 0)
{
    throw new InvalidOperationException(
        "No open Power BI Desktop instance was found. Open a report in Power BI Desktop and try again.");
}

Console.WriteLine($"Instances ({instances.Count}):");
foreach (var instance in instances)
{
    Console.WriteLine($"  {instance.Endpoint} ({instance.Workspace})");
}

using var server = Connect(instances);
Console.WriteLine($"Connected to {server.Name}");
Console.WriteLine($"Databases ({server.Databases.Count}):");
foreach (Database database in server.Databases)
{
    Console.WriteLine($"  {database.Name} [{database.ID}]");
}

if (server.Databases.Count == 0)
{
    throw new InvalidOperationException("The selected Power BI instance does not contain a database.");
}

var selectedDatabase = server.Databases[0];
var tables = ReadVisibleTables(selectedDatabase);
foreach (var table in tables)
{
    EmitTable(outputPath, table);
}

Console.WriteLine($"Watching database {selectedDatabase.Name} with {tables.Count} visible tables.");
var knownTables = tables.Select(table => table.Name).ToHashSet(StringComparer.OrdinalIgnoreCase);

using var cancellation = new CancellationTokenSource();
Console.CancelKeyPress += (_, eventArgs) =>
{
    eventArgs.Cancel = true;
    cancellation.Cancel();
};

while (!cancellation.IsCancellationRequested)
{
    try
    {
        await Task.Delay(ModelPollIntervalMilliseconds, cancellation.Token);
        tables = ReadVisibleTables(selectedDatabase);
        foreach (var table in tables.Where(table => !knownTables.Contains(table.Name)))
        {
            EmitTable(outputPath, table);
        }

        knownTables = tables.Select(table => table.Name).ToHashSet(StringComparer.OrdinalIgnoreCase);
    }
    catch (OperationCanceledException) when (cancellation.IsCancellationRequested)
    {
        break;
    }
    catch (Exception exception)
    {
        Console.Error.WriteLine($"Failed to refresh the Power BI model: {exception.Message}");
    }
}

static string GetOutputPath(string[] arguments)
{
    if (arguments.Length == 0)
    {
        return ".";
    }

    if (arguments.Length == 2 && arguments[0] == "--output-path")
    {
        return arguments[1];
    }

    throw new ArgumentException("Usage: dotnet run -- [--output-path <directory>]");
}

static IReadOnlyList<PowerBiInstance> DiscoverPowerBiInstances()
{
    var instances = new List<PowerBiInstance>();

    foreach (var root in PowerBiWorkspaceRoots().Where(Directory.Exists))
    {
        IEnumerable<string> portFiles;
        try
        {
            portFiles = Directory.EnumerateFiles(root, "msmdsrv.port.txt", SearchOption.AllDirectories);
        }
        catch (IOException)
        {
            continue;
        }
        catch (UnauthorizedAccessException)
        {
            continue;
        }

        foreach (var portFile in portFiles)
        {
            try
            {
                var workspace = Directory.GetParent(Path.GetDirectoryName(portFile)!)?.FullName;
                if (workspace is null || !File.Exists(Path.Combine(workspace, "pbiworkspace.lock")))
                {
                    continue;
                }

                var port = ReadPort(portFile);
                if (int.TryParse(port, out _))
                {
                    instances.Add(new PowerBiInstance($"localhost:{port}", workspace));
                }
            }
            catch (IOException)
            {
                // A workspace can disappear while Power BI Desktop is closing.
            }
            catch (UnauthorizedAccessException)
            {
                // Ignore inaccessible stale workspaces and continue discovery.
            }
        }
    }

    return instances
        .DistinctBy(instance => instance.Endpoint, StringComparer.OrdinalIgnoreCase)
        .OrderBy(instance => instance.Endpoint, StringComparer.OrdinalIgnoreCase)
        .ToArray();
}

static Server Connect(IReadOnlyList<PowerBiInstance> instances)
{
    var failures = new List<string>();

    foreach (var instance in instances)
    {
        var server = new Server();
        try
        {
            server.Connect(instance.Endpoint);
            return server;
        }
        catch (Exception exception)
        {
            server.Dispose();
            failures.Add($"{instance.Endpoint}: {exception.Message}");
        }
    }

    throw new InvalidOperationException(
        $"Could not connect to any discovered Power BI instance.{Environment.NewLine}{string.Join(Environment.NewLine, failures)}");
}

static IReadOnlyList<TableMetadata> ReadVisibleTables(Database database)
{
    database.Refresh();

    return database.Model.Tables
        .Where(table => !table.IsHidden && !table.IsPrivate)
        .Select(table => new TableMetadata(
            table.Name,
            table.Columns
                .Where(column => !column.IsHidden)
                .Select(column => new ColumnMetadata(column.Name, column.DataType.ToString()))
                .ToArray(),
            table.Measures
                .Where(measure => !measure.IsHidden)
                .Select(measure => new MeasureMetadata(measure.Name, measure.Expression))
                .ToArray()))
        .ToArray();
}

static void EmitTable(string outputPath, TableMetadata table)
{
    Directory.CreateDirectory(Path.Combine(outputPath, table.Name));

    Console.WriteLine($"Table: {table.Name}");
    foreach (var column in table.Columns)
    {
        Console.WriteLine($"  Column: {column.Name} ({column.DataType})");
    }

    foreach (var measure in table.Measures)
    {
        Console.WriteLine($"  Measure: {measure.Name} = {measure.Expression}");
    }
}

static IEnumerable<string> PowerBiWorkspaceRoots()
{
    var userProfile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
    var localAppData = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);

    yield return Path.Combine(userProfile, "Microsoft", "Power BI Desktop Store App", "AnalysisServicesWorkspaces");
    yield return Path.Combine(localAppData, "Microsoft", "Power BI Desktop", "AnalysisServicesWorkspaces");
}

static string ReadPort(string path)
{
    var bytes = File.ReadAllBytes(path);
    var value = bytes.Length >= 2 && bytes[1] == 0
        ? Encoding.Unicode.GetString(bytes)
        : Encoding.UTF8.GetString(bytes);
    return value.Trim('\0', '\r', '\n', ' ');
}

internal sealed record PowerBiInstance(string Endpoint, string Workspace);
internal sealed record TableMetadata(
    string Name,
    IReadOnlyList<ColumnMetadata> Columns,
    IReadOnlyList<MeasureMetadata> Measures);
internal sealed record ColumnMetadata(string Name, string DataType);
internal sealed record MeasureMetadata(string Name, string Expression);
