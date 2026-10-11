package zyxel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ailequal/zy-stats-go/internal/zyxel"
	"github.com/ailequal/zy-stats-go/internal/zyxel/zyxeltest"
)

func newFake(t *testing.T, o zyxeltest.Options) *zyxeltest.Server {
	t.Helper()
	if o.Cellwan == nil {
		o.Cellwan = readFixture(t, "fwa505-V1.70.json")
	}
	return zyxeltest.New(t, o)
}

// login logs in through the client and fails the test on error.
func login(t *testing.T, c *zyxel.Client) string {
	t.Helper()
	if err := c.Login(context.Background(), "admin", "q1w2e3r4"); err != nil {
		t.Fatal(err)
	}
	return c.SessionKey()
}

func TestNew(t *testing.T) {
	t.Parallel()
	c, err := zyxel.New("https://192.168.1.1///")
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL() != "https://192.168.1.1" || c.SessionKey() != "" {
		t.Errorf("BaseURL = %q, SessionKey = %q", c.BaseURL(), c.SessionKey())
	}
	for _, bad := range []string{"", "192.168.1.1", "ftp://x", "https://", "://x"} {
		if _, err := zyxel.New(bad); err == nil {
			t.Errorf("New(%q) accepted", bad)
		}
	}
}

func TestLoginCheckAndCellwanStatus(t *testing.T) {
	t.Parallel()
	s := newFake(t, zyxeltest.Options{})
	c, err := zyxel.New(s.URL, zyxel.WithUserAgent("zy-stats-go-test"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := c.LoginCheck(ctx); !errors.Is(err, zyxel.ErrUnauthorized) {
		t.Fatalf("LoginCheck before login: %v", err)
	}
	var apiErr *zyxel.APIError
	if err := errorsAsAPI(c.LoginCheck(ctx)); !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Result != "Invalid Username or Password" || apiErr.Op != "logincheck" {
		t.Fatalf("APIError = %+v", apiErr)
	}

	login(t, c)
	raw, err := c.LoginCheck(ctx)
	if err != nil || !strings.Contains(string(raw), "ZCFG_SUCCESS") {
		t.Fatalf("LoginCheck: %s %v", raw, err)
	}
	resp, err := c.CellwanStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Result != "ZCFG_SUCCESS" || len(resp.Object) != 1 || resp.Object[0].INTFCurrentAccessTechnology != "NR5G-NSA" {
		t.Errorf("unexpected response: %+v", resp)
	}

	reqs := s.Requests()
	last := reqs[len(reqs)-1]
	if last.Path != "/cgi-bin/DAL" || last.Query.Get("oid") != "cellwan_status" || last.Query.Get("sessionkey") != c.SessionKey() {
		t.Errorf("DAL request: %s %v, want sessionkey %q", last.Path, last.Query, c.SessionKey())
	}
	if last.Header.Get("Accept") != "application/json, text/javascript, */*; q=0.01" ||
		last.Header.Get("X-Requested-With") != "XMLHttpRequest" ||
		last.Header.Get("User-Agent") != "zy-stats-go-test" {
		t.Errorf("headers: %v", last.Header)
	}
}

func errorsAsAPI(_ json.RawMessage, err error) error { return err }

func TestCellwanStatusErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("expired statuses", func(t *testing.T) {
		t.Parallel()
		for _, code := range []int{401, 403, 302, 504} {
			s := newFake(t, zyxeltest.Options{ExpiredStatus: code})
			c, _ := zyxel.New(s.URL)
			_, err := c.CellwanStatus(ctx)
			var apiErr *zyxel.APIError
			if !errors.Is(err, zyxel.ErrSessionExpired) || !errors.As(err, &apiErr) || apiErr.Status != code {
				t.Errorf("status %d: err = %v", code, err)
			}
		}
	})

	t.Run("envelope", func(t *testing.T) {
		t.Parallel()
		s := newFake(t, zyxeltest.Options{Cellwan: readFixture(t, "envelope.json")})
		c, _ := zyxel.New(s.URL)
		login(t, c)
		if _, err := c.CellwanStatus(ctx); !errors.Is(err, zyxel.ErrEncryptedEnvelope) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("no object", func(t *testing.T) {
		t.Parallel()
		s := newFake(t, zyxeltest.Options{Cellwan: []byte(`{"result":"ZCFG_INVALID_OBJECT","Object":[]}`)})
		c, _ := zyxel.New(s.URL)
		login(t, c)
		_, err := c.CellwanStatus(ctx)
		if !errors.Is(err, zyxel.ErrNoData) || !strings.Contains(err.Error(), "ZCFG_INVALID_OBJECT") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		s := newFake(t, zyxeltest.Options{Cellwan: []byte(`<html>login</html>`)})
		c, _ := zyxel.New(s.URL)
		login(t, c)
		if _, err := c.CellwanStatus(ctx); err == nil || !strings.Contains(err.Error(), "decode response") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("unexpected status", func(t *testing.T) {
		t.Parallel()
		hs := zyxeltest.New(t, zyxeltest.Options{})
		c, _ := zyxel.New(hs.URL + "/nowhere") // every path becomes 404
		_, err := c.CellwanStatus(ctx)
		var apiErr *zyxel.APIError
		if !errors.Is(err, zyxel.ErrUnexpectedStatus) || !errors.As(err, &apiErr) || apiErr.Status != 404 {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()
		c, _ := zyxel.New("http://127.0.0.1:1")
		if _, err := c.CellwanStatus(ctx); err == nil || !strings.HasPrefix(err.Error(), "zyxel: cellwan_status: ") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestSessionKeyRotation(t *testing.T) {
	t.Parallel()
	s := newFake(t, zyxeltest.Options{RotateSessionKey: true})
	c, _ := zyxel.New(s.URL)
	first := login(t, c)
	if _, err := c.CellwanStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := c.SessionKey()
	if got == "" || got == first || len(got) != 64 {
		t.Errorf("SessionKey after rotation = %q (login key %q)", got, first)
	}
	if keys := s.SessionKeys(); len(keys) != 1 || keys[0] != got {
		t.Errorf("fake holds %v", keys)
	}
}

func TestTLSInsecureTransport(t *testing.T) {
	t.Parallel()
	s := newFake(t, zyxeltest.Options{TLS: true})
	c, err := zyxel.New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	// A TLS handshake failure would surface as a transport error, not as 401.
	if _, err := c.LoginCheck(context.Background()); !errors.Is(err, zyxel.ErrUnauthorized) {
		t.Fatalf("self-signed certificate not accepted: %v", err)
	}
	login(t, c)
	if _, err := c.CellwanStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDebugDumpRedacts(t *testing.T) {
	t.Parallel()
	s := newFake(t, zyxeltest.Options{})
	var buf bytes.Buffer
	c, _ := zyxel.New(s.URL, zyxel.WithDebug(&buf))
	login(t, c)
	if _, err := c.CellwanStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "--- request") || !strings.Contains(out, "--- response") || !strings.Contains(out, "GET /cgi-bin/DAL?oid=cellwan_status&sessionkey=<redacted>") {
		t.Fatalf("dump incomplete:\n%s", out)
	}
	if k := c.SessionKey(); k == "" || strings.Contains(out, k) {
		t.Errorf("sessionkey %q leaked into the dump", k)
	}
	// Jar cookies are added by http.Client after DumpRequestOut runs, so the
	// request dump never shows them; Set-Cookie redaction is asserted in the
	// Login tests. Here: nothing recorded by the fake may leak.
	for _, sess := range s.Requests() {
		if v := sess.Header.Get("Cookie"); v != "" && strings.Contains(out, v) {
			t.Errorf("raw cookie %q leaked into the dump", v)
		}
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()
	in := "GET /cgi-bin/DAL?oid=cellwan_status&sessionkey=KEY1 HTTP/1.1\r\n" +
		"POST /cgi-bin/UserLogout?sessionkey=KEY2 HTTP/1.1\r\n" +
		"Cookie: Session=abc123; Path=/\r\nSet-Cookie: Session=def; path=/; HttpOnly\r\n" +
		`{"Input_Account":"admin","Input_Passwd":"cTF3","content":"AAA","key":"BBB","iv":"CCC","sessionkey":"KEY3"}` + "\r\n" +
		`{"sessionkey":12345,"result":"ZCFG_SUCCESS"}`
	got := zyxel.Redact(in)
	for _, leaked := range []string{"abc123", "Session=def", `"cTF3"`, `"AAA"`, `"BBB"`, "KEY1", "KEY2", "KEY3", "12345"} {
		if strings.Contains(got, leaked) {
			t.Errorf("%q leaked: %s", leaked, got)
		}
	}
	for _, kept := range []string{
		"admin", `"iv":"CCC"`, "Cookie: Session=<redacted>; Path=/", `"Input_Passwd":"<redacted>"`,
		"oid=cellwan_status&sessionkey=<redacted> HTTP/1.1", "UserLogout?sessionkey=<redacted> HTTP/1.1",
		`"sessionkey":"<redacted>"`, `"sessionkey":<redacted>,"result":"ZCFG_SUCCESS"`,
	} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q missing: %s", kept, got)
		}
	}
}

// TestCellwanStatusSessionKeyChecks covers firmwares that reject DAL reads
// without the current sessionkey and rotate it on every response.
func TestCellwanStatusSessionKeyChecks(t *testing.T) {
	t.Parallel()
	s := newFake(t, zyxeltest.Options{SessionKeyOnDAL: true, RotateSessionKey: true})
	c, err := zyxel.New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	keys := []string{login(t, c)}
	for i := range 3 {
		if _, err := c.CellwanStatus(ctx); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
		keys = append(keys, c.SessionKey())
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] == keys[i-1] {
			t.Errorf("sessionkey not rotated after poll %d: %q", i, keys[i])
		}
	}
	if err := c.Logout(ctx); err != nil {
		t.Errorf("logout with the rotated sessionkey: %v", err)
	}
}

func TestWithHTTPClientAndTimeout(t *testing.T) {
	t.Parallel()
	s := newFake(t, zyxeltest.Options{})
	custom := &http.Client{}
	c, err := zyxel.New(s.URL, zyxel.WithHTTPClient(custom), zyxel.WithTimeout(0))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPClient() != custom {
		t.Error("WithHTTPClient not applied")
	}
	// No jar: the cookie is lost, so every API call stays unauthorized.
	if _, err := c.LoginCheck(context.Background()); !errors.Is(err, zyxel.ErrUnauthorized) {
		t.Errorf("err = %v", err)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	t.Parallel()
	e := &zyxel.APIError{Op: "cellwan_status", Status: 401, Result: "Invalid Username or Password", Err: zyxel.ErrSessionExpired}
	if got := e.Error(); got != "zyxel: cellwan_status: HTTP 401 (Invalid Username or Password): zyxel: session expired" {
		t.Errorf("Error() = %q", got)
	}
	e2 := &zyxel.APIError{Op: "logincheck", Status: 500, Err: zyxel.ErrUnauthorized}
	if got := e2.Error(); got != "zyxel: logincheck: HTTP 500: zyxel: unauthorized" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(e, zyxel.ErrSessionExpired) {
		t.Error("Unwrap broken")
	}
}
