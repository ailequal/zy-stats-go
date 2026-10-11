package zyxel_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ailequal/zy-stats-go/internal/zyxel"
	"github.com/ailequal/zy-stats-go/internal/zyxel/zyxeltest"
)

const wantLoginBody = `{"Input_Account":"admin","Input_Passwd":"cTF3MmUzcjQ=","currLang":"en","RememberPassword":0,"SHA512_password":false}`

func TestLoginHandshake(t *testing.T) {
	t.Parallel()
	s := newFake(t, zyxeltest.Options{})
	var dump bytes.Buffer
	c, err := zyxel.New(s.URL, zyxel.WithDebug(&dump))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Login(ctx, "admin", "q1w2e3r4"); err != nil {
		t.Fatal(err)
	}
	if key := c.SessionKey(); len(key) != 64 || key != s.SessionKeys()[0] {
		t.Errorf("SessionKey = %q, fake holds %v", key, s.SessionKeys())
	}

	reqs := s.Requests()
	if len(reqs) != 2 || reqs[0].Method != http.MethodGet || reqs[0].Path != "/getRSAPublickKey" {
		t.Fatalf("requests: %+v", reqs)
	}
	lr := reqs[1]
	if lr.Method != http.MethodPost || lr.Path != "/UserLogin" || string(lr.Body) != wantLoginBody {
		t.Errorf("login request: %s %s %s", lr.Method, lr.Path, lr.Body)
	}
	for k, want := range map[string]string{
		"Content-Type":      "application/x-www-form-urlencoded; charset=UTF-8",
		"Accept":            "application/json, text/javascript, */*; q=0.01",
		"X-Requested-With":  "XMLHttpRequest",
		"If-Modified-Since": "Thu, 01 Jun 1970 00:00:00 GMT",
	} {
		if got := lr.Header.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}

	// The session works for the API calls and a second login replaces it.
	if _, err := c.LoginCheck(ctx); err != nil {
		t.Fatal(err)
	}
	first := c.SessionKey()
	if err := c.Login(ctx, "admin", "q1w2e3r4"); err != nil {
		t.Fatal(err)
	}
	if c.SessionKey() == first {
		t.Error("second login kept the old sessionkey")
	}
	if _, err := c.CellwanStatus(ctx); err != nil {
		t.Fatal(err)
	}

	out := dump.String()
	if !strings.Contains(out, `"Input_Passwd":"<redacted>"`) || strings.Contains(out, "cTF3MmUzcjQ=") {
		t.Errorf("password leaked into the debug dump:\n%s", out)
	}
	if !strings.Contains(out, "Set-Cookie: Session=<redacted>") {
		t.Errorf("Set-Cookie not redacted:\n%s", out)
	}
	for _, cookie := range s.Requests()[2].Header.Values("Cookie") {
		if strings.Contains(out, strings.TrimPrefix(cookie, "Session=")) {
			t.Errorf("cookie value leaked into the dump")
		}
	}
}

func TestLoginFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("wrong password", func(t *testing.T) {
		t.Parallel()
		s := newFake(t, zyxeltest.Options{})
		c, _ := zyxel.New(s.URL)
		err := c.Login(ctx, "admin", "wrong")
		var apiErr *zyxel.APIError
		if !errors.Is(err, zyxel.ErrInvalidCredentials) || !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Op != "login" {
			t.Errorf("err = %v", err)
		}
		if c.SessionKey() != "" {
			t.Error("sessionkey set after a failed login")
		}
	})

	t.Run("session limit", func(t *testing.T) {
		t.Parallel()
		s := newFake(t, zyxeltest.Options{MaxSessions: 1})
		c1, _ := zyxel.New(s.URL)
		if err := c1.Login(ctx, "admin", "q1w2e3r4"); err != nil {
			t.Fatal(err)
		}
		c2, _ := zyxel.New(s.URL)
		if err := c2.Login(ctx, "admin", "q1w2e3r4"); !errors.Is(err, zyxel.ErrSessionLimit) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("real RSA key over http", func(t *testing.T) {
		t.Parallel()
		s := newFake(t, zyxeltest.Options{RSAPublicKey: "-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----"})
		c, _ := zyxel.New(s.URL)
		if err := c.Login(ctx, "admin", "q1w2e3r4"); !errors.Is(err, zyxel.ErrEncryptionRequired) {
			t.Errorf("err = %v", err)
		}
		for _, r := range s.Requests() {
			if r.Path == "/UserLogin" {
				t.Error("UserLogin must not be attempted when encryption is required")
			}
		}
	})

	t.Run("real RSA key over https is ignored", func(t *testing.T) {
		t.Parallel()
		s := newFake(t, zyxeltest.Options{TLS: true, RSAPublicKey: "-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----"})
		c, _ := zyxel.New(s.URL)
		if err := c.Login(ctx, "admin", "q1w2e3r4"); err != nil {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()
		c, _ := zyxel.New("http://127.0.0.1:1")
		if err := c.Login(ctx, "admin", "q1w2e3r4"); err == nil || !strings.HasPrefix(err.Error(), "zyxel: login: ") {
			t.Errorf("err = %v", err)
		}
	})
}

// TestLoginResultMapping drives the result strings the fake never produces
// through a bare handler.
func TestLoginResultMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"locked", 401, `{"result":"Locked User"}`, zyxel.ErrAccountLocked},
		{"maxium typo", 401, `{"result":"Maxium number of login account has reached"}`, zyxel.ErrSessionLimit},
		{"maximum", 401, `{"result":"Maximum number of login account has reached"}`, zyxel.ErrSessionLimit},
		{"duplicated 200", 200, `{"result":"Duplicated login"}`, zyxel.ErrSessionLimit},
		{"unknown result", 401, `{"result":"Something"}`, zyxel.ErrUnauthorized},
		{"html body", 500, `<html>oops</html>`, zyxel.ErrUnauthorized},
		{"no sessionkey", 200, `{"result":"ZCFG_SUCCESS"}`, zyxel.ErrUnauthorized},
		{"envelope", 200, `{"content":"abc","iv":"def"}`, zyxel.ErrEncryptedEnvelope},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/getRSAPublickKey" {
					_, _ = w.Write([]byte(`{"RSAPublicKey":"None","result":"ZCFG_SUCCESS"}`))
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			c, _ := zyxel.New(srv.URL)
			if err := c.Login(context.Background(), "admin", "pw"); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLoginNumericSessionKeyAndKeyProbeFailures(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getRSAPublickKey":
			w.WriteHeader(http.StatusNotFound) // non-200: treated as "no key"
		case "/UserLogin":
			_, _ = w.Write([]byte(`{"sessionkey":12345,"result":"ZCFG_SUCCESS"}`))
		}
	}))
	t.Cleanup(srv.Close)
	c, _ := zyxel.New(srv.URL)
	if err := c.Login(context.Background(), "admin", "pw"); err != nil {
		t.Fatal(err)
	}
	if c.SessionKey() != "12345" {
		t.Errorf("numeric sessionkey kept as %q", c.SessionKey())
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/getRSAPublickKey" {
			_, _ = w.Write([]byte(`not json`)) // non-JSON 200: also "no key"
			return
		}
		_, _ = w.Write([]byte(`{"sessionkey":"k","result":"ZCFG_SUCCESS"}`))
	}))
	t.Cleanup(srv2.Close)
	c2, _ := zyxel.New(srv2.URL)
	if err := c2.Login(context.Background(), "admin", "pw"); err != nil {
		t.Fatal(err)
	}
}

func TestLogout(t *testing.T) {
	t.Parallel()
	s := newFake(t, zyxeltest.Options{})
	c, _ := zyxel.New(s.URL)
	ctx := context.Background()

	if err := c.Logout(ctx); !errors.Is(err, zyxel.ErrSessionExpired) {
		t.Errorf("logout before login: %v", err)
	}
	key := login(t, c)
	if err := c.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if c.SessionKey() != "" || s.ActiveSessions() != 0 {
		t.Errorf("after logout: key=%q sessions=%d", c.SessionKey(), s.ActiveSessions())
	}
	reqs := s.Requests()
	last := reqs[len(reqs)-1]
	if last.Method != http.MethodPost || last.Path != "/cgi-bin/UserLogout" || last.Query.Get("sessionkey") != key || last.Header.Get("CSRFToken") != "" {
		t.Errorf("logout request: %s %s %v %v", last.Method, last.Path, last.Query, last.Header)
	}
	if _, err := c.CellwanStatus(ctx); !errors.Is(err, zyxel.ErrSessionExpired) {
		t.Errorf("session still valid after logout: %v", err)
	}

	// Logout after the router dropped the session: APIError, key cleared.
	login(t, c)
	s.ExpireSessions()
	err := c.Logout(ctx)
	var apiErr *zyxel.APIError
	if !errors.Is(err, zyxel.ErrSessionExpired) || !errors.As(err, &apiErr) || apiErr.Op != "logout" || c.SessionKey() != "" {
		t.Errorf("logout on expired session: %v", err)
	}
}
