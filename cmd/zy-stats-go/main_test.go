package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ailequal/zy-stats-go/internal/zyxel/zyxeltest"
)

func TestRunExitCodes(t *testing.T) {
	// Not parallel: run reads the process environment and the .env of the
	// working directory (absent in the package directory).
	t.Setenv("ZY_SERVER_URL", "")
	t.Setenv("ZY_USERNAME", "")
	t.Setenv("ZY_PASSWORD", "")
	t.Setenv("ZY_INTERVAL", "")
	logs := filepath.Join(t.TempDir(), "logs")

	cases := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"version", []string{"--version"}, 0, "dev (go", ""},
		{"bad log level", []string{"-s", "https://x", "-u", "a", "-p", "b", "-i", "1", "--log-level", "loud"}, 2, "", "zy-stats-go: log-level:"},
		{"help", []string{"-h"}, 0, "Usage: zy-stats-go [options]", ""},
		{"unknown flag", []string{"--nope"}, 2, "", "zy-stats-go: flags: flag provided but not defined: -nope\nRun 'zy-stats-go --help' for usage.\n"},
		{"missing config", []string{"--server-url", "https://x"}, 2, "", "zy-stats-go: username: required"},
		{"connection refused", []string{"-s", "http://127.0.0.1:1", "-u", "a", "-p", "b", "-i", "1", "--logs-dir", logs}, 1, "", `Failed to connect to "http://127.0.0.1:1": `},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != tc.code {
				t.Errorf("exit = %d, want %d (stderr %q)", code, tc.code, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.stdout) || !strings.Contains(stderr.String(), tc.stderr) {
				t.Errorf("stdout %q stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunWrongPasswordAgainstFake(t *testing.T) {
	s := zyxeltest.New(t, zyxeltest.Options{})
	var stdout, stderr bytes.Buffer
	code := run([]string{"-s", s.URL, "-u", "admin", "-p", "wrong", "-i", "1", "--logs-dir", filepath.Join(t.TempDir(), "logs"), "--debug"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "Login failed: the router rejected the username or password.") {
		t.Errorf("exit %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--- request") || strings.Contains(stderr.String(), "d3Jvbmc=") {
		t.Errorf("debug dump missing or password leaked:\n%s", stderr.String())
	}
}

func TestResolveVersion(t *testing.T) {
	if got := resolveVersion(); got == "" {
		t.Error("empty version")
	}
	if _, err := os.Stat("main.go"); err != nil {
		t.Skip("not in the package directory")
	}
}

func fakeWithFixture(t *testing.T) *zyxeltest.Server {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "cellwan", "fwa505-V1.70.json"))
	if err != nil {
		t.Fatal(err)
	}
	return zyxeltest.New(t, zyxeltest.Options{Cellwan: b})
}

func TestRunOnceCompactAgainstFake(t *testing.T) {
	s := fakeWithFixture(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-s", s.URL, "-u", "admin", "-p", "q1w2e3r4", "-i", "60", "--once", "--compact", "--logs-dir", filepath.Join(t.TempDir(), "logs"), "--log-level", "debug"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "  |  NR N78 (60MHz/60MHz)  ") || strings.Contains(stdout.String(), "Shutting down") {
		t.Errorf("stdout %q", stdout.String())
	}
	for _, want := range []string{`msg="configuration resolved"`, `msg="logged in"`, "--- request"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
}

func TestVersionLine(t *testing.T) {
	v := versionLine()
	if !strings.HasPrefix(v, resolveVersion()+" (go") || !strings.Contains(v, ", built ") || !strings.HasSuffix(v, "/"+runtimeArch()+")") {
		t.Errorf("version line = %q", v)
	}
}

func runtimeArch() string { return runtime.GOARCH }
