// Package fakeup implements an in-memory stand-in for the Fine Uploader
// chunked upload server described in docs/protocol.md.
//
// One Server owns the storage; StartN puts several httptest servers in front
// of it, which is how the real mirrors behave: different hosts, one storage.
// The package is normal (not _test) so that any package may import it, but it
// is meant for tests only.
package fakeup

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type file struct {
	name      string
	size      int64
	total     int
	parts     map[int][]byte
	hosts     map[int]string
	resume    map[int]bool
	touches   int
	doneHost  string
	assembled []byte
	done      bool
	deleted   bool
}

func newFile() *file {
	return &file{
		parts:  map[int][]byte{},
		hosts:  map[int]string{},
		resume: map[int]bool{},
	}
}

// Server is the shared storage plus the knobs used to simulate failures.
// All methods are safe for concurrent use.
type Server struct {
	mu         sync.Mutex
	files      map[string]*file
	failHost   map[string]int
	failPart   map[int]int
	rejectPart map[int]int
	failDone   int
	delay      map[string]time.Duration
	urls       []string
	hosts      []string
}

// New returns an empty server.
func New() *Server {
	return &Server{
		files:      map[string]*file{},
		failHost:   map[string]int{},
		failPart:   map[int]int{},
		rejectPart: map[int]int{},
		delay:      map[string]time.Duration{},
	}
}

// Handler returns the HTTP handler backed by this server's storage.
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serve) }

// StartN starts n httptest servers sharing this storage and returns their base
// URLs. The servers are closed via t.Cleanup.
func (s *Server) StartN(t *testing.T, n int) []string {
	t.Helper()
	urls := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ts := httptest.NewServer(s.Handler())
		t.Cleanup(ts.Close)
		host := strings.TrimPrefix(ts.URL, "http://")
		s.mu.Lock()
		s.urls = append(s.urls, ts.URL)
		s.hosts = append(s.hosts, host)
		s.mu.Unlock()
		urls = append(urls, ts.URL)
	}
	return urls
}

// Hosts returns the host:port values of the started servers, in start order.
// These are the values FailHost and Delay expect, and the ones reported by
// PartHosts and DoneHost.
func (s *Server) Hosts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hosts...)
}

// URLs returns the base URLs of the started servers, in start order.
func (s *Server) URLs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.urls...)
}

// FailHost makes the next n requests to host answer 500.
func (s *Server) FailHost(host string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failHost[host] += n
}

// FailPart makes the next n uploads of chunk idx answer 500, on any host.
func (s *Server) FailPart(idx, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failPart[idx] += n
}

// RejectPart makes the next n uploads of chunk idx answer 400, on any host.
// Unlike FailPart, this is a refusal of the chunk itself, not a sick host: a
// client must retry without dropping the host from its pool.
func (s *Server) RejectPart(idx, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectPart[idx] += n
}

// FailDone makes the next n finalization requests answer 500.
func (s *Server) FailDone(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failDone += n
}

// Delay makes every request to host sleep d before it is served. It is not
// consumed: call Delay(host, 0) to clear it.
func (s *Server) Delay(host string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay[host] = d
}

// Reset clears storage and all knobs; started servers keep running.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = map[string]*file{}
	s.failHost = map[string]int{}
	s.failPart = map[int]int{}
	s.rejectPart = map[int]int{}
	s.failDone = 0
	s.delay = map[string]time.Duration{}
}

// Assembled returns the finalized file body for uuid.
func (s *Server) Assembled(uuid string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[uuid]
	if !ok || !f.done {
		return nil, false
	}
	return append([]byte(nil), f.assembled...), true
}

// Touches returns how many keep-alive pings arrived for uuid.
func (s *Server) Touches(uuid string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.files[uuid]; ok {
		return f.touches
	}
	return 0
}

// PartHosts maps chunk index to the host that accepted it last.
func (s *Server) PartHosts(uuid string) map[int]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]string{}
	if f, ok := s.files[uuid]; ok {
		for k, v := range f.hosts {
			out[k] = v
		}
	}
	return out
}

// ResumeFlags maps chunk index to whether qqresume was present on it.
func (s *Server) ResumeFlags(uuid string) map[int]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]bool{}
	if f, ok := s.files[uuid]; ok {
		for k, v := range f.resume {
			out[k] = v
		}
	}
	return out
}

// DoneHost returns the host that served the finalization request.
func (s *Server) DoneHost(uuid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.files[uuid]; ok {
		return f.doneHost
	}
	return ""
}

// Deleted reports whether uuid was deleted.
func (s *Server) Deleted(uuid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.files[uuid]; ok {
		return f.deleted
	}
	return false
}

// PartCount returns how many distinct chunks are stored for uuid.
func (s *Server) PartCount(uuid string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.files[uuid]; ok {
		return len(f.parts)
	}
	return 0
}

func (s *Server) fileLocked(uuid string) *file {
	f, ok := s.files[uuid]
	if !ok {
		f = newFile()
		s.files[uuid] = f
	}
	return f
}

