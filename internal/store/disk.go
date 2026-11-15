package store

import "os"

func diskUsage(path string) (free, total uint64) {
	// Best-effort: if the data directory cannot be inspected we simply omit the
	// storage card values instead of failing the API.
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		return 0, 0
	}
	return platformDiskUsage(path)
}
