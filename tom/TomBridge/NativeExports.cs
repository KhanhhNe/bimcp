using System.Collections;
using System.Globalization;
using System.Reflection;
using System.Runtime.CompilerServices;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using TOM = Microsoft.AnalysisServices.Tabular;

namespace TomBridge;

public static class NativeExports
{
    private static readonly object Gate = new();
    private static readonly Dictionary<long, object> Handles = [];
    private static readonly Dictionary<object, long> ReverseHandles = new(ReferenceEqualityComparer.Instance);
    private static long _nextHandle;

    [UnmanagedCallersOnly(EntryPoint = "tom_call", CallConvs = [typeof(CallConvCdecl)])]
    public static nint Call(nint request)
    {
        try
        {
            var json = Marshal.PtrToStringUTF8(request) ?? throw new ArgumentException("Request is null.");
            var command = JsonNode.Parse(json)?.AsObject() ?? throw new ArgumentException("Request must be a JSON object.");
            var result = Dispatch(command);
            return Marshal.StringToCoTaskMemUTF8(new JsonObject
            {
                ["ok"] = true,
                ["result"] = result
            }.ToJsonString());
        }
        catch (Exception exception)
        {
            var root = Unwrap(exception);
            return Marshal.StringToCoTaskMemUTF8(new JsonObject
            {
                ["ok"] = false,
                ["error"] = root.Message,
                ["type"] = root.GetType().FullName,
                ["stack"] = root.StackTrace
            }.ToJsonString());
        }
    }

    [UnmanagedCallersOnly(EntryPoint = "tom_free", CallConvs = [typeof(CallConvCdecl)])]
    public static void Free(nint value)
    {
        if (value != 0)
        {
            Marshal.FreeCoTaskMem(value);
        }
    }

    private static JsonNode? Dispatch(JsonObject command)
    {
        return RequiredString(command, "op") switch
        {
            "discover" => Discover(),
            "connect" => Connect(command),
            "create" => Create(command),
            "get" => Get(command),
            "snapshot" => Snapshot(command),
            "set" => Set(command),
            "getStatic" => GetStatic(command),
            "setStatic" => SetStatic(command),
            "getField" => GetField(command),
            "setField" => SetField(command),
            "getStaticField" => GetStaticField(command),
            "setStaticField" => SetStaticField(command),
            "index" => Index(command),
            "invoke" => Invoke(command),
            "invokeStatic" => InvokeStatic(command),
            "items" => Items(command),
            "release" => Release(command),
            _ => throw new ArgumentException($"Unknown operation '{RequiredString(command, "op")}'.")
        };
    }

    private static JsonNode Discover()
    {
        var results = new JsonArray();
        foreach (var root in PowerBiWorkspaceRoots())
        {
            if (!Directory.Exists(root))
            {
                continue;
            }

            foreach (var portFile in Directory.EnumerateFiles(root, "msmdsrv.port.txt", SearchOption.AllDirectories))
            {
                try
                {
                    var workspace = Directory.GetParent(Path.GetDirectoryName(portFile)!)?.FullName;
                    if (workspace is null || !File.Exists(Path.Combine(workspace, "pbiworkspace.lock")))
                    {
                        continue;
                    }

                    var port = ReadPort(portFile);
                    if (!int.TryParse(port, out _))
                    {
                        continue;
                    }
                    results.Add(new JsonObject
                    {
                        ["endpoint"] = $"localhost:{port}",
                        ["workspace"] = workspace
                    });
                }
                catch (IOException)
                {
                    // A workspace can disappear while Power BI Desktop is closing.
                }
            }
        }

        return results;
    }

    private static JsonNode Connect(JsonObject command)
    {
        var endpoint = command["endpoint"]?.GetValue<string>();
        if (string.IsNullOrWhiteSpace(endpoint))
        {
            var instances = Discover().AsArray();
            if (instances.Count == 0)
            {
                throw new InvalidOperationException("No running Power BI Desktop Analysis Services instance was found.");
            }

            endpoint = instances[0]!["endpoint"]!.GetValue<string>();
        }

        var server = new TOM.Server();
        server.Connect(endpoint);
        return ToJson(server);
    }

    private static JsonNode? Create(JsonObject command)
    {
        var type = FindType(RequiredString(command, "type"));
        var args = command["args"]?.AsArray() ?? [];
        var constructors = type.GetConstructors(BindingFlags.Instance | BindingFlags.Public);
        return ToJson(InvokeBest(constructors, null, args, ParameterTypes(command)));
    }

