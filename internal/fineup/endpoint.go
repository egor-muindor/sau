package fineup

import "strings"

// Endpoint turns a server URL into the upload endpoint used for HTTP requests.
//
// Trailing slashes are trimmed on purpose: the russian channel serves its only
// host with one, and a verbatim join would produce a double slash. The verbatim
// join is still required, but only inside the hidden form field, and it lives in
// the site package. Do not reuse this function there.
func Endpoint(serverURL string) string {
	return strings.TrimRight(serverURL, "/") + "/upload.php"
}
