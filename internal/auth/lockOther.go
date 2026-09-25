//go:build !linux && !darwin && !freebsd

package auth

import (
	"context"
	"errors"
	"os"
)

var errUnsupportedLock = errors.New("private auth store is unsupported on this platform")

func openPrivateRead(string) (*os.File, error) { return nil, errUnsupportedLock }
func openPrivateLock(string) (*os.File, error) { return nil, errUnsupportedLock }
func lock(context.Context, *os.File) error     { return errUnsupportedLock }
func unlock(*os.File)                          {}
