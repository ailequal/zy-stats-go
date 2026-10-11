package stats

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// ANSI colour sequences.
const (
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
	ansiReset  = "\x1b[39m"
)

const (
	barWidth  = 25
	separator = "  -------------------------------------------\n"
	frame     = "  ===========================================\n"
)

// Render builds the terminal report (leading blank lines, two-space
// indentation, trailing "  " without newline, conditional 5G block). The
// timestamp line is "2006-01-02 15:04:05" in now's location.
func Render(s Stats, now time.Time, color bool) string {
	var b strings.Builder
	b.Grow(1400)

	b.WriteString("\n\n")
	b.WriteString(frame)
	b.WriteString("  LTE Network Stats\n")
	b.WriteString(separator)
	b.WriteString("  Technology: " + s.Technology + "\n")
	b.WriteString("  PLMN:       " + s.PLMN + "\n")
	b.WriteString("  ENB ID:     " + s.ENBID.String() + "\n")
	b.WriteString("  Cell ID:    " + s.CellID.String() + "\n")
	b.WriteString("  PCI:        " + s.PCI.String() + "\n")
	b.WriteString("  EARFCN:     " + s.EARFCN.String() + "\n")
	b.WriteString("  LTEItaly:   " + s.LTEItalyLink + "\n")
	b.WriteString(separator)
	b.WriteString("  Primary Band: " + s.CurrentBand + "\n")
	b.WriteString("  CA Bands:     " + s.CAInfo + "\n")
	b.WriteString(separator)
	b.WriteString("  Signal Quality (4G LTE)\n")
	writeSignalBlock(&b, s.RSRP, s.RSRQ, s.SINR, s.RSSI, color)
	b.WriteString(separator)

	if s.NSA.Enabled {
		b.WriteString("  5G NSA Status: Enabled\n")
		b.WriteString("  NR Band:      " + s.NSA.Band + "\n")
		b.WriteString("  NR PCI:       " + s.NSA.PCI.String() + "\n")
		b.WriteString("  NR ARFCN:     " + s.NSA.ARFCN.String() + "\n")
		b.WriteString(separator)
		b.WriteString("  Signal Quality (5G NR)\n")
		writeSignalBlock(&b, s.NSA.RSRP, s.NSA.RSRQ, s.NSA.SINR, s.NSA.RSSI, color)
	} else {
		b.WriteString("  5G NSA Status: Disabled\n")
	}
	b.WriteString(separator)
	b.WriteString("  Report generated at: " + now.Format("2006-01-02 15:04:05") + "\n")
	b.WriteString(frame)
	b.WriteString("  ")
	return b.String()
}

func writeSignalBlock(b *strings.Builder, rsrp, rsrq, sinr, rssi *float64, color bool) {
	b.WriteString("  RSRP: " + signalValue(rsrp, "dBm") + " " + bar(rsrp, -130, -60, barWidth, color) + "\n")
	b.WriteString("  RSRQ: " + signalValue(rsrq, "dB ") + " " + bar(rsrq, -20, -3, barWidth, color) + "\n")
	b.WriteString("  SINR: " + signalValue(sinr, "dB ") + " " + bar(sinr, -5, 30, barWidth, color) + "\n")
	b.WriteString("  RSSI: " + signalValue(rssi, "dBm") + " " + bar(rssi, -110, -50, barWidth, color) + "\n")
}

// signalValue right-aligns the value to 4 characters and appends the unit;
// a missing value is "N/A" padded to 7 characters.
func signalValue(v *float64, unit string) string {
	if v == nil {
		return "N/A    "
	}
	return padStart(formatNumber(*v), 4) + " " + unit
}

// formatNumber prints the shortest decimal form of the values a router
// sends: no exponent, no trailing ".0".
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func padStart(s string, width int) string {
	if n := len(s); n < width {
		return strings.Repeat(" ", width-n) + s
	}
	return s
}

