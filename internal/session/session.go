// Package session keeps the site cookies on disk, one file per mirror.
//
// The session is bound to a domain: a login obtained on one mirror does not
// work on another (docs/architecture.md §"Mirrors of the site"), so the store
// is keyed by host.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sau/internal/fsx"
)

// Store is a directory of per-mirror cookie files.
type Store struct {
	Dir string
}

// storedCookie is the on-disk shape of one cookie. Expires is RFC 3339, or
// empty for a session cookie such as PHPSESSID. HostOnly marks a cookie that
// carried no Domain attribute: it must not be sent to subdomains, so Domain
// is not restored from it on load.
type storedCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	HostOnly bool   `json:"hostOnly"`
	Path     string `json:"path"`
	Expires  string `json:"expires"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
}

// Jar is an http.CookieJar that can save itself to disk.
//
// net/http/cookiejar.Jar returns only names and values from Cookies, so the
// full cookies are kept here as well, purely for persistence.
type Jar struct {
	inner *cookiejar.Jar
	u     *url.URL
	path  string

	mu  sync.Mutex
	all map[string]entry
}

// entry is one cookie kept for persistence, alongside whether it was a
// host-only cookie (no Domain attribute) rather than a domain cookie.
type entry struct {
	cookie   *http.Cookie
	hostOnly bool
}

// Jar returns the jar of the given host, loading Dir/<host>.json if present.
// A missing file yields an empty jar; a corrupt file is an error.
func (s Store) Jar(host string) (*Jar, error) {
	inner, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	j := &Jar{
		inner: inner,
		// Scheme is fixed to https: the URL is used only to address the jar
		// when saving and restoring, mirrors are served over TLS.
		u:    &url.URL{Scheme: "https", Host: host, Path: "/"},
		path: filepath.Join(s.Dir, fileName(host)),
		all:  make(map[string]entry),
	}
	if err := j.load(); err != nil {
		return nil, err
	}
	return j, nil
}

func fileName(host string) string {
	return strings.ReplaceAll(host, ":", "_") + ".json"
}

func cookieKey(c *http.Cookie) string {
	return c.Domain + "\x00" + c.Path + "\x00" + c.Name
}

// SetCookies records the cookies and hands them to the inner jar.
//
// The inner jar may reject a cookie outright (a foreign domain, an
// unsatisfied __Host-/__Secure- prefix, ...): only cookies it actually
// accepted are kept for persistence, checked by asking it back for the
// cookies it would send to the cookie's own path — not to u — since a cookie
// scoped to a sub-path would not appear when asking for u's root path.
func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.inner.SetCookies(u, cookies)

	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cookies {
		hostOnly := c.Domain == ""
		cc := *c
		if cc.Domain == "" {
			cc.Domain = u.Hostname()
		}
		if cc.Path == "" {
			cc.Path = "/"
		}
		if cc.MaxAge > 0 && cc.Expires.IsZero() {
			cc.Expires = now.Add(time.Duration(cc.MaxAge) * time.Second)
		}
		key := cookieKey(&cc)
		expired := cc.MaxAge < 0 || (!cc.Expires.IsZero() && !cc.Expires.After(now))
		if expired || !wasAccepted(j.inner.Cookies(cookieURL(u, cc.Path)), &cc) {
			delete(j.all, key)
			continue
		}
		j.all[key] = entry{cookie: &cc, hostOnly: hostOnly}
	}
}

// cookieURL builds the URL to ask the inner jar about a cookie set on u with
// the given path, so that a sub-path cookie is checked at its own path
// instead of at u's.
func cookieURL(u *url.URL, path string) *url.URL {
	cu := *u
	cu.Path = path
	return &cu
}

// wasAccepted reports whether the inner jar's view of the cookies it would
// send to u includes c. cookiejar.Jar.Cookies exposes only names and values,
// so that is the most that can be checked here.
func wasAccepted(accepted []*http.Cookie, c *http.Cookie) bool {
	for _, a := range accepted {
		if a.Name == c.Name && a.Value == c.Value {
			return true
		}
	}
	return false
}

// Cookies returns the cookies to send with a request to u.
func (j *Jar) Cookies(u *url.URL) []*http.Cookie {
	return j.inner.Cookies(u)
}

// Persist writes the live cookies to disk atomically with mode 0600. Expired
// cookies are dropped.
func (j *Jar) Persist() error {
	now := time.Now()

	j.mu.Lock()
	out := make([]storedCookie, 0, len(j.all))
	for _, e := range j.all {
		c := e.cookie
		if !c.Expires.IsZero() && !c.Expires.After(now) {
			continue
		}
		sc := storedCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			HostOnly: e.hostOnly,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HttpOnly,
		}
		if !c.Expires.IsZero() {
			sc.Expires = c.Expires.UTC().Format(time.RFC3339)
		}
		out = append(out, sc)
	}
	j.mu.Unlock()

	sortStored(out)
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := fsx.MkdirPrivate(filepath.Dir(j.path)); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(j.path, data, 0o600)
}

func sortStored(cs []storedCookie) {
	for i := 1; i < len(cs); i++ {
		for k := i; k > 0 && cs[k].Name < cs[k-1].Name; k-- {
			cs[k], cs[k-1] = cs[k-1], cs[k]
		}
	}
}

func (j *Jar) load() error {
	data, err := os.ReadFile(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored []storedCookie
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("session: %s: %w", j.path, err)
	}

	now := time.Now()
	cookies := make([]*http.Cookie, 0, len(stored))
	for _, sc := range stored {
		domain := sc.Domain
		if sc.HostOnly {
			// Leave Domain empty so SetCookies binds it to j.u's host only,
			// instead of resurrecting it as a domain cookie that would also
			// match subdomains.
			domain = ""
		}
		c := &http.Cookie{
			Name:     sc.Name,
			Value:    sc.Value,
			Domain:   domain,
			Path:     sc.Path,
			Secure:   sc.Secure,
			HttpOnly: sc.HTTPOnly,
		}
		if sc.Expires != "" {
			exp, err := time.Parse(time.RFC3339, sc.Expires)
			if err != nil {
				return fmt.Errorf("session: %s: cookie %s: %w", j.path, sc.Name, err)
			}
			if !exp.After(now) {
				continue
			}
			c.Expires = exp
		}
		cookies = append(cookies, c)
	}
	j.SetCookies(j.u, cookies)
	return nil
}
