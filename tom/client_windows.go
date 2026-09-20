//go:build windows

package tom

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// Client invokes operations on the native TOM bridge.
type Client struct {
	call syscall.Proc
	free syscall.Proc
	mu   sync.Mutex
}

// Value identifies a managed TOM object held by the native bridge.
type Value struct {
	client *Client
	// Handle is the bridge-owned identifier for the managed object.
	Handle int64 `json:"$handle"`
	// Type is the object's fully qualified CLR type name.
	Type string `json:"$type"`
	// Text is the object's string representation returned by the bridge.
	Text string `json:"$string"`
}

type objectRef struct {
	Value
}

type valueCarrier interface {
	TOMValue() Value
}

// TOMValue returns the underlying managed TOM object handle.
func (v Value) TOMValue() Value {
	return v
}

// TOMValue returns the underlying managed TOM object handle.
func (v objectRef) TOMValue() Value {
	return v.Value
}

// Instance describes a discovered Power BI Desktop Analysis Services instance.
type Instance struct {
	// Endpoint is the local Analysis Services endpoint used to connect.
	Endpoint string `json:"endpoint"`
	// Workspace is the Power BI Desktop workspace directory.
	Workspace string `json:"workspace"`
}

type response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
	Type   string          `json:"type"`
	Stack  string          `json:"stack"`
}

// Open loads the native TOM bridge DLL and returns a client for invoking it.
// An empty path uses TOM_BRIDGE_DLL, then falls back to tom\bin\tombridge.dll.
func Open(path string) (*Client, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("TOM bridge currently supports Windows only")
	}
	if path == "" {
		path = os.Getenv("TOM_BRIDGE_DLL")
	}
	if path == "" {
		path = filepath.Join("tom", "bin", "tombridge.dll")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dll, err := syscall.LoadDLL(absolute)
	if err != nil {
		return nil, fmt.Errorf("load TOM bridge %q: %w", absolute, err)
	}
	call, err := dll.FindProc("tom_call")
	if err != nil {
		return nil, err
	}
	free, err := dll.FindProc("tom_free")
	if err != nil {
		return nil, err
	}
	return &Client{call: *call, free: *free}, nil
}

// Call executes a bridge command and decodes its result into output.
func (c *Client) Call(command map[string]any, output any) error {
	payload, err := c.callRaw(command)
	if err != nil {
		return err
	}
	if output == nil || string(payload) == "null" {
		return nil
	}
	return json.Unmarshal(payload, output)
}

// CallAny executes a bridge command and returns either a Value handle or a decoded JSON value.
func (c *Client) CallAny(command map[string]any) (any, error) {
	payload, err := c.callRaw(command)
	if err != nil || string(payload) == "null" {
		return nil, err
	}
	var handle Value
	if err := json.Unmarshal(payload, &handle); err == nil && handle.Handle != 0 {
		handle.client = c
		return handle, nil
	}
	var result any
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) callRaw(command map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	request, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}
	request = append(request, 0)
	result, _, callErr := c.call.Call(uintptr(unsafe.Pointer(&request[0])))
	if result == 0 {
		return nil, fmt.Errorf("tom_call failed: %w", callErr)
	}
	defer c.free.Call(result)

	var payload response
	if err := json.Unmarshal(unsafe.Slice((*byte)(unsafe.Pointer(result)), cStringLength(result)), &payload); err != nil {
		return nil, err
	}
	if !payload.OK {
		return nil, fmt.Errorf("%s: %s", payload.Type, payload.Error)
	}
	return payload.Result, nil
}

// Discover returns the local Power BI Desktop Analysis Services instances available for connection.
func (c *Client) Discover() ([]Instance, error) {
	var result []Instance
	err := c.Call(map[string]any{"op": "discover"}, &result)
	return result, err
}

// Connect opens a TOM server connection to an Analysis Services endpoint.
func (c *Client) Connect(endpoint string) (Value, error) {
	var result Value
	err := c.Call(map[string]any{"op": "connect", "endpoint": endpoint}, &result)
	result.client = c
	return result, err
}

// Create constructs a managed object by selecting a compatible constructor at runtime.
func (c *Client) Create(typeName string, args ...any) (Value, error) {
	var result Value
	err := c.Call(map[string]any{"op": "create", "type": typeName, "args": encodeArgs(args)}, &result)
	result.client = c
	return result, err
}

