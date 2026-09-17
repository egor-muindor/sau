package site

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sau/internal/secrets"
	"sau/internal/translation"
)

// Options configures a Client. Transport is mandatory: every HTTP exchange of
// the program goes through the one redacting transport, so the client is never
// allowed to build its own (docs/architecture.md §5).
type Options struct {
	Mirrors   []string
	Transport http.RoundTripper
	Jars      func(host string) http.CookieJar
	Sleep     func(time.Duration)
	UserAgent string
}

// Client talks to the Yii pages and the read-only API of one site mirror.
// The session is bound to a domain, so cookies are kept per mirror.
//
// A Client is not safe for concurrent use: the current mirror and the per-mirror
// client cache are plain fields with no locking. One publish run drives one
// Client from one goroutine; the parallelism of an upload lives in fineup, which
// talks to the upload servers and not to the site.
type Client struct {
	mirrors []string
	idx     int
	tr      http.RoundTripper
	jars    func(host string) http.CookieJar
	clients map[string]*http.Client
	sleep   func(time.Duration)
	ua      string

	// Timeout is passed to the underlying http.Client. Zero means no timeout.
	Timeout time.Duration
}

const (
	maxGetAttempts  = 3
	formContentType = "application/x-www-form-urlencoded; charset=UTF-8"
)

