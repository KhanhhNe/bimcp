//go:build !windows

package tom

import "errors"

// Client represents the unavailable native TOM bridge on non-Windows platforms.
type Client struct{}

// Value represents a managed TOM object handle on supported platforms.
type Value struct{}

// Instance describes a discoverable Power BI Desktop Analysis Services instance on supported platforms.
type Instance struct{}

// Open reports that the native TOM bridge is only available on Windows.
func Open(string) (*Client, error) {
	return nil, errors.New("TOM bridge currently supports Windows only")
}
