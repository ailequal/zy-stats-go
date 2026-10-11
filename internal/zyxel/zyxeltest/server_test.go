package zyxeltest_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ailequal/zy-stats-go/internal/zyxel/zyxeltest"
)

// handClient is a minimal hand-written client, independent of internal/zyxel,
// so the fake is tested on its own.
type handClient struct {
	t    *testing.T
	base string
	hc   *http.Client
}

func newHandClient(t *testing.T, s *zyxeltest.Server) *handClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // self-signed test server
	}
	return &handClient{t: t, base: s.URL, hc: hc}
}

func (c *handClient) do(method, path string, body []byte) (int, []byte) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, c.base+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, data
}

func (c *handClient) login(user, pw string) (int, map[string]any) {
	c.t.Helper()
	body, _ := json.Marshal(map[string]any{
		"Input_Account": user, "Input_Passwd": base64.StdEncoding.EncodeToString([]byte(pw)),
		"currLang": "en", "RememberPassword": 0, "SHA512_password": false,
	})
	status, data := c.do(http.MethodPost, "/UserLogin", body)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return status, m
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "cellwan", "fwa505-V1.70.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHappyPath(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t)})
	c := newHandClient(t, s)

	status, data := c.do(http.MethodGet, "/getRSAPublickKey", nil)
	if status != 200 || !strings.Contains(string(data), `"RSAPublicKey":"None"`) {
		t.Fatalf("rsa: %d %s", status, data)
	}

	status, login := c.login("admin", "q1w2e3r4")
	key, _ := login["sessionkey"].(string)
	if status != 200 || login["result"] != "ZCFG_SUCCESS" || len(key) != 64 || login["loginAccount"] != "admin" {
		t.Fatalf("login: %d %v", status, login)
	}
	if s.ActiveSessions() != 1 {
		t.Fatalf("sessions = %d", s.ActiveSessions())
	}

	if status, data := c.do(http.MethodGet, "/cgi-bin/UserLoginCheck", nil); status != 200 || !strings.Contains(string(data), "ZCFG_SUCCESS") {
		t.Fatalf("check: %d %s", status, data)
	}
	status, data = c.do(http.MethodGet, "/cgi-bin/DAL?oid=cellwan_status", nil)
	if status != 200 || !bytes.Equal(data, fixture(t)) {
		t.Fatalf("dal: %d (%d bytes)", status, len(data))
	}
	if status, data := c.do(http.MethodGet, "/cgi-bin/DAL?oid=other", nil); status != 200 || !strings.Contains(string(data), "ZCFG_INVALID_OBJECT") {
		t.Fatalf("unknown oid: %d %s", status, data)
	}

	if status, _ := c.do(http.MethodPost, "/cgi-bin/UserLogout?sessionkey=wrong", nil); status != 401 {
		t.Fatalf("logout with wrong key: %d", status)
	}
	if status, _ := c.do(http.MethodPost, "/cgi-bin/UserLogout?sessionkey="+key, nil); status != 200 {
		t.Fatalf("logout: %d", status)
	}
	if status, data := c.do(http.MethodGet, "/cgi-bin/UserLoginCheck", nil); status != 401 || !strings.Contains(string(data), "Invalid Username or Password") {
		t.Fatalf("after logout: %d %s", status, data)
	}
	if s.ActiveSessions() != 0 {
		t.Fatalf("sessions after logout = %d", s.ActiveSessions())
	}

	reqs := s.Requests()
	if len(reqs) != 8 || reqs[1].Method != http.MethodPost || reqs[1].Path != "/UserLogin" || !bytes.Contains(reqs[1].Body, []byte(`"Input_Account":"admin"`)) {
		t.Errorf("recorded requests: %d, second = %+v", len(reqs), reqs[1])
	}
}

func TestLoginFailures(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{Username: "root", Password: "pw", MaxSessions: 1})
	c := newHandClient(t, s)
	if status, m := c.login("root", "nope"); status != 401 || m["result"] != "Invalid Username or Password" {
		t.Errorf("wrong password: %d %v", status, m)
	}
	if status, m := c.login("admin", "pw"); status != 401 || m["result"] != "Invalid Username or Password" {
		t.Errorf("wrong user: %d %v", status, m)
	}
	if status, data := c.do(http.MethodPost, "/UserLogin", []byte("not json")); status != 401 || !strings.Contains(string(data), "Invalid Username or Password") {
		t.Errorf("malformed body: %d %s", status, data)
	}
	if status, _ := c.login("root", "pw"); status != 200 {
		t.Fatalf("first login failed: %d", status)
	}
	if status, m := newHandClient(t, s).login("root", "pw"); status != 401 || m["result"] != "Duplicated login" {
		t.Errorf("second session: %d %v", status, m)
	}
	if status, _ := c.do(http.MethodGet, "/UserLogin", nil); status != http.StatusNotFound { // the catch-all route answers 404 for a wrong method
		t.Errorf("GET /UserLogin = %d", status)
	}
	if status, _ := c.do(http.MethodGet, "/nowhere", nil); status != 404 {
		t.Errorf("unknown route = %d", status)
	}
}

