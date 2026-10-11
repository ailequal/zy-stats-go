package stats_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ailequal/zy-stats-go/internal/stats"
)

// TestRenderGolden compares Render with the reference reports under
// testdata/stats (colours off, fixed timestamp).
func TestRenderGolden(t *testing.T) {
	t.Parallel()
	matches, err := filepath.Glob(filepath.Join("..", "..", "testdata", "stats", "*.golden.txt"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no goldens found: %v", err)
	}
	for _, golden := range matches {
		name := strings.TrimSuffix(filepath.Base(golden), ".golden.txt")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, err := stats.Derive(loadFixture(t, name), fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			got := stats.Render(s, fixedNow, false)
			if writeGolden(t, golden, []byte(got)) {
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("report differs from the golden (first difference at byte %d)\n got: %q\nwant: %q", firstDiff(got, string(want)), got, string(want))
			}
			if strings.Contains(got, "\x1b[") {
				t.Error("colour=false must not emit ANSI sequences")
			}
		})
	}
}

func firstDiff(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func TestRenderColor(t *testing.T) {
	t.Parallel()
	s, err := stats.Derive(loadFixture(t, "fwa505-V1.70"), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	out := stats.Render(s, fixedNow, true)
	// RSRP -79 in [-130,-60]: 72.9 % → green, round(18.21) = 18 blocks, 7 spaces.
	wantRSRP := "  RSRP:  -79 dBm [\x1b[32m" + strings.Repeat("█", 18) + "\x1b[39m" + strings.Repeat(" ", 7) + "]\n"
	if !strings.Contains(out, wantRSRP) {
		t.Errorf("green RSRP bar missing:\n%q", out)
	}
	// NR RSSI -78 in [-110,-50]: 53.3 % → green as well; RSRQ -11 in [-20,-3]: 52.9 % → green.
	// Force a yellow and a red bar through the SINR range [-5,30].
	low, high := -4.0, 30.0
	s.SINR, s.NSA.SINR = &low, &high
	out = stats.Render(s, fixedNow, true)
	if !strings.Contains(out, "  SINR:   -4 dB  [\x1b[33m"+strings.Repeat("█", 1)+"\x1b[39m"+strings.Repeat(" ", 24)+"]\n") {
		t.Errorf("yellow SINR bar missing:\n%q", out)
	}
	if !strings.Contains(out, "  SINR:   30 dB  [\x1b[31m"+strings.Repeat("█", 25)+"\x1b[39m]\n") {
		t.Errorf("red SINR bar missing:\n%q", out)
	}
	// Colours never touch the surrounding text.
	if strings.Count(out, "\x1b[39m") != 8 {
		t.Errorf("expected 8 coloured bars, got %d resets", strings.Count(out, "\x1b[39m"))
	}
}

func TestRenderEdgeValues(t *testing.T) {
	t.Parallel()
	s, err := stats.Derive(loadFixture(t, "missing-fields"), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	decimal, huge, tiny := -85.5, 500.0, -500.0
	s.RSRP, s.RSRQ, s.SINR = &decimal, &huge, &tiny
	out := stats.Render(s, fixedNow, false)
	for _, want := range []string{
		"  RSRP: -85.5 dBm [" + strings.Repeat("█", 16) + strings.Repeat(" ", 9) + "]\n", // clamp-free, 63.6 % → 15.9 → 16
		"  RSRQ:  500 dB  [" + strings.Repeat("█", 25) + "]\n",                           // clamped to max
		"  SINR: -500 dB  [" + strings.Repeat(" ", 25) + "]\n",                           // clamped to min
		"  RSSI: N/A     [N/A]                      \n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestColorEnabled(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	env := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	cases := []struct {
		name string
		env  map[string]string
		out  *os.File
		want bool
	}{
		{"FORCE_COLOR=1", map[string]string{"FORCE_COLOR": "1"}, file, true},
		{"FORCE_COLOR empty", map[string]string{"FORCE_COLOR": ""}, file, true},
		{"FORCE_COLOR=0", map[string]string{"FORCE_COLOR": "0"}, file, false},
		{"FORCE_COLOR=false", map[string]string{"FORCE_COLOR": "false"}, file, false},
		{"FORCE_COLOR beats NO_COLOR", map[string]string{"FORCE_COLOR": "1", "NO_COLOR": "1"}, file, true},
		{"NO_COLOR", map[string]string{"NO_COLOR": ""}, file, false},
		{"TERM=dumb", map[string]string{"TERM": "dumb"}, file, false},
		{"regular file is not a TTY", map[string]string{}, file, false},
		{"nil output", map[string]string{}, nil, false},
	}
	for _, c := range cases {
		if got := stats.ColorEnabled(c.out, env(c.env)); got != c.want {
			t.Errorf("%s: ColorEnabled = %v, want %v", c.name, got, c.want)
		}
	}
	if stats.ColorEnabled(file, nil) {
		t.Error("nil lookupEnv with a regular file must be false")
	}
}

func TestRenderCompact(t *testing.T) {
	t.Parallel()
	s, err := stats.Derive(loadFixture(t, "fwa505-V1.70"), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	want := "00:00:00  NR5G-NSA  B3 (20MHz/20MHz)  CA None  RSRP  -79 RSRQ  -11 SINR   11 RSSI  -67  |  NR N78 (60MHz/60MHz)  RSRP  -89 RSRQ  -11 SINR   20 RSSI  -78"
	if got := stats.RenderCompact(s, fixedNow, false); got != want {
		t.Errorf("compact line\n got: %q\nwant: %q", got, want)
	}
	// Colour: RSRP -79 is 72.9 % of [-130,-60] → green; SINR 11 is 45.7 % of [-5,30] → yellow;
	// NR SINR 20 is 71.4 % → green; a value above 85 % is red.
	got := stats.RenderCompact(s, fixedNow, true)
	for _, want := range []string{"RSRP \x1b[32m -79\x1b[39m", "SINR \x1b[33m  11\x1b[39m", "SINR \x1b[32m  20\x1b[39m"} {
		if !strings.Contains(got, want) {
			t.Errorf("coloured compact line lacks %q:\n%q", want, got)
		}
	}
	high := -62.0
	s.RSRP = &high
	if got := stats.RenderCompact(s, fixedNow, true); !strings.Contains(got, "RSRP \x1b[31m -62\x1b[39m") {
		t.Errorf("strong RSRP not red:\n%q", got)
	}

	// NSA disabled and missing readings.
	s, err = stats.Derive(loadFixture(t, "lte-plain"), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	got = stats.RenderCompact(s, fixedNow, false)
	if !strings.HasSuffix(got, "  |  NR off") || strings.Contains(got, "\x1b[") {
		t.Errorf("NSA-disabled compact line: %q", got)
	}
	s.RSRQ = nil
	if got := stats.RenderCompact(s, fixedNow, false); !strings.Contains(got, "RSRQ  N/A") {
		t.Errorf("missing reading not shown as N/A: %q", got)
	}
}
