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

type Client struct {
	call syscall.Proc
	free syscall.Proc
	mu   sync.Mutex
}

type Value struct {
	client *Client
	Handle int64  `json:"$handle"`
	Type   string `json:"$type"`
	Text   string `json:"$string"`
}

type objectRef struct {
	Value
}

type valueCarrier interface {
	TOMValue() Value
}

func (v Value) TOMValue() Value {
	return v
}

func (v objectRef) TOMValue() Value {
	return v.Value
}

type Instance struct {
	Endpoint  string `json:"endpoint"`
	Workspace string `json:"workspace"`
}

type response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
	Type   string          `json:"type"`
	Stack  string          `json:"stack"`
}

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

func (c *Client) Discover() ([]Instance, error) {
	var result []Instance
	err := c.Call(map[string]any{"op": "discover"}, &result)
	return result, err
}

func (c *Client) Connect(endpoint string) (Value, error) {
	var result Value
	err := c.Call(map[string]any{"op": "connect", "endpoint": endpoint}, &result)
	result.client = c
	return result, err
}

func (c *Client) Create(typeName string, args ...any) (Value, error) {
	var result Value
	err := c.Call(map[string]any{"op": "create", "type": typeName, "args": encodeArgs(args)}, &result)
	result.client = c
	return result, err
}

func (c *Client) CreateExact(typeName string, parameterTypes []string, args ...any) (Value, error) {
	var result Value
	err := c.Call(map[string]any{
		"op": "create", "type": typeName, "parameterTypes": parameterTypes, "args": encodeArgs(args),
	}, &result)
	result.client = c
	return result, err
}

func (c *Client) GetStatic(typeName, name string, output any) error {
	return c.Call(map[string]any{"op": "getStatic", "type": typeName, "name": name}, output)
}

func (c *Client) GetStaticValue(typeName, name string) (Value, error) {
	var result Value
	err := c.GetStatic(typeName, name, &result)
	result.client = c
	return result, err
}

func (c *Client) GetStaticAny(typeName, name string) (any, error) {
	return c.CallAny(map[string]any{"op": "getStatic", "type": typeName, "name": name})
}

func (c *Client) SetStatic(typeName, name string, value any) error {
	return c.Call(map[string]any{
		"op": "setStatic", "type": typeName, "name": name, "value": encodeArg(value),
	}, nil)
}

func (c *Client) InvokeStaticAny(typeName, name string, args ...any) (any, error) {
	return c.CallAny(map[string]any{
		"op": "invokeStatic", "type": typeName, "name": name, "args": encodeArgs(args),
	})
}

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

func (c *Client) GetStaticField(typeName, name string, output any) error {
	return c.Call(map[string]any{"op": "getStaticField", "type": typeName, "name": name}, output)
}

func (c *Client) GetStaticFieldValue(typeName, name string) (Value, error) {
	var result Value
	err := c.GetStaticField(typeName, name, &result)
	result.client = c
	return result, err
}

func (c *Client) GetStaticFieldAny(typeName, name string) (any, error) {
	return c.CallAny(map[string]any{"op": "getStaticField", "type": typeName, "name": name})
}

func (c *Client) SetStaticField(typeName, name string, value any) error {
	return c.Call(map[string]any{
		"op": "setStaticField", "type": typeName, "name": name, "value": encodeArg(value),
	}, nil)
}

func (v Value) Get(name string, output any) error {
	return v.client.Call(map[string]any{"op": "get", "handle": v.Handle, "name": name}, output)
}

func (v Value) GetValue(name string) (Value, error) {
	var result Value
	err := v.Get(name, &result)
	result.client = v.client
	return result, err
}

func (v Value) GetAny(name string) (any, error) {
	return v.client.CallAny(map[string]any{"op": "get", "handle": v.Handle, "name": name})
}

func (v Value) Set(name string, value any) error {
	return v.client.Call(map[string]any{"op": "set", "handle": v.Handle, "name": name, "value": encodeArg(value)}, nil)
}

func (v Value) Index(index any) (Value, error) {
	var result Value
	err := v.client.Call(map[string]any{"op": "index", "handle": v.Handle, "index": encodeArg(index)}, &result)
	result.client = v.client
	return result, err
}

func (v Value) Invoke(name string, output any, args ...any) error {
	return v.client.Call(map[string]any{"op": "invoke", "handle": v.Handle, "name": name, "args": encodeArgs(args)}, output)
}

func (v Value) InvokeValue(name string, args ...any) (Value, error) {
	var result Value
	err := v.Invoke(name, &result, args...)
	result.client = v.client
	return result, err
}

func (v Value) InvokeAny(name string, args ...any) (any, error) {
	return v.client.CallAny(map[string]any{
		"op": "invoke", "handle": v.Handle, "name": name, "args": encodeArgs(args),
	})
}

func (v Value) InvokeExact(name string, parameterTypes []string, output any, args ...any) error {
	return v.client.Call(map[string]any{
		"op": "invoke", "handle": v.Handle, "name": name,
		"parameterTypes": parameterTypes, "args": encodeArgs(args),
	}, output)
}

func (v Value) InvokeExactValue(name string, parameterTypes []string, args ...any) (Value, error) {
	var result Value
	err := v.InvokeExact(name, parameterTypes, &result, args...)
	result.client = v.client
	return result, err
}

func (v Value) InvokeExactAny(name string, parameterTypes []string, args ...any) (any, error) {
	return v.client.CallAny(map[string]any{
		"op": "invoke", "handle": v.Handle, "name": name,
		"parameterTypes": parameterTypes, "args": encodeArgs(args),
	})
}

func (v Value) GetField(name string, output any) error {
	return v.client.Call(map[string]any{"op": "getField", "handle": v.Handle, "name": name}, output)
}

func (v Value) GetFieldValue(name string) (Value, error) {
	var result Value
	err := v.GetField(name, &result)
	result.client = v.client
	return result, err
}

func (v Value) GetFieldAny(name string) (any, error) {
	return v.client.CallAny(map[string]any{"op": "getField", "handle": v.Handle, "name": name})
}

func (v Value) SetField(name string, input any) error {
	return v.client.Call(map[string]any{
		"op": "setField", "handle": v.Handle, "name": name, "value": encodeArg(input),
	}, nil)
}

func (v Value) Items(limit int) ([]Value, error) {
	var result []Value
	err := v.client.Call(map[string]any{"op": "items", "handle": v.Handle, "limit": limit}, &result)
	for i := range result {
		result[i].client = v.client
	}
	return result, err
}

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
