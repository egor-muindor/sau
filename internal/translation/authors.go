package translation

import "strings"

// NormalizeAuthors brings an authors string to a comparable form. The site
// rewrites the string when it saves it: a comma between members comes back from
// the API as an ampersand. Comparing verbatim would therefore never match, so
// both sides of any comparison go through this function first.
//
// The rules are: lower case, the separators ",", "&" and the word " и " all
// become a comma, runs of whitespace collapse to a single space and empty
// pieces are dropped.
func NormalizeAuthors(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&", ",")
	s = strings.ReplaceAll(s, " и ", ",")

	pieces := strings.Split(s, ",")
	cleaned := make([]string, 0, len(pieces))
	for _, p := range pieces {
		p = strings.Join(strings.Fields(p), " ")
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, ", ")
}

// AuthorsEqual reports whether two authors strings name the same people once
// separators, case and spacing are normalized.
func AuthorsEqual(a, b string) bool {
	return NormalizeAuthors(a) == NormalizeAuthors(b)
}
