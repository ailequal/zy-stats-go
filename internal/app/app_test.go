package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ailequal/zy-stats-go/internal/app"
	"github.com/ailequal/zy-stats-go/internal/config"
	"github.com/ailequal/zy-stats-go/internal/stats"
	"github.com/ailequal/zy-stats-go/internal/zyxel"
	"github.com/ailequal/zy-stats-go/internal/zyxel/zyxeltest"
)

var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// syncBuffer is a goroutine-safe bytes.Buffer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.b.Write(p)
	if err != nil {
		return n, fmt.Errorf("syncBuffer: %w", err)
	}
	return n, nil
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "cellwan", "fwa505-V1.70.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func baseConfig(t *testing.T, url string) config.Config {
	t.Helper()
	return config.Config{
		ServerURL: url, Username: "admin", Password: "q1w2e3r4",
		Interval: 10 * time.Millisecond,
		LogsDir:  filepath.Join(t.TempDir(), "logs"), Timeout: 5 * time.Second,
	}
}

// waitFor polls cond until true or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestRunLogMode(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t)})
	cfg := baseConfig(t, s.URL)
	cfg.Log = true
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, cfg, app.Deps{Stdout: &out, Now: func() time.Time { return fixedNow }}) }()

	waitFor(t, func() bool { return strings.Count(out.String(), "Stats logged to") >= 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v", err)
	}

	text := out.String()
	for _, want := range []string{
		"loginCheckData {\"result\":\"ZCFG_SUCCESS\"}\n",
		"Logging stats to file...\n",
		`"technology":"NR5G-NSA"`,
		`Stats logged to "` + filepath.Join(cfg.LogsDir, "2026-01-01.log") + `".` + "\n",
		"\nShutting down...\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("stdout lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[2J") {
		t.Error("screen cleared although stdout is not a TTY")
	}
	data, err := os.ReadFile(filepath.Join(cfg.LogsDir, "2026-01-01.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("log lines = %d", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first["timestamp"] != "2026-01-01T00:00:00.000Z" {
		t.Errorf("first line %q: %v", lines[0], err)
	}
	if s.ActiveSessions() != 0 {
		t.Errorf("logout not performed: %d sessions left", s.ActiveSessions())
	}
	reqs := s.Requests()
	if last := reqs[len(reqs)-1]; last.Path != "/cgi-bin/UserLogout" {
		t.Errorf("last request %s, want logout", last.Path)
	}
}

func TestRunPrettyMode(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t)})
	cfg := baseConfig(t, s.URL)
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, cfg, app.Deps{Stdout: &out, Now: func() time.Time { return fixedNow }, IsTTY: true, Color: true})
	}()
	waitFor(t, func() bool { return strings.Count(out.String(), "LTE Network Stats") >= 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "\x1b[2J\x1b[H") || !strings.Contains(text, "Report generated at: 2026-01-01 00:00:00") || !strings.Contains(text, "\x1b[32m") {
		t.Errorf("pretty output incomplete:\n%q", text)
	}
	if strings.Contains(text, "Logging stats to file") {
		t.Error("log-mode message in pretty mode")
	}
	if _, err := os.Stat(cfg.LogsDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("pretty mode created the logs directory: stat err = %v", err)
	}
}

func TestRunLoginFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t)})
	cfg := baseConfig(t, s.URL)
	cfg.Password = "wrong"
	err := app.Run(ctx, cfg, app.Deps{})
	if !errors.Is(err, zyxel.ErrInvalidCredentials) {
		t.Fatalf("err = %v", err)
	}
	if msg := app.UserMessage(err); msg != "Login failed: the router rejected the username or password." {
		t.Errorf("message = %q", msg)
	}

	cfg = baseConfig(t, "http://127.0.0.1:1")
	err = app.Run(ctx, cfg, app.Deps{})
	var cerr *app.ConnectError
	if !errors.As(err, &cerr) || cerr.URL != "http://127.0.0.1:1" || !strings.HasPrefix(app.UserMessage(err), `Failed to connect to "http://127.0.0.1:1": `) {
		t.Errorf("err = %v", err)
	}

	cfg = baseConfig(t, s.URL)
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, []byte("x"), 0o600)
	cfg.LogsDir = filepath.Join(file, "logs")
	cfg.Log = true
	if err := app.Run(ctx, cfg, app.Deps{}); err == nil || !strings.Contains(err.Error(), "logfile: create") {
		t.Errorf("err = %v", err)
	}
}

