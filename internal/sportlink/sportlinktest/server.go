// Package sportlinktest provides a fake Sportlink server for tests.
package sportlinktest

import (
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/fixtures"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
)

// Test credentials accepted by the fake server. Obviously not real.
var Creds = sportlink.Credentials{
	Username:     "test-user@example.org",
	Password:     "test-password",
	ClientID:     "test-client-id",
	ClientSecret: "test-client-secret",
}

const entityPrefix = "/entity/common/memberportal/app/"

// Server is a fake Sportlink backend. Entity responses are gzip-compressed when
// the client advertises gzip, like the real server.
type Server struct {
	*httptest.Server
	t testing.TB

	mu       sync.Mutex
	logins   int
	token    string
	requests []string
	routes   map[string]http.HandlerFunc
}

// NewServer starts a server with default routes serving the fixtures.
func NewServer(t testing.TB) *Server {
	s := &Server{t: t, routes: map[string]http.HandlerFunc{}}
	s.Handle("team/TeamCompetitionData", Body(fixtures.CompetitionData))
	s.Handle("team/TeamProgram", Body(fixtures.PoolProgram))
	s.Handle("pool/PoolProgram", Body(fixtures.PoolProgram))
	s.Handle("match/MatchFacility", Body(fixtures.MatchFacility))
	s.Handle("team/TeamLatestResults", Body([]byte(`{"ProgramItemMatch": []}`)))
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Client returns a sportlink client pointed at the server, with retries not sleeping.
func (s *Server) Client() *sportlink.Client {
	c := sportlink.NewClient(Creds, nil)
	c.BaseURL = s.URL
	c.HTTP = s.Server.Client()
	c.Sleep = sportlink.NoSleep
	return c
}

// Handle overrides the handler for an entity sub-path such as "team/TeamProgram".
func (s *Server) Handle(endpoint string, h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[endpoint] = h
}

// Body returns a handler writing a fixed JSON body.
func Body(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { writeJSON(w, r, http.StatusOK, b) }
}

// Sequence returns a handler that replies with each handler in turn, repeating the last.
func Sequence(hs ...http.HandlerFunc) http.HandlerFunc {
	var mu sync.Mutex
	i := 0
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h := hs[min(i, len(hs)-1)]
		i++
		mu.Unlock()
		h(w, r)
	}
}

// Status returns a handler replying with the status code and body.
func Status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { writeJSON(w, r, code, []byte(body)) }
}

// Logins is the number of successful logins.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

// Requests lists the entity sub-paths requested (with query), in order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// Token is the currently valid access token.
func (s *Server) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Navajo-Instance") != "KNVB" || r.Header.Get("X-Navajo-Locale") != "nl" {
		writeJSON(w, r, http.StatusBadRequest, []byte(`{"error":"missing navajo headers"}`))
		return
	}
	if r.URL.Path == "/oauth/token" {
		s.login(w, r)
		return
	}
	endpoint, ok := strings.CutPrefix(r.URL.Path, entityPrefix)
	if !ok || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, endpoint+"?"+r.URL.RawQuery)
	token := s.token
	h := s.routes[endpoint]
	s.mu.Unlock()

	if token == "" || r.Header.Get("Authorization") != "Bearer "+token {
		writeJSON(w, r, http.StatusUnauthorized, []byte(`{"Error":true,"Status":"401","Message":"Unauthorized (proxy)"}`))
		return
	}
	sum := md5.Sum([]byte(token))
	if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, " "+hex.EncodeToString(sum[:])+" ") || !strings.HasPrefix(ua, "sportlink-app-voetbalnl/") {
		s.t.Errorf("User-Agent lacks md5(token): %q", ua)
	}
	if r.Header.Get("X-Navajo-Version") == "" && endpoint != "team/TeamLatestResults" {
		s.t.Errorf("%s: missing X-Navajo-Version", endpoint)
	}
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h(w, r)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.Method != http.MethodPost {
		writeJSON(w, r, http.StatusBadRequest, []byte(`{"error":"bad request"}`))
		return
	}
	f := r.PostForm
	if f.Get("grant_type") != "password" || f.Get("username") != Creds.Username || f.Get("password") != Creds.Password ||
		f.Get("client_id") != Creds.ClientID || f.Get("secret") != Creds.ClientSecret {
		writeJSON(w, r, http.StatusBadRequest, []byte(`{"error":"Inloggen niet mogelijk","description":"onjuiste inloggegevens"}`))
		return
	}
	s.mu.Lock()
	s.logins++
	s.token = fmt.Sprintf("test-access-token-%d", s.logins)
	tok := s.token
	s.mu.Unlock()
	writeJSON(w, r, http.StatusOK, fmt.Appendf(nil, `{"access_token":%q,"token_type":"bearer","expires_in":3600,"refresh_token":"test-refresh"}`, tok))
}

// Invalidate makes the current token stale, as if it expired server-side.
func (s *Server) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = "expired"
}

func writeJSON(w http.ResponseWriter, r *http.Request, code int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(code)
		gz := gzip.NewWriter(w)
		_, _ = gz.Write(body)
		_ = gz.Close()
		return
	}
	w.WriteHeader(code)
	_, _ = w.Write(body)
}
