package logfile_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ailequal/zy-stats-go/internal/logfile"
)

func TestPathFor(t *testing.T) {
	t.Parallel()
	rome := time.FixedZone("CEST", 2*3600)
	// 23:30 UTC on the 4th is already the 5th in Rome: the local day wins.
	now := time.Date(2025, 6, 4, 23, 30, 0, 0, time.UTC).In(rome)
	if got := logfile.PathFor("logs", now); got != filepath.Join("logs", "2025-06-05.log") {
		t.Errorf("PathFor = %q", got)
	}
	if got := logfile.PathFor("/var/log/zy", time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)); got != filepath.Join("/var/log/zy", "2026-01-09.log") {
		t.Errorf("PathFor = %q", got)
	}
}

func TestEnsureDirAndAppend(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	if err := logfile.EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := logfile.EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir must be idempotent: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}

	path := logfile.PathFor(dir, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	if err := logfile.Append(path, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := logfile.Append(path, []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"a\":1}\n{\"a\":2}\n" {
		t.Errorf("file content = %q", data)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o600 != 0o600 {
		t.Errorf("file mode = %v, want owner read/write", fi.Mode().Perm())
	}
}

func TestAppendErrors(t *testing.T) {
	t.Parallel()
	// Missing directory: Append does not create it (EnsureDir does).
	if err := logfile.Append(filepath.Join(t.TempDir(), "missing", "x.log"), []byte("x")); err == nil {
		t.Error("expected an error for a missing directory")
	}
	// A directory in place of the file.
	dir := t.TempDir()
	if err := logfile.Append(dir, []byte("x")); err == nil {
		t.Error("expected an error when the path is a directory")
	}
}

func TestEnsureDirError(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := logfile.EnsureDir(filepath.Join(file, "sub")); err == nil {
		t.Error("expected an error when a path component is a regular file")
	}
}
