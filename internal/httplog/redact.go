// Package httplog is the one place where the HTTP exchange of both transports
// reaches the log, together with its redactor.
//
// The rule (docs/architecture.md §5): what has not passed through the redactor
// does not reach the logger at all. There are no per-call exceptions. The
// canary tests of this package guard that rule.
package httplog

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// redacted stands in for every hidden value.
const redacted = "[redacted]"

const truncationMark = "...[truncated]"

// secretHeaders never reach the log in clear text, regardless of what their
// name contains.
var secretHeaders = map[string]bool{
	"Cookie":              true,
	"Set-Cookie":          true,
	"Authorization":       true,
	"Proxy-Authorization": true,
	"Www-Authenticate":    true,
	"X-Csrf-Token":        true,
	"X-Auth-Token":        true,
}

// secretHeaderKeywords catch headers that were never explicitly listed above,
// so a new credential header does not silently bypass the redactor.
var secretHeaderKeywords = []string{"token", "secret", "password", "auth"}

func isSecretHeaderName(name string) bool {
	if secretHeaders[name] {
		return true
	}
	lower := strings.ToLower(name)
	for _, kw := range secretHeaderKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// redactHeaders turns headers into a loggable map, hiding credentials.
func redactHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		name := http.CanonicalHeaderKey(k)
		switch {
		case isSecretHeaderName(name):
			out[name] = redacted
		case name == "Referer" || name == "Referrer":
			out[name] = redactReferer(strings.Join(vs, ", "))
		default:
			out[name] = strings.Join(vs, ", ")
		}
	}
	return out
}

// redactReferer keeps the path of a Referer header but hides its query
// string, which can carry the same tokens as the page it points to.
func redactReferer(v string) string {
	parts := strings.Split(v, ", ")
	for i, p := range parts {
		u, err := url.Parse(p)
		if err != nil || u.RawQuery == "" {
			continue
		}
		u.RawQuery = redacted
		parts[i] = u.String()
	}
	return strings.Join(parts, ", ")
}

// formPairRe matches one "key=value" pair of a urlencoded body.
var formPairRe = regexp.MustCompile(`([^&=]+)=([^&]*)`)

// redactForm hides the values of the CSRF token and of every login field,
// keeping everything else byte for byte, then truncates the result to limit
// bytes for the log.
//
// The body is rewritten pair by pair rather than parsed and re-encoded:
// url.ParseQuery loses both the order of the fields and repeated keys, and the
// submit body depends on both. Redaction always runs over the full body
// before any truncation, so a cut can never land inside a secret value and
// leave part of it unredacted.
func redactForm(body []byte, limit int) string {
	out := formPairRe.ReplaceAllStringFunc(string(body), func(pair string) string {
		eq := strings.Index(pair, "=")
		rawKey := pair[:eq]
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			key = rawKey
		}
		if isSecretFormKey(key) {
			return rawKey + "=" + redacted
		}
		return pair
	})
	return truncate(out, limit)
}

// truncate caps s at limit bytes, marking the cut when it happens. Callers
// must redact s in full before truncating: a cut applied first can slice
// through the middle of a secret value and leave a half-redacted fragment in
// the log.
func truncate(s string, limit int) string {
	if len(s) > limit {
		return s[:limit] + truncationMark
	}
	return s
}

func isSecretFormKey(key string) bool {
	return key == "csrf" || strings.HasPrefix(key, "LoginForm[")
}

// The CSRF token can appear with either attribute order and either quote
// style in HTML, or as a JSON field, so all three shapes are rewritten.
var (
	csrfAfterNameRe  = regexp.MustCompile(`(?i)(name=["']csrf["'][^>]{0,200}?value=["'])[^"']*(["'])`)
	csrfBeforeNameRe = regexp.MustCompile(`(?i)(value=["'])[^"']*(["'][^>]{0,200}?name=["']csrf["'])`)
	csrfJSONRe       = regexp.MustCompile(`"csrf"\s*:\s*"[^"]*"`)
)

// redactCSRF hides the CSRF token inside an HTML or JSON body.
func redactCSRF(s string) string {
	s = csrfAfterNameRe.ReplaceAllString(s, "${1}"+redacted+"${2}")
	s = csrfBeforeNameRe.ReplaceAllString(s, "${1}"+redacted+"${2}")
	s = csrfJSONRe.ReplaceAllString(s, `"csrf":"`+redacted+`"`)
	return s
}