// CreateExact constructs a managed object using the specified CLR constructor parameter types.
func (c *Client) CreateExact(typeName string, parameterTypes []string, args ...any) (Value, error) {
	var result Value
	err := c.Call(map[string]any{
		"op": "create", "type": typeName, "parameterTypes": parameterTypes, "args": encodeArgs(args),
	}, &result)
	result.client = c
	return result, err
}

// GetStatic reads a static CLR property and decodes it into output.
func (c *Client) GetStatic(typeName, name string, output any) error {
	return c.Call(map[string]any{"op": "getStatic", "type": typeName, "name": name}, output)
}

// GetStaticValue reads a static CLR property whose value is a managed object.
func (c *Client) GetStaticValue(typeName, name string) (Value, error) {
	var result Value
	err := c.GetStatic(typeName, name, &result)
	result.client = c
	return result, err
}

// GetStaticAny reads a static CLR property and returns either a Value handle or a decoded JSON value.
func (c *Client) GetStaticAny(typeName, name string) (any, error) {
	return c.CallAny(map[string]any{"op": "getStatic", "type": typeName, "name": name})
}

// SetStatic assigns a static CLR property.
func (c *Client) SetStatic(typeName, name string, value any) error {
	return c.Call(map[string]any{
		"op": "setStatic", "type": typeName, "name": name, "value": encodeArg(value),
	}, nil)
}

// InvokeStaticAny calls a static CLR method selected at runtime and returns its dynamic result.
func (c *Client) InvokeStaticAny(typeName, name string, args ...any) (any, error) {
	return c.CallAny(map[string]any{
		"op": "invokeStatic", "type": typeName, "name": name, "args": encodeArgs(args),
	})
}

// InvokeStaticExact calls a static CLR method with the specified parameter types and decodes its result into output.
func (c *Client) InvokeStaticExact(
	typeName, name string,
	parameterTypes []string,
	output any,
	args ...any,
) error {
	return c.Call(map[string]any{
		"op": "invokeStatic", "type": typeName, "name": name,
		"parameterTypes": parameterTypes, "args": encodeArgs(args),
	}, output)
}

// InvokeStaticExactValue calls a static CLR method with the specified parameter types and returns its object result.
func (c *Client) InvokeStaticExactValue(
	typeName, name string,
	parameterTypes []string,
	args ...any,
) (Value, error) {
	var result Value
	err := c.InvokeStaticExact(typeName, name, parameterTypes, &result, args...)
	result.client = c
	return result, err
}

// InvokeStaticExactAny calls a static CLR method with the specified parameter types and returns its dynamic result.
func (c *Client) InvokeStaticExactAny(
	typeName, name string,
	parameterTypes []string,
	args ...any,
) (any, error) {
	return c.CallAny(map[string]any{
		"op": "invokeStatic", "type": typeName, "name": name,
		"parameterTypes": parameterTypes, "args": encodeArgs(args),
	})
}

// GetStaticField reads a static CLR field and decodes it into output.
func (c *Client) GetStaticField(typeName, name string, output any) error {
	return c.Call(map[string]any{"op": "getStaticField", "type": typeName, "name": name}, output)
}

// GetStaticFieldValue reads a static CLR field whose value is a managed object.
func (c *Client) GetStaticFieldValue(typeName, name string) (Value, error) {
	var result Value
	err := c.GetStaticField(typeName, name, &result)
	result.client = c
	return result, err
}

// GetStaticFieldAny reads a static CLR field and returns either a Value handle or a decoded JSON value.
func (c *Client) GetStaticFieldAny(typeName, name string) (any, error) {
	return c.CallAny(map[string]any{"op": "getStaticField", "type": typeName, "name": name})
}

// SetStaticField assigns a static CLR field.
func (c *Client) SetStaticField(typeName, name string, value any) error {
	return c.Call(map[string]any{
		"op": "setStaticField", "type": typeName, "name": name, "value": encodeArg(value),
	}, nil)
}

// Get reads an instance property and decodes it into output.
func (v Value) Get(name string, output any) error {
	return v.client.Call(map[string]any{"op": "get", "handle": v.Handle, "name": name}, output)
}

// GetValue reads an instance property whose value is a managed object.
func (v Value) GetValue(name string) (Value, error) {
	var result Value
	err := v.Get(name, &result)
	result.client = v.client
	return result, err
}

// GetAny reads an instance property and returns either a Value handle or a decoded JSON value.
func (v Value) GetAny(name string) (any, error) {
	return v.client.CallAny(map[string]any{"op": "get", "handle": v.Handle, "name": name})
}

