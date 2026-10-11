// Package app runs the zy-stats-go lifecycle: log in, check the session, then
// poll cellwan_status on an interval and print the report or append a JSON
// line to the daily log file, until the context is cancelled.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"time"

	"github.com/ailequal/zy-stats-go/internal/config"
	"github.com/ailequal/zy-stats-go/internal/logfile"
	"github.com/ailequal/zy-stats-go/internal/stats"
	"github.com/ailequal/zy-stats-go/internal/zyxel"
)

// Client is the subset of *zyxel.Client the app uses; tests substitute it.
type Client interface {
	Login(ctx context.Context, username, password string) error
	LoginCheck(ctx context.Context) (json.RawMessage, error)
	CellwanStatus(ctx context.Context) (*zyxel.CellwanStatusResponse, error)
	Logout(ctx context.Context) error
}

// Deps are the side effects injected by main (and replaced by tests).
type Deps struct {
	Stdout, Stderr io.Writer
	Logger         *slog.Logger // diagnostics; nil discards them
	Now            func() time.Time
	NewClient      func(cfg config.Config) (Client, error)
	IsTTY          bool // clear the screen before each report
	Color          bool // ANSI colours in the report
}

// NewLogger returns the diagnostics logger main wires to stderr: a text
// handler with the given threshold. User-facing output stays on stdout and
// is not logged.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// LogoutTimeout bounds the best-effort logout on shutdown.
const LogoutTimeout = 3 * time.Second

// clearScreen clears the terminal and moves the cursor to the top left.
const clearScreen = "\x1b[2J\x1b[H"

// Run returns nil on graceful shutdown (ctx cancelled) or the fatal error.
// Messages meant for the user go to Stdout.
func Run(ctx context.Context, cfg config.Config, deps Deps) error {
	deps = deps.withDefaults()

	// The logs directory is needed only in log mode; create it before logging
	// in so an unwritable path fails fast.
	if cfg.Log {
		if err := logfile.EnsureDir(cfg.LogsDir); err != nil {
			return err
		}
	}
	client, err := deps.NewClient(cfg)
	if err != nil {
		return err
	}

	if err := client.Login(ctx, cfg.Username, cfg.Password); err != nil {
		return connectError(cfg.ServerURL, err)
	}
	deps.Logger.Info("logged in", "server", cfg.ServerURL, "user", cfg.Username)
	check, err := client.LoginCheck(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(deps.Stdout, "loginCheckData %s\n", compactJSON(check))

	defer func() {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), LogoutTimeout)
		defer cancel()
		_ = client.Logout(lctx) // best effort, like closing the browser
	}()

	// Run once immediately, then wait for the interval before repeating
	// (or stop right away with --once).
	for {
		if err := pollWithRelogin(ctx, cfg, deps, client); err != nil {
			return err
		}
		if cfg.Once {
			deps.Logger.Debug("single poll done, exiting")
			return nil
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(deps.Stdout, "\nShutting down...")
			return nil
		case <-time.After(cfg.Interval):
		}
	}
}

// pollWithRelogin runs poll and, when the router answers that the session is
// gone (ErrSessionExpired, ErrUnauthorized), logs in again once and retries
// the poll once. A second failure is fatal. Verified on the FWA505: a new
// login is accepted while the old session is still alive, so no Logout is
// needed first.
func pollWithRelogin(ctx context.Context, cfg config.Config, deps Deps, client Client) error {
	err := poll(ctx, cfg, deps, client)
	if err == nil || !sessionLost(err) {
		return err
	}
	deps.Logger.Warn("session lost, logging in again", "err", err)
	if err := client.Login(ctx, cfg.Username, cfg.Password); err != nil {
		return fmt.Errorf("%w (re-login failed: %w)", ErrSessionLost, connectError(cfg.ServerURL, err))
	}
	deps.Logger.Info("re-login succeeded")
	if err := poll(ctx, cfg, deps, client); err != nil {
		if sessionLost(err) {
			return fmt.Errorf("%w: %w", ErrSessionLost, err)
		}
		return err
	}
	return nil
}

// sessionLost reports whether the router says the session is gone.
func sessionLost(err error) bool {
	return errors.Is(err, zyxel.ErrSessionExpired) || errors.Is(err, zyxel.ErrUnauthorized)
}