// stubClient drives the paths the fake cannot produce.
type stubClient struct {
	loginErr, checkErr, statusErr error
	status                        *zyxel.CellwanStatusResponse
	logouts                       int
}

func (c *stubClient) Login(context.Context, string, string) error { return c.loginErr }
func (c *stubClient) LoginCheck(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{ "result" : "ZCFG_SUCCESS" }`), c.checkErr
}

func (c *stubClient) CellwanStatus(context.Context) (*zyxel.CellwanStatusResponse, error) {
	return c.status, c.statusErr
}
func (c *stubClient) Logout(context.Context) error { c.logouts++; return nil }

func withStub(stub *stubClient) app.Deps {
	return app.Deps{NewClient: func(config.Config) (app.Client, error) { return stub, nil }}
}

func TestRunWithStubClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := baseConfig(t, "https://192.168.1.1")

	checkErr := &zyxel.APIError{Op: "logincheck", Status: 401, Result: "Invalid Username or Password", Err: zyxel.ErrUnauthorized}
	stub := &stubClient{checkErr: checkErr}
	err := app.Run(ctx, cfg, withStub(stub))
	if !errors.Is(err, zyxel.ErrUnauthorized) || app.UserMessage(err) != "Login check failed. Check your credentials in the `.env` file." {
		t.Errorf("login check: %v / %q", err, app.UserMessage(err))
	}
	if stub.logouts != 0 {
		t.Error("logout must not run before the login check succeeded")
	}

	stub = &stubClient{statusErr: &zyxel.APIError{Op: "cellwan_status", Status: 500, Err: zyxel.ErrUnexpectedStatus}}
	var out bytes.Buffer
	err = app.Run(ctx, cfg, app.Deps{Stdout: &out, NewClient: withStub(stub).NewClient})
	if !errors.Is(err, zyxel.ErrUnexpectedStatus) || stub.logouts != 1 {
		t.Errorf("poll error: %v, logouts %d", err, stub.logouts)
	}
	if !strings.Contains(out.String(), `loginCheckData {"result":"ZCFG_SUCCESS"}`) {
		t.Errorf("loginCheckData not compacted: %q", out.String())
	}

	stub = &stubClient{status: &zyxel.CellwanStatusResponse{Result: "ZCFG_SUCCESS"}}
	err = app.Run(ctx, cfg, withStub(stub))
	if !errors.Is(err, stats.ErrNoData) || app.UserMessage(err) != "No valid stats data available to display." {
		t.Errorf("empty Object: %v / %q", err, app.UserMessage(err))
	}

	depsErr := app.Deps{NewClient: func(config.Config) (app.Client, error) { return nil, errors.New("boom") }}
	if err := app.Run(ctx, cfg, depsErr); err == nil || err.Error() != "boom" {
		t.Errorf("NewClient error: %v", err)
	}
}

func TestUserMessage(t *testing.T) {
	t.Parallel()
	cases := map[error]string{
		&zyxel.APIError{Op: "login", Status: 401, Result: "Locked User", Err: zyxel.ErrAccountLocked}:     "Login failed: the account is temporarily locked by the router.",
		&zyxel.APIError{Op: "login", Status: 401, Result: "Duplicated login", Err: zyxel.ErrSessionLimit}: "Login failed: another session is active on the router (Duplicated login). Log out there or wait.",
		&zyxel.APIError{Op: "login", Status: 401, Result: "Weird", Err: zyxel.ErrUnauthorized}:            "Login failed: zyxel: login: HTTP 401 (Weird): zyxel: unauthorized",
		zyxel.ErrEncryptionRequired: zyxel.ErrEncryptionRequired.Error(),
		errors.New("plain"):         "plain",
	}
	for err, want := range cases {
		if got := app.UserMessage(err); got != want {
			t.Errorf("UserMessage(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestRunReloginOnceOnExpiredSession(t *testing.T) {
	t.Parallel()
	// 401 is what the FWA505 answers once the session is gone; 504 {"result":"Timeout"}
	// is what it answers on the very first call after the idle timeout (observed
	// 2026-09-17), so both must trigger the single re-login.
	for _, code := range []int{401, 504} {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			t.Parallel()
			runReloginOnce(t, code)
		})
	}
}

func runReloginOnce(t *testing.T, expiredStatus int) {
	t.Helper()
	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t), ExpiredStatus: expiredStatus})
	cfg := baseConfig(t, s.URL)
	cfg.Log = true
	var out, errOut syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, cfg, app.Deps{Stdout: &out, Stderr: &errOut, Logger: app.NewLogger(&errOut, slog.LevelInfo), Now: func() time.Time { return fixedNow }})
	}()

	waitFor(t, func() bool { return strings.Count(out.String(), "Stats logged to") >= 1 })
	s.ExpireSessions() // the router drops the session mid-run
	waitFor(t, func() bool { return strings.Count(out.String(), "Stats logged to") >= 3 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if !strings.Contains(errOut.String(), "session lost") || !strings.Contains(errOut.String(), "logging in again") {
		t.Errorf("no re-login notice on stderr: %q", errOut.String())
	}
	logins := 0
	for _, r := range s.Requests() {
		if r.Path == "/UserLogin" {
			logins++
		}
	}
	if logins != 2 {
		t.Errorf("UserLogin requests = %d, want 2", logins)
	}
}

// failingStatusClient answers CellwanStatus with the given errors in order.
type failingStatusClient struct {
	stubClient
	errs []error
}

func (c *failingStatusClient) CellwanStatus(context.Context) (*zyxel.CellwanStatusResponse, error) {
	if len(c.errs) == 0 {
		return nil, errors.New("no more scripted answers")
	}
	err := c.errs[0]
	c.errs = c.errs[1:]
	return nil, err
}

func TestRunGivesUpAfterSecondExpiry(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(t, "https://192.168.1.1")
	expired := &zyxel.APIError{Op: "cellwan_status", Status: 401, Result: "Invalid Username or Password", Err: zyxel.ErrSessionExpired}

	stub := &failingStatusClient{errs: []error{expired, expired}}
	var errOut bytes.Buffer
	err := app.Run(context.Background(), cfg, app.Deps{Stderr: &errOut, NewClient: func(config.Config) (app.Client, error) { return stub, nil }})
	if !errors.Is(err, app.ErrSessionLost) || !errors.Is(err, zyxel.ErrSessionExpired) {
		t.Fatalf("err = %v", err)
	}
	if app.UserMessage(err) != "Session expired twice in a row; giving up." {
		t.Errorf("message = %q", app.UserMessage(err))
	}
	if stub.logouts != 1 {
		t.Errorf("logouts = %d", stub.logouts)
	}

	// Re-login itself failing is fatal too, with the login error attached.
	stub = &failingStatusClient{errs: []error{expired}}
	stub.loginErr = nil
	relogin := &reloginFailsClient{failingStatusClient: stub}
	err = app.Run(context.Background(), cfg, app.Deps{NewClient: func(config.Config) (app.Client, error) { return relogin, nil }})
	if !errors.Is(err, app.ErrSessionLost) || !errors.Is(err, zyxel.ErrInvalidCredentials) {
		t.Errorf("err = %v", err)
	}

	// Other poll errors are not retried.
	stub = &failingStatusClient{errs: []error{&zyxel.APIError{Op: "cellwan_status", Status: 500, Err: zyxel.ErrUnexpectedStatus}, expired}}
	err = app.Run(context.Background(), cfg, app.Deps{NewClient: func(config.Config) (app.Client, error) { return stub, nil }})
	if !errors.Is(err, zyxel.ErrUnexpectedStatus) || len(stub.errs) != 1 {
		t.Errorf("non-session error retried: %v (remaining %d)", err, len(stub.errs))
	}
}

// reloginFailsClient accepts the first Login and rejects the second.
type reloginFailsClient struct {
	*failingStatusClient
	logins int
}

func (c *reloginFailsClient) Login(context.Context, string, string) error {
	c.logins++
	if c.logins > 1 {
		return &zyxel.APIError{Op: "login", Status: 401, Result: "Invalid Username or Password", Err: zyxel.ErrInvalidCredentials}
	}
	return nil
}

// TestRunOnce: --once polls a single time, logs out and returns nil without
// waiting for the interval or a signal, and prints no shutdown line.
func TestRunOnce(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t)})
	cfg := baseConfig(t, s.URL)
	cfg.Once = true
	cfg.Interval = time.Hour // must not be waited for
	var out syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- app.Run(context.Background(), cfg, app.Deps{Stdout: &out, Now: func() time.Time { return fixedNow }})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after a single poll")
	}
	text := out.String()
	if !strings.Contains(text, "LTE Network Stats") || strings.Contains(text, "Shutting down") {
		t.Errorf("stdout: %q", text)
	}
	polls, logouts := 0, 0
	for _, r := range s.Requests() {
		switch {
		case strings.HasPrefix(r.Path, "/cgi-bin/DAL"):
			polls++
		case strings.HasPrefix(r.Path, "/cgi-bin/UserLogout"):
			logouts++
		}
	}
	if polls != 1 || logouts != 1 {
		t.Errorf("polls = %d, logouts = %d, want 1 and 1", polls, logouts)
	}
}

// TestRunCompact: --compact prints one line per poll, never clears the screen,
// and combined with --log keeps the JSON file while replacing the three
// verbose stdout lines with the compact one.
func TestRunCompact(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t)})
	cfg := baseConfig(t, s.URL)
	cfg.Compact, cfg.Once = true, true
	var out syncBuffer
	if err := app.Run(context.Background(), cfg, app.Deps{Stdout: &out, IsTTY: true, Now: func() time.Time { return fixedNow }}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "\x1b[2J") || strings.Contains(text, "LTE Network Stats") {
		t.Errorf("compact mode cleared the screen or printed the full report:\n%s", text)
	}
	if !strings.Contains(text, "00:00:00  NR5G-NSA  B3 (20MHz/20MHz)  CA None  RSRP  -79 RSRQ  -11 SINR   11 RSSI  -67  |  NR N78 (60MHz/60MHz)  RSRP  -89 RSRQ  -11 SINR   20 RSSI  -78\n") {
		t.Errorf("compact line missing:\n%s", text)
	}

	cfg.Log = true
	out = syncBuffer{}
	if err := app.Run(context.Background(), cfg, app.Deps{Stdout: &out, Now: func() time.Time { return fixedNow }}); err != nil {
		t.Fatal(err)
	}
	text = out.String()
	if strings.Contains(text, "Logging stats to file") || strings.Contains(text, `"technology"`) || !strings.Contains(text, "CA None") {
		t.Errorf("compact+log stdout: %q", text)
	}
	data, err := os.ReadFile(filepath.Join(cfg.LogsDir, "2026-01-01.log"))
	if err != nil || !strings.Contains(string(data), `"technology":"NR5G-NSA"`) {
		t.Errorf("log file not written: %v %q", err, data)
	}
}

// TestRunLogsDiagnostics: the logger receives the login notice at info and
// the poll detail at debug; nothing goes to stdout.
func TestRunLogsDiagnostics(t *testing.T) {
	t.Parallel()
	s := zyxeltest.New(t, zyxeltest.Options{Cellwan: fixture(t)})
	cfg := baseConfig(t, s.URL)
	cfg.Once = true
	var out, errOut syncBuffer
	if err := app.Run(context.Background(), cfg, app.Deps{Stdout: &out, Logger: app.NewLogger(&errOut, slog.LevelDebug), Now: func() time.Time { return fixedNow }}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`level=INFO msg="logged in"`, "user=admin", `level=DEBUG msg="polled cellwan_status"`, "technology=NR5G-NSA", `msg="single poll done, exiting"`} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut.String())
		}
	}
	if strings.Contains(out.String(), "level=") {
		t.Errorf("diagnostics leaked to stdout: %q", out.String())
	}
}
