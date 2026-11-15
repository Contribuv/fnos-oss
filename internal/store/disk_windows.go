//go:build windows

package store

func platformDiskUsage(path string) (free, total uint64) {
	return 0, 0
}
