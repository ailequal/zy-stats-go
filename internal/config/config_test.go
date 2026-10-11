package config_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ailequal/zy-stats-go/internal/config"
)

func envFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var baseArgs = []string{"--server-url", "https://192.168.1.1", "-u", "admin", "-p", "secret", "-i", "30"}

func TestLoadFlagsOnly(t *testing.T) {
	t.Parallel()
	cfg, action, err := config.Load(append(baseArgs, "-l", "--debug", "--logs-dir", "out"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if action != config.ActionRun {
		t.Fatalf("action = %v, want ActionRun", action)
	}
	want := config.Config{
		ServerURL: "https://192.168.1.1",
		Username:  "admin",
		Password:  "secret",
		Interval:  30 * time.Second,
		Log:       true,
		LogsDir:   "out",
		LogLevel:  slog.LevelDebug,
		Debug:     true,
		Timeout:   15 * time.Second,
	}
	assertConfig(t, cfg, want)
	if src := cfg.Sources(); src[config.KeyUsername] != config.SourceFlag || src[config.KeyLogsDir] != config.SourceFlag || src[config.KeyLogLevel] != config.SourceFlag {
		t.Errorf("sources = %v", src)
	}
}

func TestLoadModesAndLogLevel(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(append(baseArgs, "--once", "-c", "--log-level", "WARN"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Once || !cfg.Compact || cfg.LogLevel != slog.LevelWarn || cfg.Debug {
		t.Errorf("modes not applied: %+v", cfg)
	}
	// --debug is shorthand for --log-level debug and wins over the variable.
	cfg, _, err = config.Load(append(baseArgs, "--debug"), envFrom(map[string]string{"ZY_LOG_LEVEL": "error"}), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != slog.LevelDebug || !cfg.Debug || cfg.Sources()[config.KeyLogLevel] != config.SourceFlag {
		t.Errorf("--debug: level %v debug %v sources %v", cfg.LogLevel, cfg.Debug, cfg.Sources())
	}
	// The variable alone sets the level and Debug.
	cfg, _, err = config.Load(baseArgs, envFrom(map[string]string{"ZY_LOG_LEVEL": "debug"}), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != slog.LevelDebug || !cfg.Debug || cfg.Sources()[config.KeyLogLevel] != config.SourceEnv {
		t.Errorf("ZY_LOG_LEVEL: level %v debug %v sources %v", cfg.LogLevel, cfg.Debug, cfg.Sources())
	}
	// Invalid level.
	_, _, err = config.Load(append(baseArgs, "--log-level", "loud"), nil, "")
	var verr *config.ValidationError
	if !errors.As(err, &verr) || verr.Field != "log-level" || !strings.Contains(verr.Reason, "debug, info, warn, error") {
		t.Errorf("err = %v", err)
	}
}

// TestLoadIgnoresUnprefixed pins that only the ZY_ names are read: the bare
// names (USERNAME is also the OS user on some systems) are ignored in both
// the process environment and the env file.
func TestLoadIgnoresUnprefixed(t *testing.T) {
	t.Parallel()
	file := writeEnvFile(t, "PASSWORD=\"filepw\"\nLOGS_DIR=\"filelogs\"\n")
	env := map[string]string{
		"SERVER_URL": "https://env.example",
		"USERNAME":   "osuser",
		"INTERVAL":   "7",
	}
	_, _, err := config.Load(nil, envFrom(env), file)
	var verr *config.ValidationError
	if !errors.As(err, &verr) || verr.Field != "server-url" || !strings.Contains(verr.Reason, "ZY_SERVER_URL") {
		t.Fatalf("err = %v, want server-url required", err)
	}
	cfg, _, err := config.Load([]string{"-s", "https://flag.example", "-u", "admin", "-p", "secret", "-i", "30"}, envFrom(env), file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Username != "admin" || cfg.Password != "secret" || cfg.Interval != 30*time.Second || cfg.LogsDir != config.DefaultLogsDir {
		t.Errorf("unprefixed names leaked into the config: %+v", cfg)
	}
}

func TestLoadPrecedence(t *testing.T) {
	t.Parallel()
	file := writeEnvFile(t, "ZY_SERVER_URL=\"https://file.example\"\nZY_USERNAME=\"fileuser\"\nZY_PASSWORD=\"filepw\"\nZY_INTERVAL=\"5\"\nZY_LOGS_DIR=\"filelogs\"\n")
	env := map[string]string{
		"ZY_SERVER_URL": "https://env.example",
		"ZY_USERNAME":   "envuser",
		"ZY_INTERVAL":   "10",
		"ZY_LOGS_DIR":   "", // empty counts as unset
	}
	cfg, _, err := config.Load([]string{"--server-url", "https://flag.example/"}, envFrom(env), file)
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		ServerURL: "https://flag.example", // flag wins, trailing slash trimmed
		Username:  "envuser",              // env over file
		Password:  "filepw",               // file over default
		Interval:  10 * time.Second,       // env over file
		LogsDir:   "filelogs",             // empty env is unset, file wins
		LogLevel:  slog.LevelInfo,
		Timeout:   15 * time.Second,
	}
	assertConfig(t, cfg, want)
	src := cfg.Sources()
	wantSrc := map[string]string{
		"ZY_SERVER_URL": "flag", "ZY_USERNAME": "env", "ZY_PASSWORD": "file",
		"ZY_INTERVAL": "env", "ZY_LOGS_DIR": "file", "ZY_LOG_LEVEL": "default",
	}
	for k, v := range wantSrc {
		if src[k] != v {
			t.Errorf("source[%s] = %q, want %q", k, src[k], v)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(baseArgs, nil, filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatalf("missing env file must not be an error: %v", err)
	}
	if cfg.LogsDir != "logs" || cfg.Log || cfg.Compact || cfg.Once || cfg.Debug || cfg.LogLevel != slog.LevelInfo {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if src := cfg.Sources(); src[config.KeyLogsDir] != config.SourceDefault {
		t.Errorf("sources = %v", src)
	}
}

// TestLoadEnvExample pins godotenv behaviour on the repository's .env.example
// (quoted values followed by an inline comment).
func TestLoadEnvExample(t *testing.T) {
	t.Parallel()
	example := filepath.Join("..", "..", ".env.example")
	if _, err := os.Stat(example); err != nil {
		t.Fatal(err)
	}
	cfg, action, err := config.Load(nil, nil, example)
	if err != nil {
		t.Fatal(err)
	}
	if action != config.ActionRun {
		t.Fatalf("action = %v", action)
	}
	want := config.Config{
		ServerURL: "https://192.168.1.1",
		Username:  "admin",
		Password:  "q1w2e3r4",
		Interval:  30 * time.Second,
		LogsDir:   "logs",
		LogLevel:  slog.LevelInfo,
		Timeout:   15 * time.Second,
	}
	assertConfig(t, cfg, want)
	for k, v := range cfg.Sources() {
		if v != config.SourceFile {
			t.Errorf("source[%s] = %q, want file", k, v)
		}
	}
}

func TestLoadActions(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		args []string
		want config.Action
	}{
		"version long":      {[]string{"--version"}, config.ActionVersion},
		"version short":     {[]string{"-V"}, config.ActionVersion},
		"help long":         {[]string{"--help"}, config.ActionHelp},
		"help short":        {[]string{"-h"}, config.ActionHelp},
		"version over help": {[]string{"--help", "--version"}, config.ActionVersion},
		"no validation":     {[]string{"--version", "--interval", "abc"}, config.ActionVersion},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, action, err := config.Load(tc.args, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if action != tc.want {
				t.Errorf("action = %v, want %v", action, tc.want)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	replace := func(flagName, value string) []string {
		out := make([]string, 0, len(baseArgs))
		for i := 0; i < len(baseArgs); i += 2 {
			if baseArgs[i] == flagName {
				if value != "" {
					out = append(out, flagName, value)
				}
				continue
			}
			out = append(out, baseArgs[i], baseArgs[i+1])
		}
		return out
	}
	cases := map[string]struct {
		args      []string
		field     string
		reasonHas string
	}{
		"unknown flag":     {[]string{"--nope"}, "flags", "flag provided but not defined: -nope"},
		"positional":       {append(append([]string{}, baseArgs...), "extra"), "flags", `unexpected argument "extra"`},
		"missing url":      {replace("--server-url", ""), "server-url", "required"},
		"bad scheme":       {replace("--server-url", "ftp://192.168.1.1"), "server-url", "must start with https://"},
		"no host":          {replace("--server-url", "https://"), "server-url", "has no host"},
		"query in url":     {replace("--server-url", "https://192.168.1.1/?x=1"), "server-url", "scheme://host[:port] only"},
		"missing username": {replace("-u", ""), "username", "required"},
		"missing password": {replace("-p", ""), "password", "required"},
		"missing interval": {replace("-i", ""), "interval", "required"},
		"interval not int": {replace("-i", "abc"), "interval", `"abc" must be an integer number of seconds >= 1`},
		"interval zero":    {replace("-i", "0"), "interval", "must be an integer"},
		"interval float":   {replace("-i", "1.5"), "interval", "must be an integer"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, err := config.Load(tc.args, nil, "")
			var verr *config.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if verr.Field != tc.field || !strings.Contains(verr.Reason, tc.reasonHas) {
				t.Errorf("got %q, want field %q and reason containing %q", verr.Error(), tc.field, tc.reasonHas)
			}
		})
	}
}

func TestLoadHTTPAcceptedForLocalTesting(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(replaceURL("http://127.0.0.1:8080///"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "http://127.0.0.1:8080" {
		t.Errorf("ServerURL = %q", cfg.ServerURL)
	}
}

func TestLoadMalformedEnvFile(t *testing.T) {
	t.Parallel()
	file := writeEnvFile(t, "PASSWORD=\"unterminated\n")
	_, _, err := config.Load(baseArgs, nil, file)
	var verr *config.ValidationError
	if !errors.As(err, &verr) || verr.Field != "env-file" {
		t.Fatalf("err = %v, want env-file ValidationError", err)
	}
}

func TestUsage(t *testing.T) {
	t.Parallel()
	u := config.Usage()
	for _, want := range []string{"Usage: zy-stats-go [options]", "-s, --server-url <url>", "--logs-dir <path>", "--debug", "--once", "-c, --compact", "--log-level <level>", "ZY_USERNAME", "FWA505"} {
		if !strings.Contains(u, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
	if strings.Contains(u, "no-headless") {
		t.Error("usage still mentions --no-headless")
	}
}

func replaceURL(u string) []string {
	out := append([]string{}, baseArgs...)
	out[1] = u
	return out
}

func assertConfig(t *testing.T, got, want config.Config) {
	t.Helper()
	if got.ServerURL != want.ServerURL || got.Username != want.Username || got.Password != want.Password ||
		got.Interval != want.Interval || got.Log != want.Log || got.Compact != want.Compact || got.Once != want.Once ||
		got.LogsDir != want.LogsDir || got.LogLevel != want.LogLevel ||
		got.Debug != want.Debug || got.Timeout != want.Timeout {
		t.Errorf("config mismatch\n got: %+v\nwant: %+v", got, want)
	}
}
