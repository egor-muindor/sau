package translation

import (
	"path/filepath"
	"strings"
)

// HasAllowedExt reports whether the file extension of path is in allowed. The
// comparison ignores case and a leading dot on either side, because the list
// comes from the site's uploader configuration and the file comes from a user.
func HasAllowedExt(path string, allowed []string) bool {
	ext := normalizeExt(filepath.Ext(path))
	if ext == "" {
		return false
	}
	for _, a := range allowed {
		if normalizeExt(a) == ext {
			return true
		}
	}
	return false
}

func normalizeExt(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
}
