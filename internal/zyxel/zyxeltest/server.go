// Package zyxeltest is an in-process fake of the Zyxel FWA505 web API for
// client tests and for manual end-to-end runs of the binary (cmd/fakemodem).
// It mirrors the behaviour verified on the real device: plaintext login over
// HTTPS or HTTP, a Session cookie, a 64-character sessionkey, JSON 401 on
// expired sessions, per-session logout. Encrypted logins are not emulated.
package zyxeltest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// Options configures the fake. Zero values reproduce the FWA505.
type Options struct {
	Addr             string // listen address for manual runs ("" = random port)
	Username         string // default "admin"
	Password         string // default "q1w2e3r4"; compared with base64decode(Input_Passwd)
	RSAPublicKey     string // "" serves "None" (FWA505); a non-empty value is served verbatim (login stays plaintext)
	SessionKeyOnDAL  bool   // 401 on DAL without a matching &sessionkey= (safety net, off by default)
	MaxSessions      int    // 0 = unlimited (FWA505); "Duplicated login" when exceeded
	RotateSessionKey bool   // a new sessionkey inside every DAL response
	ExpiredStatus    int    // 401 (default, FWA505); 504 (FWA505 first call after idle timeout); 403 or 302 as safety-net tests
	Cellwan          []byte // plaintext cellwan_status JSON to serve
	TLS              bool   // serve https with the httptest self-signed certificate
}

// RecordedRequest is one request seen by the fake.
type RecordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// Server is the running fake. The embedded httptest.Server gives URL and Close.
type Server struct {
	*httptest.Server

	opts Options

	mu       sync.Mutex
	sessions map[string]*session // cookie value → session
	cellwan  []byte
	requests []RecordedRequest
}

type session struct {
	user string
	key  string
}

const (
	cookieName  = "Session"
	resultOK    = "ZCFG_SUCCESS"
	msgInvalid  = "Invalid Username or Password"
	msgDupLogin = "Duplicated login"
)

// Start runs the fake with o. The caller closes it.
func Start(o Options) (*Server, error) {
	if o.Username == "" {
		o.Username = "admin"
	}
	if o.Password == "" {
		o.Password = "q1w2e3r4"
	}
	if o.ExpiredStatus == 0 {
		o.ExpiredStatus = http.StatusUnauthorized
	}
	if o.RSAPublicKey == "" {
		o.RSAPublicKey = "None"
	}
	s := &Server{
		opts:     o,
		sessions: map[string]*session{},
		cellwan:  o.Cellwan,
	}
	s.Server = httptest.NewUnstartedServer(s.handler())
	if o.Addr != "" {
		var lc net.ListenConfig
		ln, err := lc.Listen(context.Background(), "tcp", o.Addr)
		if err != nil {
			return nil, fmt.Errorf("zyxeltest: listen %s: %w", o.Addr, err)
		}
		_ = s.Listener.Close()
		s.Listener = ln
	}
	if o.TLS {
		s.StartTLS()
	} else {
		s.Start()
	}
	return s, nil
}

// New starts the fake for a test and closes it on cleanup.
func New(t testing.TB, o Options) *Server {
	t.Helper()
	s, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// ExpireSessions drops every session: the next authenticated call fails with
// ExpiredStatus, like the router's idle timeout.
func (s *Server) ExpireSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = map[string]*session{}
}

// SetCellwan swaps the cellwan_status fixture at runtime.
func (s *Server) SetCellwan(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cellwan = append([]byte(nil), b...)
}

// Requests returns a copy of every request received so far.
func (s *Server) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RecordedRequest(nil), s.requests...)
}

// ActiveSessions returns the number of live sessions.
func (s *Server) ActiveSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// SessionKeys returns the current sessionkey of every live session.
func (s *Server) SessionKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.sessions))
	for _, sess := range s.sessions {
		keys = append(keys, sess.key)
	}
	return keys
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /getBasicInformation", s.basicInformation)
	mux.HandleFunc("GET /getRSAPublickKey", s.rsaPublicKey)
	mux.HandleFunc("POST /UserLogin", s.userLogin)
	mux.HandleFunc("GET /cgi-bin/UserLoginCheck", s.userLoginCheck)
	mux.HandleFunc("GET /cgi-bin/DAL", s.dal)
	mux.HandleFunc("POST /cgi-bin/UserLogout", s.userLogout)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"result": "ZCFG_NOT_FOUND"})
	})
	return s.record(mux)
}

