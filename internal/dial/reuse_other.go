//go:build !linux

package dial

import "syscall"

func reuseAddr(network, address string, c syscall.RawConn) error { return nil }
