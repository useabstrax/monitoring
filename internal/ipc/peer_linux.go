//go:build linux

package ipc

import (
	"fmt"
	"net"
	"os"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/moniterr"
	"golang.org/x/sys/unix"
)

func authorize(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var uid int
	var ctrlErr error
	if err := raw.Control(func(fd uintptr) {
		ucred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil {
			ctrlErr = err
			return
		}
		uid = int(ucred.Uid)
	}); err != nil {
		return err
	}
	if ctrlErr != nil {
		return ctrlErr
	}
	allowed := os.Geteuid()
	if allowed == 0 && uid != 0 {
		return moniterr.E("permission_denied", "monitoring is root-only")
	}
	if allowed != 0 && uid != allowed {
		return moniterr.E("permission_denied", fmt.Sprintf("monitoring socket is restricted to uid %d", allowed))
	}
	return nil
}
