package cli

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"

	"sau/internal/config"
	"sau/internal/fineup"
	"sau/internal/fsx"
	"sau/internal/history"
	"sau/internal/httplog"
	"sau/internal/publish"
	"sau/internal/secrets"
	"sau/internal/session"
	"sau/internal/site"
	"sau/internal/statefile"
)

const (
	userAgent      = "sau"
	keyringService = "sau"
)

// sessions keeps the cookie jars that were opened during this run so that they
// can be written back once, at the end.
type sessions struct {
	mu    sync.Mutex
	store session.Store
	open  map[string]*session.Jar
}

// activeSessions is set when the real site client is built. Tests never touch
// it, because they substitute Deps.Site.
var activeSessions *sessions

func (s *sessions) jar(host string) http.CookieJar {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.open[host]; ok {
		return j
	}
	j, err := s.store.Jar(host)
	if err != nil {
		return nil // site falls back to an in-memory jar
	}
	s.open[host] = j
	return j
}

func persistSessions(d Deps) {
	s := activeSessions
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for host, j := range s.open {
		if err := j.Persist(); err != nil {
			fmt.Fprintf(d.Stderr, "sau: could not save the session for %s: %v\n", host, err)
		}
	}
}

// newTransport is the single place where HTTP goes out. Everything passes the
// redactor, so a password or a cookie cannot reach the log by accident.
func newTransport(log *slog.Logger) http.RoundTripper {
	return httplog.New(http.DefaultTransport, log)
}

func mirrorList(cfg config.Config) []string {
	list := []string{cfg.Mirror}
	for _, m := range cfg.Mirrors {
		if !slices.Contains(list, m) {
			list = append(list, m)
		}
	}
	return list
}

// newSiteClient opens the session store for this run and builds the client over
// the redacting transport. Deps is carried through because the session writer
// reports its failures on Stderr.
func newSiteClient(d Deps, cfg config.Config, log *slog.Logger) (*site.Client, error) {
	if err := fsx.MkdirPrivate(cfg.SessionDir); err != nil {
		return nil, err
	}
	s := &sessions{store: session.Store{Dir: cfg.SessionDir}, open: map[string]*session.Jar{}}
	activeSessions = s

	return site.New(site.Options{
		Mirrors:   mirrorList(cfg),
		Transport: newTransport(log),
		Jars:      s.jar,
		UserAgent: userAgent,
	})
}

func buildSite(d Deps, cfg config.Config, log *slog.Logger) (siteAPI, error) {
	return newSiteClient(d, cfg, log)
}

func buildRunner(d Deps, cfg config.Config, log *slog.Logger) (publishRunner, error) {
	client, err := newSiteClient(d, cfg, log)
	if err != nil {
		return nil, err
	}
	if err := fsx.MkdirPrivate(cfg.StateDir); err != nil {
		return nil, err
	}

	// Chunks go to the upload servers, not to the site, but they share the
	// redacting transport so that nothing escapes the logger.
	up := fineup.New(&http.Client{Transport: newTransport(log)})

	return &publish.Runner{
		Site:     client,
		Uploader: up,
		State:    statefile.Store{Dir: cfg.StateDir},
		History:  history.Log{Path: cfg.HistoryPath},
		Report:   newConsoleReporter(d),
	}, nil
}

// savePassword stores the password in the system keyring.
//
// It hands over a secrets.Secret and never unwraps it: the one call of Reveal
// on this path lives inside internal/secrets, so the plaintext does not cross
// into cli at all. The adapter over github.com/zalando/go-keyring is
// secrets.SystemKeyring; cli must not declare a second one.
func savePassword(user string, pass secrets.Secret) error {
	return secrets.KeyringSource{
		Ring:    secrets.SystemKeyring{},
		Service: keyringService,
		User:    user,
	}.Save(pass)
}