// Set assigns an instance property.
func (v Value) Set(name string, value any) error {
	return v.client.Call(map[string]any{"op": "set", "handle": v.Handle, "name": name, "value": encodeArg(value)}, nil)
}

// Index returns the managed object at an index or key exposed by the value's default indexer.
func (v Value) Index(index any) (Value, error) {
	var result Value
	err := v.client.Call(map[string]any{"op": "index", "handle": v.Handle, "index": encodeArg(index)}, &result)
	result.client = v.client
	return result, err
}

// Invoke calls an instance method selected at runtime and decodes its result into output.
func (v Value) Invoke(name string, output any, args ...any) error {
	return v.client.Call(map[string]any{"op": "invoke", "handle": v.Handle, "name": name, "args": encodeArgs(args)}, output)
}

// InvokeValue calls an instance method selected at runtime and returns its managed object result.
func (v Value) InvokeValue(name string, args ...any) (Value, error) {
	var result Value
	err := v.Invoke(name, &result, args...)
	result.client = v.client
	return result, err
}

// InvokeAny calls an instance method selected at runtime and returns its dynamic result.
func (v Value) InvokeAny(name string, args ...any) (any, error) {
	return v.client.CallAny(map[string]any{
		"op": "invoke", "handle": v.Handle, "name": name, "args": encodeArgs(args),
	})
}

// InvokeExact calls an instance method with the specified parameter types and decodes its result into output.
func (v Value) InvokeExact(name string, parameterTypes []string, output any, args ...any) error {
	return v.client.Call(map[string]any{
		"op": "invoke", "handle": v.Handle, "name": name,
		"parameterTypes": parameterTypes, "args": encodeArgs(args),
	}, output)
}

// InvokeExactValue calls an instance method with the specified parameter types and returns its object result.
func (v Value) InvokeExactValue(name string, parameterTypes []string, args ...any) (Value, error) {
	var result Value
	err := v.InvokeExact(name, parameterTypes, &result, args...)
	result.client = v.client
	return result, err
}

// InvokeExactAny calls an instance method with the specified parameter types and returns its dynamic result.
func (v Value) InvokeExactAny(name string, parameterTypes []string, args ...any) (any, error) {
	return v.client.CallAny(map[string]any{
		"op": "invoke", "handle": v.Handle, "name": name,
		"parameterTypes": parameterTypes, "args": encodeArgs(args),
	})
}

// GetField reads an instance field and decodes it into output.
func (v Value) GetField(name string, output any) error {
	return v.client.Call(map[string]any{"op": "getField", "handle": v.Handle, "name": name}, output)
}

// GetFieldValue reads an instance field whose value is a managed object.
func (v Value) GetFieldValue(name string) (Value, error) {
	var result Value
	err := v.GetField(name, &result)
	result.client = v.client
	return result, err
}

// GetFieldAny reads an instance field and returns either a Value handle or a decoded JSON value.
func (v Value) GetFieldAny(name string) (any, error) {
	return v.client.CallAny(map[string]any{"op": "getField", "handle": v.Handle, "name": name})
}

// SetField assigns an instance field.
func (v Value) SetField(name string, input any) error {
	return v.client.Call(map[string]any{
		"op": "setField", "handle": v.Handle, "name": name, "value": encodeArg(input),
	}, nil)
}

// Items enumerates managed objects from a collection, stopping at limit when it is greater than zero.
func (v Value) Items(limit int) ([]Value, error) {
	var result []Value
	err := v.client.Call(map[string]any{"op": "items", "handle": v.Handle, "limit": limit}, &result)
	for i := range result {
		result[i].client = v.client
	}
	return result, err
}

// Release frees the managed object handle. Releasing a zero or detached handle is a no-op.
func (v Value) Release() error {
	if v.Handle == 0 || v.client == nil {
		return nil
	}
	return v.client.Call(map[string]any{"op": "release", "handle": v.Handle}, nil)
}

func encodeArgs(args []any) []any {
	result := make([]any, len(args))
	for index, arg := range args {
		result[index] = encodeArg(arg)
	}
	return result
}

func encodeArg(value any) any {
	switch typed := value.(type) {
	case Value:
		return map[string]any{"$handle": typed.Handle}
	case *Value:
		return map[string]any{"$handle": typed.Handle}
	case valueCarrier:
		return map[string]any{"$handle": typed.TOMValue().Handle}
	default:
		return value
	}
}

func cStringLength(pointer uintptr) int {
	length := 0
	for *(*byte)(unsafe.Pointer(pointer + uintptr(length))) != 0 {
		length++
	}
	return length
}
