package health

import "syscall"

// freeGBPath reports the space available to an unprivileged process on the
// volume holding path. Bavail, not Bfree: the difference is the reserved block
// count, and using Bfree would report space the node cannot actually write.
func freeGBPath(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return float64(st.Bavail) * float64(st.Bsize) / 1e9, nil
}
