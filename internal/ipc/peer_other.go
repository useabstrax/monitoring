//go:build !linux

package ipc

import "net"

func authorize(conn *net.UnixConn) error { return nil }