func New(o Options) (*Client, error) {
	if len(o.Mirrors) == 0 {
		return nil, errors.New("site: no mirrors configured")
	}
	if o.Transport == nil {
		return nil, errors.New("site: transport is required")
	}
	// A mirror is a bare origin: scheme and host, no trailing slash. Paths are
	// concatenated onto it verbatim, so a stray slash would produce "//api/...",
	// which the site answers differently from "/api/...".
	mirrors := make([]string, 0, len(o.Mirrors))
	for _, m := range o.Mirrors {
		trimmed := strings.TrimRight(m, "/")
		u, err := url.Parse(trimmed)
		if err != nil {
			return nil, fmt.Errorf("site: bad mirror %q: %w", m, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("site: mirror %q needs an http or https scheme", m)
		}
		if u.Host == "" {
			return nil, fmt.Errorf("site: mirror %q has no host", m)
		}
		mirrors = append(mirrors, trimmed)
	}
	c := &Client{
		mirrors: mirrors,
		tr:      o.Transport,
		jars:    o.Jars,
		clients: map[string]*http.Client{},
		sleep:   o.Sleep,
		ua:      o.UserAgent,
	}
	if c.jars == nil {
		c.jars = func(string) http.CookieJar {
			j, _ := cookiejar.New(nil)
			return j
		}
	}
	if c.sleep == nil {
		c.sleep = time.Sleep
	}
	return c, nil
}

// Mirror returns the mirror currently in use.
func (c *Client) Mirror() string { return c.mirrors[c.idx] }

func (c *Client) nextMirror() {
	if len(c.mirrors) > 1 {
		c.idx = (c.idx + 1) % len(c.mirrors)
	}
}

// httpClient returns the client bound to the current mirror, building it on
// first use so that Jars is called once per mirror host.
func (c *Client) httpClient() *http.Client {
	m := c.Mirror()
	if hc, ok := c.clients[m]; ok {
		hc.Timeout = c.Timeout
		return hc
	}
	u, err := url.Parse(m)
	if err != nil {
		u = &url.URL{}
	}
	hc := &http.Client{
		Transport: c.tr,
		Jar:       c.jars(u.Host),
		Timeout:   c.Timeout,
		// Never follow redirects: a 302 is the success signal of a submit,
		// not a navigation step.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	c.clients[m] = hc
	return hc
}

func (c *Client) newRequest(ctx context.Context, method, path string, body string) (*http.Request, error) {
	var r *http.Request
	var err error
	if body == "" && method == http.MethodGet {
		r, err = http.NewRequestWithContext(ctx, method, c.Mirror()+path, nil)
	} else {
		r, err = http.NewRequestWithContext(ctx, method, c.Mirror()+path, strings.NewReader(body))
	}
	if err != nil {
		return nil, err
	}
	if c.ua != "" {
		r.Header.Set("User-Agent", c.ua)
	}
	if method == http.MethodPost {
		r.Header.Set("Content-Type", formContentType)
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	return r, nil
}

// Login fetches the login page for a fresh CSRF token and posts the credentials.
// A 302 means success; a 200 with the login page again means wrong credentials.
func (c *Client) Login(ctx context.Context, user string, pass secrets.Secret) error {
	page, err := c.getPage(ctx, "/users/login", nil)
	if err != nil {
		return err
	}
	body := EncodeLoginForm(page.CSRF, user, pass)
	req, err := c.newRequest(ctx, http.MethodPost, "/users/login", string(body))
	if err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The POST itself is not repeated here: one login attempt per call keeps
		// the answer unambiguous for the caller. But a mirror that provably
		// never got the request is stepped over, so the next call starts on a
		// mirror that may actually be reachable.
		cerr := classifyTransportErr(err)
		var ns *NotSentError
		if errors.As(cerr, &ns) {
			c.nextMirror()
		}
		return cerr
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{Status: resp.StatusCode, URL: req.URL.String()}
	}
	p, err := ParsePage(resp.Body)
	if err != nil {
		return err
	}
	if p.IsLogin {
		return ErrNotAuthorized
	}
	return fmt.Errorf("site: unexpected login response %d", resp.StatusCode)
}

// getPage performs a retrying GET and parses the answer as a site page.
// The login page is reported as ErrNotAuthorized by the callers, not here:
// Login itself must be able to see it.
func (c *Client) getPage(ctx context.Context, path string, prepare func()) (Page, error) {
	resp, err := c.doGET(ctx, path, prepare)
	if err != nil {
		return Page{}, err
	}
	defer resp.Body.Close()
	return ParsePage(resp.Body)
}

// retryableStatus lists the answers that are worth repeating: the site is busy
// or throttling, not refusing.
func retryableStatus(code int) bool {
	return code >= 500 || code == http.StatusRequestTimeout || code == http.StatusTooManyRequests
}

// getBackoff grows the pause between attempts: 1s, 2s, 4s.
func getBackoff(attempt int) time.Duration {
	return time.Duration(1<<(attempt-1)) * time.Second
}

// doGET performs a GET with retries, calling prepare (may be nil) before each
// attempt. Only GET and the read-only API retry;
// a form submit never does, because it is not idempotent. On an error that
// proves the request never left this machine the next mirror is tried.
func (c *Client) doGET(ctx context.Context, path string, prepare func()) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= maxGetAttempts; attempt++ {
		// prepare runs against the mirror this attempt actually targets. A
		// failover inside this loop moves the request to another host with
		// another cookie jar, so anything the request depends on — the channel
		// cookie above all — has to be put in place per attempt, not once
		// before the loop.
		if prepare != nil {
			prepare()
		}
		req, err := c.newRequest(ctx, http.MethodGet, path, "")
		if err != nil {
			return nil, err
		}
		resp, err := c.httpClient().Do(req)
		switch {
		case err != nil:
			lastErr = err
			var ns *NotSentError
			if errors.As(classifyTransportErr(err), &ns) {
				// The request provably never reached this mirror: the mirror
				// itself may be blocked, so move on to the next one.
				c.nextMirror()
			}
		case retryableStatus(resp.StatusCode):
			lastErr = &HTTPError{Status: resp.StatusCode, URL: req.URL.String()}
			resp.Body.Close()
		case resp.StatusCode >= 400:
			// A final refusal. Repeating it would only repeat the refusal, and
			// handing the body on as a page would surface later as a confusing
			// "this is not a create form".
			resp.Body.Close()
			return nil, &HTTPError{Status: resp.StatusCode, URL: req.URL.String()}
		default:
			return resp, nil
		}
		if attempt < maxGetAttempts {
			c.sleep(getBackoff(attempt))
		}
	}
	return nil, lastErr
}

// setChannelCookie puts upload-channel into the jar of the current mirror.
// It is set on every request, including the default channel, so that the
// channel a file was uploaded in is a recorded fact and not a default that
// may change under the user's feet.
func (c *Client) setChannelCookie(ch translation.Channel) {
	u, err := url.Parse(c.Mirror())
	if err != nil {
		return
	}
	jar := c.httpClient().Jar
	if jar == nil {
		return
	}
	jar.SetCookies(u, []*http.Cookie{{
		Name:  "upload-channel",
		Value: ch.CookieValue(),
		Path:  "/",
	}})
}

// CreateForm fetches a fresh create-translation page. The upload servers it
// returns are never cached anywhere: they change over time, so every upload
// starts from a freshly parsed page.
func (c *Client) CreateForm(ctx context.Context, seriesID int, ch translation.Channel) (CreateForm, error) {
	p, err := c.getPage(ctx, "/translations/create?seriesId="+strconv.Itoa(seriesID),
		func() { c.setChannelCookie(ch) })
	if err != nil {
		return CreateForm{}, err
	}
	if p.IsLogin {
		return CreateForm{}, ErrNotAuthorized
	}
	return p.CreateForm()
}

// SubmitResult carries what the successful redirect told us. The translation id
// is only available here: the read-only API indexes a new translation minutes
// later, so it cannot be looked up right after the submit.
type SubmitResult struct {
	TranslationID int
	Location      string
}

var translationIDRe = regexp.MustCompile(`/translations/update/(\d+)`)

// Submit posts the create-translation form. It is NEVER retried automatically:
// the request is not idempotent and a retry risks a duplicate publication
// (docs/architecture.md §7). Transport errors are split into two classes by
// classifySubmitErr, and the read-only API is not consulted at all.
func (c *Client) Submit(ctx context.Context, f CreateForm, d translation.Draft, ch translation.Channel, up UploadedFields) (SubmitResult, error) {
	videoField := EncodeUploadField(up.Video, f.Video)
	subField := EncodeUploadField(up.Sub, f.Sub)
	body := EncodeSubmitForm(f.CSRF, d, ch, videoField, subField)

	req, err := c.newRequest(ctx, http.MethodPost,
		"/translations/create?seriesId="+strconv.Itoa(d.SeriesID), string(body))
	if err != nil {
		return SubmitResult{}, err
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return SubmitResult{}, classifySubmitErr(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		loc := resp.Header.Get("Location")
		res := SubmitResult{Location: loc}
		if m := translationIDRe.FindStringSubmatch(loc); m != nil {
			res.TranslationID, _ = strconv.Atoi(m[1])
		}
		return res, nil
	}

	if resp.StatusCode == http.StatusOK {
		p, perr := ParsePage(resp.Body)
		if perr != nil {
			return SubmitResult{}, &UnknownOutcomeError{Err: perr}
		}
		if p.IsLogin {
			return SubmitResult{}, ErrNotAuthorized
		}
		if len(p.Errors) > 0 {
			return SubmitResult{}, &RejectedError{Messages: p.Errors}
		}
	}

	// Anything else: we got an answer, but it says neither "created" nor
	// "rejected". Per the table in docs/architecture.md §7 that is the
	// "everything else" row — the outcome is unknown and only a human resolves it.
	return SubmitResult{}, &UnknownOutcomeError{
		Err: fmt.Errorf("unexpected submit response %d", resp.StatusCode),
	}
}
