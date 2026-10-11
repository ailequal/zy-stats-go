package stats_test

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ailequal/zy-stats-go/internal/stats"
	"github.com/ailequal/zy-stats-go/internal/zyxel"
)

// update rewrites the golden files from the current code:
// go test ./internal/stats -update (or just golden). Review the diff.
var update = flag.Bool("update", false, "rewrite the golden files under testdata/stats")

var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// writeGolden replaces path with got when -update is set and reports whether it did.
func writeGolden(t *testing.T, path string, got []byte) bool {
	t.Helper()
	if !*update {
		return false
	}
	if err := os.WriteFile(path, got, 0o644); err != nil { //nolint:gosec // golden files are committed, world-readable
		t.Fatal(err)
	}
	return true
}

func loadFixture(t *testing.T, name string) *zyxel.CellwanStatusResponse {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "cellwan", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var resp zyxel.CellwanStatusResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	return &resp
}

// TestJSONGolden compares JSONLine with the reference lines under
// testdata/stats.
func TestJSONGolden(t *testing.T) {
	t.Parallel()
	matches, err := filepath.Glob(filepath.Join("..", "..", "testdata", "stats", "*.golden.json"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no goldens found: %v", err)
	}
	for _, golden := range matches {
		name := strings.TrimSuffix(filepath.Base(golden), ".golden.json")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, err := stats.Derive(loadFixture(t, name), fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.JSONLine()
			if err != nil {
				t.Fatal(err)
			}
			if writeGolden(t, golden, append(got, '\n')) {
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(got)+"\n" != string(want) {
				t.Errorf("JSON differs from the golden\n got: %s\nwant: %s", got, strings.TrimSpace(string(want)))
			}
		})
	}
}

func TestDeriveNoData(t *testing.T) {
	t.Parallel()
	for name, resp := range map[string]*zyxel.CellwanStatusResponse{
		"nil":          nil,
		"empty object": {Result: "ZCFG_SUCCESS"},
	} {
		if _, err := stats.Derive(resp, fixedNow); !errors.Is(err, stats.ErrNoData) {
			t.Errorf("%s: err = %v, want ErrNoData", name, err)
		}
	}
}

func TestDeriveTimestamp(t *testing.T) {
	t.Parallel()
	rome := time.FixedZone("CET", 3600)
	s, err := stats.Derive(loadFixture(t, "fwa505-V1.70"), time.Date(2025, 6, 4, 11, 59, 16, 405_000_000, rome))
	if err != nil {
		t.Fatal(err)
	}
	if s.Timestamp != "2025-06-04T10:59:16.405Z" { // 11:59 at UTC+1
		t.Errorf("timestamp = %q", s.Timestamp)
	}
}

func TestNumOrNA(t *testing.T) {
	t.Parallel()
	var raw zyxel.RawScalar
	_ = json.Unmarshal([]byte(`"1650"`), &raw)
	cases := []struct {
		v    stats.NumOrNA
		json string
		str  string
	}{
		{stats.NA(), `"N/A"`, "N/A"},
		{stats.NumFromInt64(-1), `-1`, "-1"},
		{stats.NumFromInt64Ptr(nil), `"N/A"`, "N/A"},
		{stats.NumFromRaw(raw), `"1650"`, "1650"},
		{stats.NumFromRaw(zyxel.RawScalar{}), `"N/A"`, "N/A"},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.json || c.v.String() != c.str {
			t.Errorf("got %s / %q, want %s / %q", b, c.v.String(), c.json, c.str)
		}
	}
}

func TestDeriveNSADisabledHidesValues(t *testing.T) {
	t.Parallel()
	resp := loadFixture(t, "fwa505-V1.70")
	resp.Object[0].NSAEnable = false
	s, err := stats.Derive(resp, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if s.NSA.Enabled || s.NSA.Band != "N/A" || !s.NSA.PCI.IsNA() || !s.NSA.ARFCN.IsNA() || s.NSA.RSRP != nil || s.NSA.RSSI != nil {
		t.Errorf("NSA values leaked while disabled: %+v", s.NSA)
	}
}

// TestJSONNumberFormatting pins that whole floats marshal without ".0" and
// decimals keep their digits.
func TestJSONNumberFormatting(t *testing.T) {
	t.Parallel()
	resp := loadFixture(t, "missing-fields")
	a, b, c := -79.0, 18.0, -8.5
	resp.Object[0].INTFRSRP, resp.Object[0].INTFSINR, resp.Object[0].INTFRSRQ = &a, &b, &c
	s, err := stats.Derive(resp, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	line, err := s.JSONLine()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"rsrp":-79,`, `"sinr":18,`, `"rsrq":-8.5,`} {
		if !strings.Contains(string(line), want) {
			t.Errorf("%s missing in %s", want, line)
		}
	}
	if strings.Contains(string(line), ".0,") || strings.Contains(string(line), "e+") {
		t.Errorf("unexpected number format in %s", line)
	}
}
