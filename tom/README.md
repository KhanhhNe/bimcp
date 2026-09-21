# `tom` package

This package is a Go bridge to
[`Microsoft.AnalysisServices.Tabular`](https://learn.microsoft.com/dotnet/api/microsoft.analysisservices.tabular?view=analysisservices-dotnet).
It is not a drop-in transcription of the .NET API. `generated.go` contains wrappers derived from
the installed TOM assembly, while `client_windows.go` contains handwritten bridge operations and
convenience APIs.

## Handwritten APIs

The following APIs do not come from the Microsoft TOM library.

### Bridge lifecycle and discovery

| Go API | Behavior | Difference from TOM |
|---|---|---|
| `Open(path)` | Loads `tombridge.dll` and resolves its native entry points. An empty path checks `TOM_BRIDGE_DLL`, then `tom\bin\tombridge.dll`. | TOM is normally loaded and called directly by .NET code. |
| `Client.Discover()` | Finds running Power BI Desktop Analysis Services workspaces and returns their local endpoints. | TOM has no Power BI Desktop process-discovery API. |

There is deliberately no `Client.Connect` convenience method. Create the server and call the
generated wrapper for the original
[`Server.Connect`](https://learn.microsoft.com/dotnet/api/microsoft.analysisservices.core.server.connect?view=analysisservices-dotnet)
instance method:

```go
server, err := tom.NewServer(client)
err = server.Connect(endpoint)
```

### Generic bridge operations

These methods are low-level escape hatches used by generated wrappers and by callers that need TOM
members the generator cannot represent directly.

| Go API | Purpose |
|---|---|
| `Client.Call` | Sends a raw bridge command and decodes its result into a supplied output value. |
| `Client.CallAny` | Sends a raw bridge command and returns either a managed `Value` or decoded JSON data. |
| `Client.Create` | Constructs a CLR object by selecting a compatible public constructor at runtime. |
| `Client.CreateExact` | Constructs a CLR object using an explicit CLR parameter-type signature. |
| `Client.GetStatic`, `GetStaticValue`, `GetStaticAny` | Read a static CLR property as a known scalar, managed object, or dynamic value. |
| `Client.SetStatic` | Sets a static CLR property. |
| `Client.GetStaticField`, `GetStaticFieldValue`, `GetStaticFieldAny` | Read a static CLR field as a known scalar, managed object, or dynamic value. |
| `Client.SetStaticField` | Sets a static CLR field. |
| `Client.InvokeStaticAny` | Invokes a static method using runtime overload selection. |
| `Client.InvokeStaticExact`, `InvokeStaticExactValue`, `InvokeStaticExactAny` | Invoke a static method using an explicit CLR parameter-type signature. |
| `Value.Get`, `GetValue`, `GetAny` | Read an instance property as a known scalar, managed object, or dynamic value. |
| `Value.Set` | Sets an instance property. |
| `Value.GetField`, `GetFieldValue`, `GetFieldAny` | Read an instance field as a known scalar, managed object, or dynamic value. |
| `Value.SetField` | Sets an instance field. |
| `Value.Index` | Reads an item through a compatible one-parameter CLR indexer. |
| `Value.Invoke`, `InvokeValue`, `InvokeAny` | Invoke an instance method using runtime overload selection. |
| `Value.InvokeExact`, `InvokeExactValue`, `InvokeExactAny` | Invoke an instance method using an explicit CLR parameter-type signature. |
| `Value.Snapshot` | Reads several named properties in one native bridge call. |
| `Value.Items` | Enumerates a CLR `IEnumerable`; `limit == 0` returns all items and a positive limit caps the result. |
| `Value.Release` | Removes the bridge handle and calls `Dispose` when the managed object implements `IDisposable`. |
| `Value.TOMValue` | Exposes the underlying bridge handle. Generated wrappers provide the same method through embedding. |

The `Value`, `Client`, and `Instance` types are also bridge-specific. A `Value` is not a TOM type;
it identifies a managed object retained by the DLL:

```go
type Value struct {
    Handle int64
    Type   string
    Text   string
}
```

## Generated API differences

`TomGen` uses reflection and TOM's XML documentation to generate Go wrappers. The following
transformations apply consistently across `generated.go`.

| Generated Go shape | Original TOM shape |
|---|---|
| `As<Type>(Value) <Type>` | Bridge-only unchecked conversion from a generic handle to a typed Go wrapper. |
| `New<Type>(client, ...) (<Type>, error)` | A CLR constructor such as `new Type(...)`. Overloaded constructors receive `With...` suffixes. |
| `<Type>.TOMValue()` | Bridge-only access to the managed handle embedded in every generated wrapper. |
| `<Type>.Snapshot() (<Type>, error)` | Bridge-only batch read of all supported scalar instance properties. |
| Scalar properties are struct fields populated by `Snapshot`. | CLR properties are live getters on the object. A generated scalar field is not automatically refreshed. |
| Object-valued properties are getter methods returning their generated type directly. | CLR properties return object references. The generated getter automatically wraps the returned `Value` with `As<Type>`. |
| Writable properties become `Set<Property>(value) error`. | CLR uses property assignment. |
| Fields become getter methods and, when writable, `Set<Field>` methods. | CLR fields are accessed directly. |
| Static members become package functions prefixed with the owning type name. | CLR static members are accessed through the type. |
| `void` methods return `error`. Other methods return `(value, error)`. | CLR methods return their declared value or `void` and throw exceptions. |
| Overloads receive deterministic `With<ParameterNames>` suffixes and send the complete CLR parameter signature to the bridge. | CLR resolves overloads from the compile-time argument types. |
| Collection properties may receive typed `<ItemType>Items(limit)` helpers. | TOM exposes the collection, which is then enumerated separately. These helpers are bridge conveniences. |
| Enums are generated as named Go `string` types and cross the bridge by enum name. | CLR enums have integral backing values. |
| `Guid`, `DateTime`, `DateTimeOffset`, and `TimeSpan` results map to `string`. | CLR exposes dedicated value types. |
| `decimal` results map to `float64`. | CLR `decimal` has greater decimal precision than `float64`. |
| Nullable parameters map to pointers when a concrete Go type is available. | CLR uses `Nullable<T>`. |
| Unknown or unrepresentable result types map to `any`. | CLR retains the declared type. |
| Generic methods and methods with `ref` or `out` parameters use variadic `...any` wrappers. | CLR provides generic type arguments and by-reference parameters directly. |
| Open generic TOM types are omitted from generated wrappers. | They remain available in the .NET library and can only be reached through low-level bridge operations here. |
| Events, delegates, and arbitrary CLR object graphs have no generated Go equivalent. | They are native .NET concepts and require purpose-built bridge adapters. |

All generated object-returning properties and methods already return typed wrappers when reflection
provides a concrete TOM return type. Manual `As<Type>` conversion is only needed when using a
handwritten API returning `Value`, a low-level `*Value` operation, or an API whose CLR result type
cannot be represented statically.

## Object identity and lifetime

The native bridge stores non-scalar CLR objects in an identity-preserving handle table. Repeated
serialization of the same CLR object returns the same handle while it remains registered. Passing a
`Value` or generated wrapper back to the bridge sends that handle rather than serializing the
object.

`Release` invalidates that handle for future bridge calls. Releasing one wrapper also invalidates
other wrappers carrying the same handle. Generated wrappers do not release themselves
automatically, so callers should release owned root objects when finished:

```go
server, err := tom.NewServer(client)
if err != nil {
    return err
}
defer server.TOMValue().Release()
```

## Error and platform behavior

- The bridge is Windows-only and is loaded through a native DLL.
- CLR exceptions are unwrapped by the bridge and returned as Go errors containing the CLR type and
  message.
- Calls cross a JSON C ABI. Scalars are copied; non-scalar objects remain in .NET and cross as
  handles.
- Runtime overload-selection methods try compatible overloads in parameter-count order. Generated
  non-generic methods use exact CLR signatures instead and avoid this ambiguity.
- `As<Type>` does not currently verify that `Value.Type` matches the requested wrapper. The native
  bridge validates the actual CLR object when the handle is later used as a typed argument.

Do not edit `generated.go` directly. Change `TomGen\Program.cs` and run `..\build.ps1` to regenerate
the wrappers.
