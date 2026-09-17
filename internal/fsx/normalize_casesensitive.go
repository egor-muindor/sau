//go:build !darwin && !windows

package fsx

// foldCase returns the path unchanged: the file system is case-sensitive.
func foldCase(p string) string {
	return p
}