// ErrSessionLost is returned when one re-login did not recover the session.
var ErrSessionLost = errors.New("session expired twice in a row; giving up")

// poll is one loop iteration: fetch, derive, then print the report, print a
// compact line, or append a JSON line to the daily file. --compact and --log
// combine: the file gets the JSON line, stdout the compact line.
func poll(ctx context.Context, cfg config.Config, deps Deps, client Client) error {
	if deps.IsTTY && !cfg.Compact {
		fmt.Fprint(deps.Stdout, clearScreen)
	}
	started := time.Now()
	resp, err := client.CellwanStatus(ctx)
	if err != nil {
		return err
	}
	now := deps.Now()
	s, err := stats.Derive(resp, now)
	if err != nil {
		return err
	}
	deps.Logger.Debug("polled cellwan_status", "took", time.Since(started).Round(time.Millisecond), "technology", s.Technology, "band", s.CurrentBand)

	if cfg.Log {
		line, err := s.JSONLine()
		if err != nil {
			return err
		}
		path := logfile.PathFor(cfg.LogsDir, now)
		if err := logfile.Append(path, line); err != nil {
			return err
		}
		if cfg.Compact {
			fmt.Fprintln(deps.Stdout, stats.RenderCompact(s, now, deps.Color))
			return nil
		}
		fmt.Fprintln(deps.Stdout, "Logging stats to file...")
		fmt.Fprintln(deps.Stdout, string(line))
		fmt.Fprintf(deps.Stdout, "Stats logged to %q.\n", path)
		return nil
	}
	if cfg.Compact {
		fmt.Fprintln(deps.Stdout, stats.RenderCompact(s, now, deps.Color))
		return nil
	}
	fmt.Fprintln(deps.Stdout, stats.Render(s, now, deps.Color))
	return nil
}

func (d Deps) withDefaults() Deps {
	if d.Stdout == nil {
		d.Stdout = io.Discard
	}
	if d.Stderr == nil {
		d.Stderr = io.Discard
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewClient == nil {
		d.NewClient = func(cfg config.Config) (Client, error) {
			c, err := zyxel.New(cfg.ServerURL, zyxel.WithTimeout(cfg.Timeout))
			if err != nil {
				return nil, err
			}
			return c, nil
		}
	}
	return d
}

// ConnectError marks a login failure caused by the transport (no HTTP
// answer): DNS, refused connection, TLS, timeout.
type ConnectError struct {
	URL string
	Err error
}

// Error returns the URL and the transport error.
func (e *ConnectError) Error() string {
	return fmt.Sprintf("connect to %q: %v", e.URL, e.Err)
}

// Unwrap returns the transport error.
func (e *ConnectError) Unwrap() error { return e.Err }

// connectError wraps transport failures of Login; API answers pass through.
func connectError(serverURL string, err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return &ConnectError{URL: serverURL, Err: uerr.Err}
	}
	return err
}

func compactJSON(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// UserMessage turns a Run error into the line main prints on stderr.
func UserMessage(err error) string {
	var apiErr *zyxel.APIError
	isAPI := errors.As(err, &apiErr)
	var connErr *ConnectError
	switch {
	case errors.Is(err, zyxel.ErrInvalidCredentials):
		return "Login failed: the router rejected the username or password."
	case errors.Is(err, zyxel.ErrAccountLocked):
		return "Login failed: the account is temporarily locked by the router."
	case errors.Is(err, zyxel.ErrSessionLimit):
		result := ""
		if isAPI {
			result = apiErr.Result
		}
		return fmt.Sprintf("Login failed: another session is active on the router (%s). Log out there or wait.", result)
	case errors.Is(err, ErrSessionLost):
		return "Session expired twice in a row; giving up."
	case isAPI && apiErr.Op == "logincheck":
		return "Login check failed. Check your credentials in the `.env` file."
	case isAPI && apiErr.Op == "login":
		return "Login failed: " + err.Error()
	case errors.As(err, &connErr):
		return fmt.Sprintf("Failed to connect to %q: %v", connErr.URL, connErr.Err)
	case errors.Is(err, stats.ErrNoData):
		return "No valid stats data available to display."
	default:
		return err.Error()
	}
}
