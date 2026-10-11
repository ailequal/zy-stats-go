// Package logfile writes the daily JSON-lines log files, one line per poll
// (logs/YYYY-MM-DD.log, appended).
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// EnsureDir creates dir and its parents when missing.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // world-readable log directory (umask applies)
		return fmt.Errorf("logfile: create %q: %w", dir, err)
	}
	return nil
}

// PathFor returns dir/2006-01-02.log for the calendar day of now in now's
// location (the app passes time.Now(), so the day is the local one).
func PathFor(dir string, now time.Time) string {
	return filepath.Join(dir, now.Format("2006-01-02")+".log")
}

// Append writes line plus "\n" to path, creating the file when missing.
// The close error is reported.
func Append(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // world-readable log file; path comes from the config
	if err != nil {
		return fmt.Errorf("logfile: open %q: %w", path, err)
	}
	buf := make([]byte, 0, len(line)+1)
	buf = append(buf, line...)
	buf = append(buf, '\n')
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return fmt.Errorf("logfile: write %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("logfile: close %q: %w", path, err)
	}
	return nil
}
