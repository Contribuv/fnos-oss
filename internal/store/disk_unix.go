//go:build !windows

package store

import "golang.org/x/sys/unix"

func platformDiskUsage(path string) (free, total uint64) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize)
}
