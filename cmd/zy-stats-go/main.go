// Command zy-stats-go fetches signal statistics from a Zyxel FWA505 router and
// prints a report or appends JSON lines to a daily log file.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/ailequal/zy-stats-go/internal/app"
	"github.com/ailequal/zy-stats-go/internal/config"
	"github.com/ailequal/zy-stats-go/internal/stats"
	"github.com/ailequal/zy-stats-go/internal/zyxel"
)

// version is overridden at build time with
// -ldflags "-X main.version=<git describe>"; otherwise the module version
// recorded by the Go toolchain is used (the tag for `go install`, a
// pseudo-version for `go build` in a git checkout), and "dev" when there is
// none.
var version = "dev"

// buildDate is overridden at build time with
// -ldflags "-X main.buildDate=<commit date, RFC 3339>"; otherwise the vcs.time
// recorded by the Go toolchain is used, and "unknown" when there is none.
var buildDate = ""

const envFile = ".env"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main without process globals: exit code 0 on success, help,
// version or signal; 1 on a runtime error; 2 on invalid flags or config.
func run(args []string, stdout, stderr io.Writer) int {
	cfg, action, err := config.Load(args, os.LookupEnv, envFile)
	if err != nil {
		var verr *config.ValidationError
		if errors.As(err, &verr) {
			fmt.Fprintf(stderr, "zy-stats-go: %s\nRun 'zy-stats-go --help' for usage.\n", verr)
			return 2
		}
		fmt.Fprintf(stderr, "zy-stats-go: %v\n", err)
		return 2
	}
	switch action {
	case config.ActionVersion:
		fmt.Fprintln(stdout, versionLine())
		return 0
	case config.ActionHelp:
		fmt.Fprint(stdout, config.Usage())
		return 0
	case config.ActionRun:
	}

	logger := app.NewLogger(stderr, cfg.LogLevel)
	logger.Debug("configuration resolved", "sources", cfg.Sources(), "interval", cfg.Interval, "log", cfg.Log, "compact", cfg.Compact, "once", cfg.Once)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps := app.Deps{
		Stdout: stdout,
		Stderr: stderr,
		Logger: logger,
		Now:    time.Now,
		NewClient: func(cfg config.Config) (app.Client, error) {
			opts := []zyxel.Option{zyxel.WithTimeout(cfg.Timeout)}
			if cfg.Debug {
				opts = append(opts, zyxel.WithDebug(stderr))
			}
			c, err := zyxel.New(cfg.ServerURL, opts...)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	}
	if f, ok := stdout.(*os.File); ok {
		deps.IsTTY = isTerminal(f)
		deps.Color = stats.ColorEnabled(f, os.LookupEnv)
	}

	if err := app.Run(ctx, cfg, deps); err != nil {
		fmt.Fprintln(stderr, app.UserMessage(err))
		return 1
	}
	return 0
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// versionLine is what --version prints: the git version, then the Go
// toolchain, the build date and the platform in parentheses.
func versionLine() string {
	return fmt.Sprintf("%s (%s, built %s, %s/%s)", resolveVersion(), runtime.Version(), resolveBuildDate(), runtime.GOOS, runtime.GOARCH)
}

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func resolveBuildDate() string {
	if buildDate != "" {
		return buildDate
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.time" && s.Value != "" {
				return s.Value
			}
		}
	}
	return "unknown"
}