    private static JsonNode? Get(JsonObject command)
    {
        var target = GetHandle(command);
        var name = RequiredString(command, "name");
        var property = target.GetType().GetProperty(name, BindingFlags.Instance | BindingFlags.Public)
            ?? throw new MissingMemberException(target.GetType().FullName, name);
        return ToJson(property.GetValue(target));
    }

    private static JsonNode Snapshot(JsonObject command)
    {
        var target = GetHandle(command);
        var properties = command["properties"]?.AsArray()
            ?? throw new ArgumentException("Snapshot properties are required.");
        var result = new JsonObject();
        foreach (var propertyNode in properties)
        {
            var name = propertyNode?.GetValue<string>()
                ?? throw new ArgumentException("Snapshot property names must be strings.");
            var property = target.GetType().GetProperty(name, BindingFlags.Instance | BindingFlags.Public)
                ?? throw new MissingMemberException(target.GetType().FullName, name);
            result[name] = ToJson(property.GetValue(target));
        }
        return result;
    }

    private static JsonNode? Set(JsonObject command)
    {
        var target = GetHandle(command);
        var name = RequiredString(command, "name");
        var property = target.GetType().GetProperty(name, BindingFlags.Instance | BindingFlags.Public)
            ?? throw new MissingMemberException(target.GetType().FullName, name);
        property.SetValue(target, ConvertArgument(command["value"], property.PropertyType));
        return null;
    }

    private static JsonNode? GetStatic(JsonObject command)
    {
        var type = FindType(RequiredString(command, "type"));
        var name = RequiredString(command, "name");
        var property = type.GetProperty(name, BindingFlags.Static | BindingFlags.Public)
            ?? throw new MissingMemberException(type.FullName, name);
        return ToJson(property.GetValue(null));
    }

    private static JsonNode? SetStatic(JsonObject command)
    {
        var type = FindType(RequiredString(command, "type"));
        var name = RequiredString(command, "name");
        var property = type.GetProperty(name, BindingFlags.Static | BindingFlags.Public)
            ?? throw new MissingMemberException(type.FullName, name);
        property.SetValue(null, ConvertArgument(command["value"], property.PropertyType));
        return null;
    }

    private static JsonNode? GetField(JsonObject command)
    {
        var target = GetHandle(command);
        var name = RequiredString(command, "name");
        var field = target.GetType().GetField(name, BindingFlags.Instance | BindingFlags.Public)
            ?? throw new MissingFieldException(target.GetType().FullName, name);
        return ToJson(field.GetValue(target));
    }

    private static JsonNode? SetField(JsonObject command)
    {
        var target = GetHandle(command);
        var name = RequiredString(command, "name");
        var field = target.GetType().GetField(name, BindingFlags.Instance | BindingFlags.Public)
            ?? throw new MissingFieldException(target.GetType().FullName, name);
        field.SetValue(target, ConvertArgument(command["value"], field.FieldType));
        return null;
    }

    private static JsonNode? GetStaticField(JsonObject command)
    {
        var type = FindType(RequiredString(command, "type"));
        var name = RequiredString(command, "name");
        var field = type.GetField(name, BindingFlags.Static | BindingFlags.Public)
            ?? throw new MissingFieldException(type.FullName, name);
        return ToJson(field.GetValue(null));
    }

    private static JsonNode? SetStaticField(JsonObject command)
    {
        var type = FindType(RequiredString(command, "type"));
        var name = RequiredString(command, "name");
        var field = type.GetField(name, BindingFlags.Static | BindingFlags.Public)
            ?? throw new MissingFieldException(type.FullName, name);
        field.SetValue(null, ConvertArgument(command["value"], field.FieldType));
        return null;
    }

    private static JsonNode? Index(JsonObject command)
    {
        var target = GetHandle(command);
        var index = command["index"];
        var properties = target.GetType().GetProperties(BindingFlags.Instance | BindingFlags.Public)
            .Where(property => property.GetIndexParameters().Length == 1);
        foreach (var property in properties)
        {
            try
            {
                var parameter = property.GetIndexParameters()[0];
                var converted = ConvertArgument(index, parameter.ParameterType);
                return ToJson(property.GetValue(target, [converted]));
            }
            catch (Exception)
            {
            }
        }
        throw new MissingMemberException($"{target.GetType().FullName} has no compatible public indexer.");
    }

