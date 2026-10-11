// Package config resolves the zy-stats-go configuration from command line
// flags, the process environment, an optional .env file and defaults, in
// that order of precedence. It is pure: no os.Exit, no globals, no reads of
// the real environment. main wires os.Args[1:], os.LookupEnv and ".env".
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is the resolved, validated configuration.
type Config struct {
	ServerURL string        // scheme://host[:port], no trailing slash
	Username  string        // login user
	Password  string        // login password
	Interval  time.Duration // polling interval, >= 1s
	Log       bool          // append JSON lines to a daily file instead of printing the report
	Compact   bool          // one line per poll on stdout instead of the full report
	Once      bool          // poll a single time, then exit 0
	LogsDir   string        // directory for the daily log files, default "logs"
	LogLevel  slog.Level    // diagnostics threshold on stderr, default info
	Debug     bool          // LogLevel is debug: redacted HTTP dump on stderr
	Timeout   time.Duration // per-request timeout, default 15s, no flag

	sources map[string]string
}

// Sources reports where each setting came from, keyed by the variable name
// (ZY_SERVER_URL, ZY_USERNAME, ...): "flag", "env" (process environment),
// "file" or "default".
func (c Config) Sources() map[string]string {
	out := make(map[string]string, len(c.sources))
	for k, v := range c.sources {
		out[k] = v
	}
	return out
}

// Action tells main what to do after Load.
type Action int

// Actions returned by Load.
const (
	ActionRun Action = iota
	ActionVersion
	ActionHelp
)

// ValidationError describes one invalid flag or setting. main prints it as
// "zy-stats-go: <field>: <reason>" and exits 2.
type ValidationError struct {
	Field  string
	Reason string
}

// Error returns "<field>: <reason>".
func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }

// Defaults that are not user-facing or need a value when nothing is set.
const (
	DefaultLogsDir  = "logs"
	DefaultLogLevel = slog.LevelInfo
	DefaultTimeout  = 15 * time.Second
)

// Environment variable names, read from the process environment and the env file.
const (
	KeyServerURL = "ZY_SERVER_URL"
	KeyUsername  = "ZY_USERNAME"
	KeyPassword  = "ZY_PASSWORD"
	KeyInterval  = "ZY_INTERVAL"
	KeyLogsDir   = "ZY_LOGS_DIR"
	KeyLogLevel  = "ZY_LOG_LEVEL"
)

// Source labels used by Sources.
const (
	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceFile    = "file"
	SourceDefault = "default"
)

// flagSpec links one setting to its long and short flag names.
type flagSpec struct {
	key   string
	long  string
	short string
}

var stringFlags = []flagSpec{
	{key: KeyServerURL, long: "server-url", short: "s"},
	{key: KeyUsername, long: "username", short: "u"},
	{key: KeyPassword, long: "password", short: "p"},
	{key: KeyInterval, long: "interval", short: "i"},
	{key: KeyLogsDir, long: "logs-dir", short: ""},
	{key: KeyLogLevel, long: "log-level", short: ""},
}

// Load merges, in order of decreasing precedence: CLI args, the process
// environment (lookupEnv), the env file (a missing file is not an error),
// then defaults. Empty values from the environment or the file count as
// unset. --version wins over --help.
func Load(args []string, lookupEnv func(string) (string, bool), envFile string) (Config, Action, error) {
	fs := flag.NewFlagSet("zy-stats-go", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	values := make(map[string]*string, len(stringFlags))
	for _, s := range stringFlags {
		v := new(string)
		values[s.key] = v
		fs.StringVar(v, s.long, "", "")
		if s.short != "" {
			fs.StringVar(v, s.short, "", "")
		}
	}
	var logFlag, compact, once, debug, showVersion, showHelp bool
	fs.BoolVar(&logFlag, "log", false, "")
	fs.BoolVar(&logFlag, "l", false, "")
	fs.BoolVar(&compact, "compact", false, "")
	fs.BoolVar(&compact, "c", false, "")
	fs.BoolVar(&once, "once", false, "")
	fs.BoolVar(&debug, "debug", false, "")
	fs.BoolVar(&showVersion, "version", false, "")
	fs.BoolVar(&showVersion, "V", false, "")
	fs.BoolVar(&showHelp, "help", false, "")
	fs.BoolVar(&showHelp, "h", false, "")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Config{}, ActionHelp, nil
		}
		return Config{}, ActionRun, &ValidationError{Field: "flags", Reason: err.Error()}
	}
	if fs.NArg() > 0 {
		return Config{}, ActionRun, &ValidationError{Field: "flags", Reason: fmt.Sprintf("unexpected argument %q", fs.Arg(0))}
	}
	if showVersion {
		return Config{}, ActionVersion, nil
	}
	if showHelp {
		return Config{}, ActionHelp, nil
	}

	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		for _, s := range stringFlags {
			if f.Name == s.long || (s.short != "" && f.Name == s.short) {
				set[s.key] = true
			}
		}
	})

	fileValues, err := readEnvFile(envFile)
	if err != nil {
		return Config{}, ActionRun, err
	}

	if lookupEnv == nil {
		lookupEnv = func(string) (string, bool) { return "", false }
	}
	sources := make(map[string]string, len(stringFlags))
	resolve := func(key, def string) string {
		if set[key] {
			sources[key] = SourceFlag
			return *values[key]
		}
		if v, ok := lookupEnv(key); ok && v != "" {
			sources[key] = SourceEnv
			return v
		}
		if v, ok := fileValues[key]; ok && v != "" {
			sources[key] = SourceFile
			return v
		}
		sources[key] = SourceDefault
		return def
	}

	cfg := Config{
		Log:     logFlag,
		Compact: compact,
		Once:    once,
		Timeout: DefaultTimeout,
		sources: sources,
	}

	cfg.ServerURL, err = parseServerURL(resolve(KeyServerURL, ""))
	if err != nil {
		return Config{}, ActionRun, err
	}
	if cfg.Username = resolve(KeyUsername, ""); cfg.Username == "" {
		return Config{}, ActionRun, &ValidationError{Field: "username", Reason: "required (--username or ZY_USERNAME)"}
	}
	if cfg.Password = resolve(KeyPassword, ""); cfg.Password == "" {
		return Config{}, ActionRun, &ValidationError{Field: "password", Reason: "required (--password or ZY_PASSWORD)"}
	}
	cfg.Interval, err = parseInterval(resolve(KeyInterval, ""))
	if err != nil {
		return Config{}, ActionRun, err
	}
	cfg.LogsDir = resolve(KeyLogsDir, DefaultLogsDir)
	cfg.LogLevel, err = parseLogLevel(resolve(KeyLogLevel, ""))
	if err != nil {
		return Config{}, ActionRun, err
	}
	if debug {
		// --debug is shorthand for --log-level debug and wins over the variable.
		cfg.LogLevel = slog.LevelDebug
		sources[KeyLogLevel] = SourceFlag
	}
	cfg.Debug = cfg.LogLevel <= slog.LevelDebug

	return cfg, ActionRun, nil
}

