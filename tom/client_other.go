//go:build !windows

package tom

import "errors"

type Client struct{}
type Value struct{}
type Instance struct{}

func Open(string) (*Client, error) {
	return nil, errors.New("TOM bridge currently supports Windows only")
}