    private static JsonNode? Invoke(JsonObject command)
    {
        var target = GetHandle(command);
        var name = RequiredString(command, "name");
        var args = command["args"]?.AsArray() ?? [];
        var methods = target.GetType().GetMethods(BindingFlags.Instance | BindingFlags.Public)
            .Where(method => method.Name == name && !method.ContainsGenericParameters);
        return ToJson(InvokeBest(methods, target, args, ParameterTypes(command)));
    }

    private static JsonNode? InvokeStatic(JsonObject command)
    {
        var type = FindType(RequiredString(command, "type"));
        var name = RequiredString(command, "name");
        var args = command["args"]?.AsArray() ?? [];
        var methods = type.GetMethods(BindingFlags.Static | BindingFlags.Public)
            .Where(method => method.Name == name && !method.ContainsGenericParameters);
        return ToJson(InvokeBest(methods, null, args, ParameterTypes(command)));
    }

    private static JsonNode Items(JsonObject command)
    {
        var target = GetHandle(command);
        if (target is not IEnumerable enumerable)
        {
            throw new ArgumentException($"{target.GetType().FullName} is not enumerable.");
        }

        var items = new JsonArray();
        var limit = command["limit"]?.GetValue<int>() ?? int.MaxValue;
        if (limit == 0)
        {
            limit = int.MaxValue;
        }
        foreach (var item in enumerable)
        {
            if (items.Count >= limit)
            {
                break;
            }
            items.Add(ToJson(item));
        }
        return items;
    }

    private static JsonNode? Release(JsonObject command)
    {
        var id = RequiredLong(command, "handle");
        lock (Gate)
        {
            if (Handles.Remove(id, out var value))
            {
                ReverseHandles.Remove(value);
                if (value is IDisposable disposable)
                {
                    disposable.Dispose();
                }
            }
        }
        return null;
    }

    private static object? InvokeBest(
        IEnumerable<MethodBase> candidates,
        object? target,
        JsonArray args,
        string[]? parameterTypes = null)
    {
        var method = FindBest(candidates, args, parameterTypes, out var converted);
        return method switch
        {
            MethodInfo info => info.Invoke(target, converted),
            ConstructorInfo constructor => constructor.Invoke(converted),
            _ => throw new InvalidOperationException("Unsupported member type.")
        };
    }

    private static MethodBase FindBest(
        IEnumerable<MethodBase> candidates,
        JsonArray args,
        string[]? parameterTypes,
        out object?[] converted)
    {
        foreach (var candidate in candidates.OrderBy(item => item.GetParameters().Length))
        {
            var parameters = candidate.GetParameters();
            if (parameterTypes is not null &&
                !parameters.Select(parameter => FriendlyTypeName(parameter.ParameterType)).SequenceEqual(parameterTypes))
            {
                continue;
            }
            var required = parameters.Count(parameter => !parameter.IsOptional);
            if (args.Count < required || args.Count > parameters.Length)
            {
                continue;
            }

            var values = new object?[parameters.Length];
            try
            {
                for (var i = 0; i < parameters.Length; i++)
                {
                    values[i] = i < args.Count
                        ? ConvertArgument(args[i], parameters[i].ParameterType)
                        : parameters[i].DefaultValue;
                }
                converted = values;
                return candidate;
            }
            catch (Exception)
            {
            }
        }

        throw new MissingMethodException($"No compatible overload accepts {args.Count} argument(s).");
    }

    private static object? ConvertArgument(JsonNode? node, Type targetType)
    {
        var nullable = Nullable.GetUnderlyingType(targetType);
        if (node is null)
        {
            return !targetType.IsValueType || nullable is not null
                ? null
                : throw new InvalidCastException($"Cannot pass null to {targetType.FullName}.");
        }

        targetType = nullable ?? targetType;
        if (node is JsonObject objectNode && objectNode["$handle"] is not null)
        {
            var value = ResolveHandle(objectNode["$handle"]!.GetValue<long>());
            return targetType.IsInstanceOfType(value)
                ? value
                : throw new InvalidCastException($"Handle contains {value.GetType().FullName}, not {targetType.FullName}.");
        }

        if (targetType.IsEnum)
        {
            return node is JsonValue value && value.TryGetValue<string>(out var text)
                ? Enum.Parse(targetType, text, true)
                : Enum.ToObject(targetType, node.GetValue<int>());
        }

