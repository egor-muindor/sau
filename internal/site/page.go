// Package site talks to the Yii pages and the read-only API of the website.
package site

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// UploaderConfig is one initFileUploader({...}) call from the create form page.
// Upload servers are never cached: this value is always parsed from a fresh page.
type UploaderConfig struct {
	ServerID   int
	ServerURL  string
	ServerURLs []string
	AllowedExt []string
}

// CreateForm is everything a submit needs from a freshly fetched form page.
type CreateForm struct {
	CSRF  string
	Video UploaderConfig
	Sub   UploaderConfig
}

// Page is the parsed result of any page the site may answer with.
type Page struct {
	CSRF      string
	IsLogin   bool
	Errors    []string
	Uploaders []UploaderConfig // video first, subtitles second
}

// maxBodyBytes bounds how much of an answer is read into memory. A site page is
// tens of kilobytes and an API answer a few hundred; anything past this is a
// misdirected request or a hostile answer, and neither is worth an allocation
// the size of the machine's memory.
const maxBodyBytes = 4 << 20

// ParsePage parses a site page: the CSRF token, whether it is the login page,
// the validation error block and the file uploader configs.
func ParsePage(r io.Reader) (Page, error) {
	var p Page
	doc, err := html.Parse(io.LimitReader(r, maxBodyBytes))
	if err != nil {
		return Page{}, err
	}
	var video, sub *UploaderConfig
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "input":
				switch attr(n, "name") {
				case "csrf":
					if p.CSRF == "" {
						p.CSRF = attr(n, "value")
					}
				case "LoginForm[password]":
					p.IsLogin = true
				}
			case "div":
				if hasClass(n, "errorSummary") {
					p.Errors = append(p.Errors, listItems(n)...)
				}
			case "script":
				for _, u := range parseUploaders(textOf(n)) {
					cfg := u.config()
					switch u.FormField {
					case videoFormField:
						video = &cfg
					case subFormField:
						sub = &cfg
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if video != nil {
		p.Uploaders = append(p.Uploaders, *video)
	}
	if sub != nil {
		p.Uploaders = append(p.Uploaders, *sub)
	}
	return p, nil
}

// ErrNoForm is returned when a page does not carry a usable create form.
var ErrNoForm = errors.New("site: page is not a create form")

// CreateForm turns a parsed page into the form data a submit needs.
func (p Page) CreateForm() (CreateForm, error) {
	if p.CSRF == "" || len(p.Uploaders) != 2 {
		return CreateForm{}, ErrNoForm
	}
	return CreateForm{CSRF: p.CSRF, Video: p.Uploaders[0], Sub: p.Uploaders[1]}, nil
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, f := range strings.Fields(attr(n, "class")) {
		if f == class {
			return true
		}
	}
	return false
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// listItems collects the text of every <li> below n, trimmed.
func listItems(n *html.Node) []string {
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "li" {
			if s := strings.TrimSpace(textOf(n)); s != "" {
				out = append(out, s)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

const (
	videoFormField = "TranslationAdminForm_videoFileNew"
	subFormField   = "TranslationAdminForm_subFileNew"
)

// uploaderJSON mirrors the argument of initFileUploader({...}) on the page.
// The JSON in the page escapes slashes (https:\/\/...); encoding/json handles it.
type uploaderJSON struct {
	Validation struct {
		AllowedExtensions []string `json:"allowedExtensions"`
	} `json:"validation"`
	FormField  string   `json:"formField"`
	ServerID   int      `json:"serverId"`
	ServerURL  string   `json:"serverUrl"`
	ServerURLs []string `json:"serverUrls"`
}

func (u uploaderJSON) config() UploaderConfig {
	return UploaderConfig{
		ServerID:   u.ServerID,
		ServerURL:  u.ServerURL,
		ServerURLs: u.ServerURLs,
		AllowedExt: u.Validation.AllowedExtensions,
	}
}

const initFileUploaderCall = "initFileUploader("

// parseUploaders finds every initFileUploader({...}) call in script and
// decodes its object argument.
//
// The closing of the call varies across the site's minified and unminified
// script and across browser/build differences: "})}", ") }", ")\n}", ");"
// with no closing brace at all, or two calls back to back with nothing
// between them but ");});". A regex anchored on a fixed closing sequence
// (the previous implementation used "\)\}") silently finds nothing the
// moment the closing punctuation differs even slightly.
//
// Instead, each occurrence of "initFileUploader(" is followed by skipping
// whitespace up to the opening '{', then the object is extracted by walking
// the text with a brace counter that understands JSON string literals (so a
// '{' or '}' inside a quoted string, including an escaped quote, does not
// perturb the count). What follows the matched object is irrelevant.
func parseUploaders(script string) []uploaderJSON {
	var out []uploaderJSON
	pos := 0
	for {
		i := strings.Index(script[pos:], initFileUploaderCall)
		if i < 0 {
			break
		}
		start := pos + i + len(initFileUploaderCall)
		pos = start

		j := start
		for j < len(script) && (script[j] == ' ' || script[j] == '\t' || script[j] == '\n' || script[j] == '\r') {
			j++
		}
		if j >= len(script) || script[j] != '{' {
			continue // not a JSON object argument; skip past this occurrence
		}

		obj, end, ok := scanJSONObject(script, j)
		if !ok {
			continue
		}
		pos = end

		var u uploaderJSON
		if err := json.Unmarshal([]byte(obj), &u); err != nil {
			continue // not our shape; ignore rather than fail the whole page
		}
		out = append(out, u)
	}
	return out
}

// scanJSONObject returns the substring of s starting at the '{' at index
// start and ending at its matching '}', tracking JSON string literals so
// braces inside strings are not counted. ok is false if the object is
// unterminated.
func scanJSONObject(s string, start int) (obj string, end int, ok bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], i + 1, true
			}
		}
	}
	return "", 0, false
}