func (s *Server) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		s.mu.Lock()
		s.requests = append(s.requests, RecordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.Query(),
			Header: r.Header.Clone(),
			Body:   body,
		})
		s.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func (s *Server) basicInformation(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"result": resultOK, "CurrentLanguage": "en", "RememberPassword": 0, "ModelName": "FAKE",
	})
}

func (s *Server) rsaPublicKey(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"RSAPublicKey": s.opts.RSAPublicKey, "result": resultOK})
}

type loginBody struct {
	InputAccount string `json:"Input_Account"`
	InputPasswd  string `json:"Input_Passwd"`
}

func (s *Server) userLogin(w http.ResponseWriter, r *http.Request) {
	var body loginBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"result": msgInvalid})
		return
	}
	pw, err := base64.StdEncoding.DecodeString(body.InputPasswd)
	if err != nil || body.InputAccount != s.opts.Username || string(pw) != s.opts.Password {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"result": msgInvalid})
		return
	}

	s.mu.Lock()
	if s.opts.MaxSessions > 0 && len(s.sessions) >= s.opts.MaxSessions {
		s.mu.Unlock()
		writeJSON(w, http.StatusUnauthorized, map[string]any{"result": msgDupLogin})
		return
	}
	cookie := randomHex(16)
	sess := &session{user: body.InputAccount, key: randomHex(32)}
	s.sessions[cookie] = sess
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure only over TLS: the fake also serves plain http for local runs
		Name: cookieName, Value: cookie, Path: "/", HttpOnly: true,
		Secure: s.opts.TLS, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, loginResponse{
		SessionKey: sess.key, LoginAccount: sess.user, LoginLevel: "medium", Result: resultOK,
	})
}

// loginResponse has the FWA505 field order.
type loginResponse struct {
	SessionKey   string `json:"sessionkey"`
	ThemeColor   string `json:"ThemeColor"`
	ChangePw     bool   `json:"changePw"`
	ShowSkipBtn  bool   `json:"showSkipBtn"`
	QuickStart   bool   `json:"quickStart"`
	LoginAccount string `json:"loginAccount"`
	LoginLevel   string `json:"loginLevel"`
	Result       string `json:"result"`
}

// authenticate returns the session for the request cookie, or writes the
// expired response and returns nil.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (*session, string) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		s.writeExpired(w)
		return nil, ""
	}
	s.mu.Lock()
	sess := s.sessions[c.Value]
	s.mu.Unlock()
	if sess == nil {
		s.writeExpired(w)
		return nil, ""
	}
	return sess, c.Value
}

func (s *Server) writeExpired(w http.ResponseWriter) {
	if s.opts.ExpiredStatus == http.StatusFound {
		w.Header().Set("Location", "/login")
		w.WriteHeader(http.StatusFound)
		return
	}
	if s.opts.ExpiredStatus == http.StatusGatewayTimeout {
		// The FWA505 answers the first request after its idle timeout with
		// 504 {"result":"Timeout"} (observed 2026-09-17); 401 follows on later calls.
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"result": "Timeout"})
		return
	}
	writeJSON(w, s.opts.ExpiredStatus, map[string]any{"result": msgInvalid})
}

func (s *Server) userLoginCheck(w http.ResponseWriter, r *http.Request) {
	if sess, _ := s.authenticate(w, r); sess == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": resultOK})
}

func (s *Server) dal(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.authenticate(w, r)
	if sess == nil {
		return
	}
	q := r.URL.Query()
	if s.opts.SessionKeyOnDAL && q.Get("sessionkey") != sess.key {
		s.writeExpired(w)
		return
	}
	if q.Get("oid") != "cellwan_status" {
		writeJSON(w, http.StatusOK, map[string]any{"result": "ZCFG_INVALID_OBJECT", "Object": []any{}})
		return
	}

	s.mu.Lock()
	body := append([]byte(nil), s.cellwan...)
	if s.opts.RotateSessionKey {
		sess.key = randomHex(32)
		body = injectSessionKey(body, sess.key)
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) userLogout(w http.ResponseWriter, r *http.Request) {
	sess, cookie := s.authenticate(w, r)
	if sess == nil {
		return
	}
	if r.URL.Query().Get("sessionkey") != sess.key {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"result": msgInvalid})
		return
	}
	s.mu.Lock()
	delete(s.sessions, cookie)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"result": resultOK})
}

// injectSessionKey adds a top-level "sessionkey" to a JSON object body.
func injectSessionKey(body []byte, key string) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	k, _ := json.Marshal(key)
	obj["sessionkey"] = k
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("zyxeltest: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