        if (targetType == typeof(string)) return node.GetValue<string>();
        if (targetType == typeof(bool)) return node.GetValue<bool>();
        if (targetType == typeof(int)) return node.GetValue<int>();
        if (targetType == typeof(long)) return node.GetValue<long>();
        if (targetType == typeof(double)) return node.GetValue<double>();
        if (targetType == typeof(decimal)) return node.GetValue<decimal>();
        if (targetType == typeof(Guid)) return Guid.Parse(node.GetValue<string>());
        if (targetType == typeof(DateTime)) return DateTime.Parse(node.GetValue<string>(), CultureInfo.InvariantCulture);
        if (targetType == typeof(object)) return JsonSerializer.Deserialize<object>(node.ToJsonString());

        return JsonSerializer.Deserialize(node.ToJsonString(), targetType)
            ?? throw new InvalidCastException($"Cannot convert JSON to {targetType.FullName}.");
    }

    private static JsonNode? ToJson(object? value)
    {
        if (value is null) return null;
        if (value is string text) return JsonValue.Create(text);
        if (value is bool boolean) return JsonValue.Create(boolean);
        if (value is byte or sbyte or short or ushort or int or uint or long or ulong or float or double or decimal)
            return JsonSerializer.SerializeToNode(value);
        if (value is DateTime dateTime) return JsonValue.Create(dateTime);
        if (value is Guid guid) return JsonValue.Create(guid);
        if (value.GetType().IsEnum) return JsonValue.Create(value.ToString());

        var handle = StoreHandle(value);
        return new JsonObject
        {
            ["$handle"] = handle,
            ["$type"] = value.GetType().FullName,
            ["$string"] = SafeToString(value)
        };
    }

    private static long StoreHandle(object value)
    {
        lock (Gate)
        {
            if (ReverseHandles.TryGetValue(value, out var existing))
            {
                return existing;
            }
            var id = ++_nextHandle;
            Handles[id] = value;
            ReverseHandles[value] = id;
            return id;
        }
    }

    private static object ResolveHandle(long id)
    {
        lock (Gate)
        {
            return Handles.TryGetValue(id, out var value)
                ? value
                : throw new KeyNotFoundException($"Unknown or released handle {id}.");
        }
    }

    private static object GetHandle(JsonObject command) => ResolveHandle(RequiredLong(command, "handle"));

    private static Type FindType(string name)
    {
        var assembly = typeof(TOM.Server).Assembly;
        return assembly.GetType(name, false, true)
            ?? assembly.GetExportedTypes().FirstOrDefault(type => type.Name.Equals(name, StringComparison.OrdinalIgnoreCase))
            ?? throw new TypeLoadException($"TOM type '{name}' was not found.");
    }

    private static string FriendlyTypeName(Type type)
    {
        if (type.IsByRef) return FriendlyTypeName(type.GetElementType()!) + "&";
        if (type.IsArray) return FriendlyTypeName(type.GetElementType()!) + "[]";
        if (!type.IsGenericType) return type.FullName ?? type.Name;
        var name = type.GetGenericTypeDefinition().FullName!.Split('`')[0];
        return $"{name}<{string.Join(",", type.GetGenericArguments().Select(FriendlyTypeName))}>";
    }

    private static string RequiredString(JsonObject command, string name) =>
        command[name]?.GetValue<string>() ?? throw new ArgumentException($"Missing '{name}'.");

    private static long RequiredLong(JsonObject command, string name) =>
        command[name]?.GetValue<long>() ?? throw new ArgumentException($"Missing '{name}'.");

    private static string[]? ParameterTypes(JsonObject command) =>
        command["parameterTypes"] is JsonArray values
            ? values.Select(value => value?.GetValue<string>() ?? string.Empty).ToArray()
            : null;

    private static Exception Unwrap(Exception exception) =>
        exception is TargetInvocationException { InnerException: not null } invocation ? invocation.InnerException : exception;

    private static string SafeToString(object value)
    {
        try { return value.ToString() ?? value.GetType().Name; }
        catch { return value.GetType().Name; }
    }

    private static IEnumerable<string> PowerBiWorkspaceRoots()
    {
        var userProfile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        var localAppData = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
        yield return Path.Combine(userProfile, "Microsoft", "Power BI Desktop Store App", "AnalysisServicesWorkspaces");
        yield return Path.Combine(localAppData, "Microsoft", "Power BI Desktop", "AnalysisServicesWorkspaces");
    }

    private static string ReadPort(string path)
    {
        var bytes = File.ReadAllBytes(path);
        var value = bytes.Length >= 2 && bytes[1] == 0
            ? Encoding.Unicode.GetString(bytes)
            : Encoding.UTF8.GetString(bytes);
        return value.Trim('\0', '\r', '\n', ' ');
    }
}