func (s *Server) hostDelay(host string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delay[host]
}

func (s *Server) takeHostFailure(host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failHost[host] > 0 {
		s.failHost[host]--
		return true
	}
	return false
}

func (s *Server) takePartFailure(idx int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failPart[idx] > 0 {
		s.failPart[idx]--
		return true
	}
	return false
}

func (s *Server) takePartRejection(idx int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejectPart[idx] > 0 {
		s.rejectPart[idx]--
		return true
	}
	return false
}

func (s *Server) takeDoneFailure() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDone > 0 {
		s.failDone--
		return true
	}
	return false
}

func writeOK(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

// writeReject answers 400: the request was understood and refused. It is the
// answer a client must not read as a broken host.
func writeReject(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": msg})
}

func writeFail(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": msg})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if d := s.hostDelay(host); d > 0 {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return
		}
	}
	if s.takeHostFailure(host) {
		writeFail(w, "host is down")
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Query().Get("touch") == "1":
		s.serveTouch(w, r)
	case r.Method == http.MethodDelete:
		s.serveDelete(w, r)
	case r.Method == http.MethodPost && r.URL.RawQuery == "done":
		s.serveDone(w, r, host)
	case r.Method == http.MethodPost:
		s.serveChunk(w, r, host)
	default:
		writeFail(w, "unexpected request "+r.Method+" "+r.URL.String())
	}
}

func (s *Server) serveTouch(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimPrefix(r.URL.Query().Get("file"), "/")
	s.mu.Lock()
	s.fileLocked(uuid).touches++
	s.mu.Unlock()
	writeOK(w, map[string]any{"success": true, "uuid": uuid})
}

func (s *Server) serveDelete(w http.ResponseWriter, r *http.Request) {
	// deleteRequest builds host + "?/" + uuid + "&"
	uuid := strings.TrimSuffix(strings.TrimPrefix(r.URL.RawQuery, "/"), "&")
	s.mu.Lock()
	s.fileLocked(uuid).deleted = true
	s.mu.Unlock()
	writeOK(w, map[string]any{"success": true, "uuid": uuid})
}

func (s *Server) serveChunk(w http.ResponseWriter, r *http.Request, host string) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeFail(w, "bad multipart: "+err.Error())
		return
	}
	uuid := r.FormValue("qquuid")
	if uuid == "" {
		writeFail(w, "missing qquuid")
		return
	}
	idx, err := strconv.Atoi(r.FormValue("qqpartindex"))
	if err != nil {
		writeFail(w, "bad qqpartindex")
		return
	}
	if s.takePartFailure(idx) {
		writeFail(w, fmt.Sprintf("part %d rejected", idx))
		return
	}
	if s.takePartRejection(idx) {
		writeReject(w, fmt.Sprintf("part %d rejected", idx))
		return
	}
	total, err := strconv.Atoi(r.FormValue("qqtotalparts"))
	if err != nil {
		writeFail(w, "bad qqtotalparts")
		return
	}
	size, err := strconv.ParseInt(r.FormValue("qqtotalfilesize"), 10, 64)
	if err != nil {
		writeFail(w, "bad qqtotalfilesize")
		return
	}
	fh, _, err := r.FormFile("qqfile")
	if err != nil {
		writeFail(w, "missing qqfile")
		return
	}
	defer fh.Close()
	body, err := io.ReadAll(fh)
	if err != nil {
		writeFail(w, "read qqfile: "+err.Error())
		return
	}
	resume := r.FormValue("qqresume") != ""

	s.mu.Lock()
	f := s.fileLocked(uuid)
	f.name = r.FormValue("qqfilename")
	f.size = size
	f.total = total
	f.parts[idx] = body
	f.hosts[idx] = host
	f.resume[idx] = resume
	s.mu.Unlock()

	writeOK(w, map[string]any{"success": true, "uuid": uuid, "uploadName": nil})
}

func (s *Server) serveDone(w http.ResponseWriter, r *http.Request, host string) {
	if err := r.ParseForm(); err != nil {
		writeFail(w, "bad form: "+err.Error())
		return
	}
	uuid := r.FormValue("qquuid")
	if s.takeDoneFailure() {
		writeFail(w, "finalization rejected")
		return
	}
	total, err := strconv.Atoi(r.FormValue("qqtotalparts"))
	if err != nil {
		writeFail(w, "bad qqtotalparts")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[uuid]
	if !ok {
		writeFail(w, "unknown uuid "+uuid)
		return
	}
	idxs := make([]int, 0, len(f.parts))
	for i := range f.parts {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	if len(idxs) != total {
		writeFail(w, fmt.Sprintf("incomplete: have %d of %d parts", len(idxs), total))
		return
	}
	var out []byte
	for n, i := range idxs {
		if i != n {
			writeFail(w, fmt.Sprintf("missing part %d", n))
			return
		}
		out = append(out, f.parts[i]...)
	}
	f.assembled = out
	f.done = true
	f.doneHost = host
	writeOK(w, map[string]any{"success": true, "uuid": uuid})
}
