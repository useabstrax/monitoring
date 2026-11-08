//go:build !linux

package collect

import "syscall"

func platformStatfs(path string) (Capacity, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Capacity{}, err
	}
	return capacityFrom(uint64(st.Bsize), uint64(st.Blocks), uint64(st.Bfree), uint64(st.Bavail), uint64(st.Files), uint64(st.Ffree)), nil
}
