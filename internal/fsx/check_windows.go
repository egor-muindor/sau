//go:build windows

package fsx

// CheckPrivate is a no-op on Windows: access is governed by the inherited
// directory ACL, and the Unix permission bits carry no meaning there.
func CheckPrivate(path string) error {
	return nil
}