// parseLogLevel accepts debug, info, warn (or warning) and error, case-insensitive;
// empty means the default.
func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return DefaultLogLevel, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, &ValidationError{Field: "log-level", Reason: fmt.Sprintf("%q must be one of debug, info, warn, error", raw)}
	}
}

// readEnvFile parses envFile with godotenv without touching the process
// environment. A missing file yields an empty map and no error.
func readEnvFile(envFile string) (map[string]string, error) {
	if envFile == "" {
		return map[string]string{}, nil
	}
	values, err := godotenv.Read(envFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, &ValidationError{Field: "env-file", Reason: fmt.Sprintf("%s: %v", envFile, err)}
	}
	return values, nil
}

// parseServerURL validates the router URL. The scheme must be https (the
// only supported setup) or http (accepted for local testing against the fake
// modem; the client refuses to log in when a real RSA key is served over
// http). Trailing slashes are trimmed so the .env.example advice is harmless.
func parseServerURL(raw string) (string, error) {
	if raw == "" {
		return "", &ValidationError{Field: "server-url", Reason: "required (--server-url or ZY_SERVER_URL)"}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", &ValidationError{Field: "server-url", Reason: fmt.Sprintf("%q is not a valid URL: %v", raw, err)}
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", &ValidationError{Field: "server-url", Reason: fmt.Sprintf("%q must start with https:// (http:// only for local testing)", raw)}
	}
	if u.Host == "" {
		return "", &ValidationError{Field: "server-url", Reason: fmt.Sprintf("%q has no host", raw)}
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", &ValidationError{Field: "server-url", Reason: fmt.Sprintf("%q must be scheme://host[:port] only", raw)}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

// parseInterval accepts an integer number of seconds >= 1.
func parseInterval(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, &ValidationError{Field: "interval", Reason: "required (--interval or ZY_INTERVAL)"}
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 0, &ValidationError{Field: "interval", Reason: fmt.Sprintf("%q must be an integer number of seconds >= 1", raw)}
	}
	return time.Duration(n) * time.Second, nil
}

// Usage returns the help text printed by --help.
func Usage() string {
	return `Usage: zy-stats-go [options]

Fetch the Zyxel Nebula FWA505 signal stats from the CLI.

Options:
  -V, --version              output the version number
  -s, --server-url <url>     server URL
  -u, --username <username>  username for login
  -p, --password <password>  password for login
  -i, --interval <seconds>   interval in seconds for fetching stats
  -l, --log                  log stats into a file (default: false)
  -c, --compact              print one line per poll instead of the full report
      --once                 fetch the stats once, then exit
      --logs-dir <path>      directory for log files (default: ./logs)
      --log-level <level>    diagnostics on stderr: debug, info, warn, error (default: info)
      --debug                same as --log-level debug; includes the redacted HTTP exchanges
  -h, --help                 display help for command

Environment: ZY_SERVER_URL, ZY_USERNAME, ZY_PASSWORD, ZY_INTERVAL, ZY_LOGS_DIR
and ZY_LOG_LEVEL, from the process environment or from ./.env.
`
}