func TestUnauthenticatedAndExpired(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t)})
	c := newHandClient(t, s)
	for _, p := range []string{"/cgi-bin/UserLoginCheck", "/cgi-bin/DAL?oid=cellwan_status"} {
		if status, data := c.do(http.MethodGet, p, nil); status != 401 || !strings.Contains(string(data), "Invalid Username or Password") {
			t.Errorf("%s without cookie: %d %s", p, status, data)
		}
	}
	if status, _ := c.do(http.MethodPost, "/cgi-bin/UserLogout?sessionkey=x", nil); status != 401 {
		t.Error("logout without cookie must be 401")
	}
	c.login("admin", "q1w2e3r4")
	s.ExpireSessions()
	if status, _ := c.do(http.MethodGet, "/cgi-bin/DAL?oid=cellwan_status", nil); status != 401 {
		t.Errorf("after ExpireSessions: %d", status)
	}
}

func TestExpiredStatusVariants(t *testing.T) {
	t.Parallel()
	for _, code := range []int{403, 302} {
		s := zyxeltest.New(t, zyxeltest.Options{ExpiredStatus: code})
		c := newHandClient(t, s)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL+"/cgi-bin/UserLoginCheck", nil)
		resp, err := c.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != code {
			t.Errorf("status = %d, want %d", resp.StatusCode, code)
		}
		if code == 302 && resp.Header.Get("Location") != "/login" {
			t.Errorf("302 without Location: %v", resp.Header)
		}
	}
}

func TestSessionKeyOnDALAndRotation(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t), SessionKeyOnDAL: true, RotateSessionKey: true})
	c := newHandClient(t, s)
	_, login := c.login("admin", "q1w2e3r4")
	key, _ := login["sessionkey"].(string)

	if status, _ := c.do(http.MethodGet, "/cgi-bin/DAL?oid=cellwan_status", nil); status != 401 {
		t.Fatalf("DAL without sessionkey must be 401, got %d", status)
	}
	status, data := c.do(http.MethodGet, "/cgi-bin/DAL?oid=cellwan_status&sessionkey="+key, nil)
	if status != 200 {
		t.Fatalf("DAL with sessionkey: %d", status)
	}
	var resp struct {
		SessionKey string          `json:"sessionkey"`
		Object     json.RawMessage `json:"Object"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || len(resp.SessionKey) != 64 || resp.SessionKey == key || len(resp.Object) == 0 {
		t.Fatalf("rotated response: %v key=%q", err, resp.SessionKey)
	}
	if status, _ := c.do(http.MethodGet, "/cgi-bin/DAL?oid=cellwan_status&sessionkey="+key, nil); status != 401 {
		t.Error("old sessionkey must be rejected after rotation")
	}
	if keys := s.SessionKeys(); len(keys) != 1 || keys[0] != resp.SessionKey {
		t.Errorf("SessionKeys = %v", keys)
	}
	if status, _ := c.do(http.MethodPost, "/cgi-bin/UserLogout?sessionkey="+resp.SessionKey, nil); status != 200 {
		t.Error("logout with the rotated key must succeed")
	}
}

func TestTLSAndSetCellwanAndRSAKey(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{TLS: true, Cellwan: []byte(`{"Object":[]}`), RSAPublicKey: "-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----"})
	if !strings.HasPrefix(s.URL, "https://") {
		t.Fatalf("URL = %s", s.URL)
	}
	c := newHandClient(t, s)
	if _, data := c.do(http.MethodGet, "/getRSAPublickKey", nil); !strings.Contains(string(data), "BEGIN PUBLIC KEY") {
		t.Errorf("custom RSA key not served: %s", data)
	}
	if status, _ := c.login("admin", "q1w2e3r4"); status != 200 {
		t.Fatalf("login over TLS: %d", status)
	}
	s.SetCellwan([]byte(`{"Object":[{"INTF_Status":"Up"}]}`))
	if _, data := c.do(http.MethodGet, "/cgi-bin/DAL?oid=cellwan_status", nil); string(data) != `{"Object":[{"INTF_Status":"Up"}]}` {
		t.Errorf("SetCellwan not applied: %s", data)
	}
	if _, data := c.do(http.MethodGet, "/getBasicInformation", nil); !strings.Contains(string(data), `"ModelName":"FAKE"`) {
		t.Errorf("basic information: %s", data)
	}
}

func TestStartOnAddr(t *testing.T) {
	t.Parallel()
	s, err := zyxeltest.Start(zyxeltest.Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if !strings.HasPrefix(s.URL, "http://127.0.0.1:") {
		t.Errorf("URL = %s", s.URL)
	}
	if _, err := zyxeltest.Start(zyxeltest.Options{Addr: "256.0.0.1:1"}); err == nil {
		t.Error("expected a listen error")
	}
}