// bar draws a signal bar: "[N/A]" padded to width+2 when the value is missing;
// otherwise the value is clamped to [minV, maxV], the filled part is
// round(pct*width) "█" characters (rounded half up), coloured
// yellow below 50 %, green below 85 %, red otherwise, followed by spaces.
func bar(value *float64, minV, maxV float64, width int, color bool) string {
	if value == nil || math.IsNaN(*value) {
		return padEnd("[N/A]", width+2)
	}
	clamped := math.Max(minV, math.Min(maxV, *value))
	pct := (clamped - minV) / (maxV - minV)
	filled := int(math.Floor(pct*float64(width) + 0.5))
	filledBar := strings.Repeat("█", filled)
	emptyBar := strings.Repeat(" ", width-filled)
	if color {
		switch pc := pct * 100; {
		case pc < 50:
			filledBar = ansiYellow + filledBar + ansiReset
		case pc < 85:
			filledBar = ansiGreen + filledBar + ansiReset
		default:
			filledBar = ansiRed + filledBar + ansiReset
		}
	}
	return "[" + filledBar + emptyBar + "]"
}

func padEnd(s string, width int) string {
	if n := len(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// ColorEnabled decides whether to colour the report: FORCE_COLOR wins ("0"
// and "false" disable, anything else enables), then NO_COLOR disables,
// TERM=dumb disables, otherwise colours are on only when out is a character
// device (a terminal).
func ColorEnabled(out *os.File, lookupEnv func(string) (string, bool)) bool {
	if lookupEnv == nil {
		lookupEnv = func(string) (string, bool) { return "", false }
	}
	if v, ok := lookupEnv("FORCE_COLOR"); ok {
		v = strings.ToLower(strings.TrimSpace(v))
		return v != "0" && v != "false"
	}
	if _, ok := lookupEnv("NO_COLOR"); ok {
		return false
	}
	if v, ok := lookupEnv("TERM"); ok && v == "dumb" {
		return false
	}
	if out == nil {
		return false
	}
	info, err := out.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// signalRange is the [min, max] window used by the bars and the compact line.
type signalRange struct{ minV, maxV float64 }

var (
	rangeRSRP = signalRange{-130, -60}
	rangeRSRQ = signalRange{-20, -3}
	rangeSINR = signalRange{-5, 30}
	rangeRSSI = signalRange{-110, -50}
)

// RenderCompact returns one line per poll, meant to scroll as a history
// instead of replacing the screen: local time, technology, primary band,
// carrier aggregation, the four LTE readings, then the 5G NSA band and
// readings or "NR off". Values are padded to four characters ("N/A" when
// absent) and, with color, tinted with the same thresholds as the bars
// (yellow below 50 % of the range, green below 85 %, red above).
func RenderCompact(s Stats, now time.Time, color bool) string {
	var b strings.Builder
	b.Grow(200)
	b.WriteString(now.Format("15:04:05"))
	b.WriteString("  ")
	b.WriteString(padEnd(s.Technology, 8))
	b.WriteString("  ")
	b.WriteString(s.CurrentBand)
	b.WriteString("  CA ")
	b.WriteString(s.CAInfo)
	b.WriteString("  ")
	writeCompactSignals(&b, s.RSRP, s.RSRQ, s.SINR, s.RSSI, color)
	b.WriteString("  |  ")
	if !s.NSA.Enabled {
		b.WriteString("NR off")
		return b.String()
	}
	b.WriteString("NR ")
	b.WriteString(s.NSA.Band)
	b.WriteString("  ")
	writeCompactSignals(&b, s.NSA.RSRP, s.NSA.RSRQ, s.NSA.SINR, s.NSA.RSSI, color)
	return b.String()
}

func writeCompactSignals(b *strings.Builder, rsrp, rsrq, sinr, rssi *float64, color bool) {
	b.WriteString("RSRP " + compactValue(rsrp, rangeRSRP, color))
	b.WriteString(" RSRQ " + compactValue(rsrq, rangeRSRQ, color))
	b.WriteString(" SINR " + compactValue(sinr, rangeSINR, color))
	b.WriteString(" RSSI " + compactValue(rssi, rangeRSSI, color))
}

// compactValue pads the reading to four characters and colours it by where
// it falls in r, like bar does for the filled part.
func compactValue(v *float64, r signalRange, color bool) string {
	if v == nil || math.IsNaN(*v) {
		return " N/A"
	}
	text := padStart(formatNumber(*v), 4)
	if !color {
		return text
	}
	clamped := math.Max(r.minV, math.Min(r.maxV, *v))
	switch pc := (clamped - r.minV) / (r.maxV - r.minV) * 100; {
	case pc < 50:
		return ansiYellow + text + ansiReset
	case pc < 85:
		return ansiGreen + text + ansiReset
	default:
		return ansiRed + text + ansiReset
	}
}
