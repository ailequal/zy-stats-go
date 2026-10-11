// Package stats derives the user-facing network statistics from a
// cellwan_status response and renders them as a JSON line or a terminal
// report. The goldens under testdata/stats pin the exact output.
package stats

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/ailequal/zy-stats-go/internal/zyxel"
)

// ErrNoData is returned when the response has no Object[0].
var ErrNoData = errors.New("stats: no data in the response")

const na = "N/A"

// NumOrNA is a value printed as a number (or the original string
// token of INTF_RFCN) when known and as the string "N/A" otherwise.
type NumOrNA struct{ raw json.RawMessage }

// NA returns the "N/A" value.
func NA() NumOrNA { return NumOrNA{} }

// NumFromInt64 wraps an integer.
func NumFromInt64(v int64) NumOrNA {
	return NumOrNA{raw: json.RawMessage(strconv.FormatInt(v, 10))}
}

// NumFromInt64Ptr wraps a pointer, "N/A" when nil.
func NumFromInt64Ptr(v *int64) NumOrNA {
	if v == nil {
		return NA()
	}
	return NumFromInt64(*v)
}

// NumFromRaw keeps the original token of a RawScalar, "N/A" when unset.
func NumFromRaw(r zyxel.RawScalar) NumOrNA {
	if !r.IsSet() {
		return NA()
	}
	b, err := r.MarshalJSON()
	if err != nil {
		return NA()
	}
	return NumOrNA{raw: b}
}

// IsNA reports whether the value is "N/A".
func (n NumOrNA) IsNA() bool { return len(n.raw) == 0 }

// MarshalJSON emits the original token, or "N/A".
func (n NumOrNA) MarshalJSON() ([]byte, error) {
	if n.IsNA() {
		return []byte(`"N/A"`), nil
	}
	return append([]byte(nil), n.raw...), nil
}

// String returns the value as the report prints it: the number, the string
// token without quotes, or "N/A".
func (n NumOrNA) String() string {
	if n.IsNA() {
		return na
	}
	var s string
	if err := json.Unmarshal(n.raw, &s); err == nil {
		return s
	}
	return string(n.raw)
}

// NSA is the 5G NSA block of the JSON output.
type NSA struct {
	Enabled bool     `json:"enabled"`
	Band    string   `json:"band"`
	PCI     NumOrNA  `json:"pci"`
	ARFCN   NumOrNA  `json:"arfcn"`
	RSRP    *float64 `json:"rsrp,omitempty"`
	RSRQ    *float64 `json:"rsrq,omitempty"`
	SINR    *float64 `json:"sinr,omitempty"`
	RSSI    *float64 `json:"rssi,omitempty"`
}

// Stats is the derived output. Field order is the JSON key order of the log
// files and must not change (existing log files are processed line by line).
type Stats struct {
	Technology   string   `json:"technology"`
	PLMN         string   `json:"plmn"`
	ENBID        NumOrNA  `json:"enbId"`
	CellID       NumOrNA  `json:"cellId"`
	PCI          NumOrNA  `json:"pci"`
	EARFCN       NumOrNA  `json:"earfcn"`
	LTEItalyLink string   `json:"lteItalyLink"`
	CurrentBand  string   `json:"currentBand"`
	CAInfo       string   `json:"caInfo"`
	RSRP         *float64 `json:"rsrp,omitempty"`
	RSRQ         *float64 `json:"rsrq,omitempty"`
	SINR         *float64 `json:"sinr,omitempty"`
	RSSI         *float64 `json:"rssi,omitempty"`
	NSA          NSA      `json:"nsa"`
	Timestamp    string   `json:"timestamp"` // RFC 3339 UTC with milliseconds and "Z"
}

// Derive computes the stats from a cellwan_status response. now becomes the
// timestamp.
func Derive(resp *zyxel.CellwanStatusResponse, now time.Time) (Stats, error) {
	if resp == nil || len(resp.Object) == 0 {
		return Stats{}, ErrNoData
	}
	d := &resp.Object[0]

	s := Stats{
		Technology:   orNA(d.INTFCurrentAccessTechnology),
		PLMN:         plmnFromNetwork(d.INTFNetworkInUse),
		CellID:       NumFromInt64Ptr(d.INTFCellID),
		PCI:          NumFromInt64Ptr(d.INTFPhyCellID),
		EARFCN:       NumFromRaw(d.INTFRFCN),
		LTEItalyLink: na,
		CurrentBand:  na,
		CAInfo:       "None",
		RSRP:         d.INTFRSRP,
		RSRQ:         d.INTFRSRQ,
		SINR:         d.INTFSINR,
		RSSI:         d.INTFRSSI,
		Timestamp:    now.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
	}

	s.ENBID = NA()
	if d.INTFCellID != nil {
		enb := *d.INTFCellID / 256 // integer division truncates towards zero
		s.ENBID = NumFromInt64(enb)
		// 22250 with a six-digit eNB id is really 22288.
		if s.PLMN == "22250" && len(strconv.FormatInt(enb, 10)) == 6 {
			s.PLMN = "22288"
		}
	}

	if d.INTFCurrentBand != "" {
		s.CurrentBand = formatBandDetails(d.INTFCurrentBand, d.INTFDownlinkBandwidth.MHz(), d.INTFUplinkBandwidth.MHz())
	}

	if len(d.SCCInfo) > 0 {
		parts := make([]string, 0, len(d.SCCInfo))
		for _, scc := range d.SCCInfo {
			if scc.Enable && scc.Band != "" {
				parts = append(parts, formatBandDetails(scc.Band, 0, 0))
			}
		}
		if joined := strings.Join(parts, " + "); joined != "" {
			s.CAInfo = joined
		}
	}

	s.NSA = NSA{Enabled: d.NSAEnable, Band: na, PCI: NA(), ARFCN: NA()}
	if d.NSAEnable {
		s.NSA.PCI = NumFromInt64Ptr(d.NSAPCI())
		s.NSA.ARFCN = NumFromInt64Ptr(d.NSARFCN)
		if d.NSABand != "" {
			s.NSA.Band = formatBandDetails(d.NSABand, d.NSADownlinkBandwidth.MHz(), d.NSAUplinkBandwidth.MHz())
		}
		s.NSA.RSRP, s.NSA.RSRQ, s.NSA.SINR, s.NSA.RSSI = d.NSARSRP, d.NSARSRQ, d.NSASINR, d.NSARSSI
	}

	if s.PLMN != na && !s.ENBID.IsNA() {
		s.LTEItalyLink = "https://lteitaly.it/internal/map.php#bts=" + s.PLMN + "." + s.ENBID.String()
	}
	return s, nil
}

// JSONLine returns the compact JSON object, without a trailing newline.
func (s Stats) JSONLine() ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err //nolint:wrapcheck // marshalling a plain struct; the error is unreachable in practice
	}
	return b, nil
}

func orNA(s string) string {
	if s == "" {
		return na
	}
	return s
}

// plmnFromNetwork extracts the PLMN from "Current_<operator>_<tech>_<plmn>"
// and applies the fixed operator remaps. "N/A" when absent.
func plmnFromNetwork(network string) string {
	if network == "" {
		return na
	}
	parts := strings.Split(network, "_")
	if len(parts) <= 3 {
		return na
	}
	switch plmn := parts[3]; plmn {
	case "22201":
		return "2221"
	case "22299":
		return "22288"
	default:
		return plmn
	}
}

// formatBandDetails formats a band with its bandwidths: "LTE_BC7", 20, 10 -> "B7 (20MHz/10MHz)";
// "N78", 60, 60 -> "N78 (60MHz/60MHz)". The quirks are intentional (a lowercase
// "b28" becomes "Bb28"; a 0 side is printed when the other side is > 0).
func formatBandDetails(band string, dlMHz, ulMHz int) string {
	var out string
	if band != "" {
		switch {
		case strings.HasPrefix(band, "N") || strings.HasPrefix(band, "n"):
			out = strings.ToUpper(band)
		case strings.Contains(band, "LTE_BC"):
			out = "B" + band[6:]
		case strings.HasPrefix(band, "B"):
			out = "B" + band[1:]
		default:
			out = "B" + band
		}
	}
	if dlMHz > 0 || ulMHz > 0 {
		out += " (" + strconv.Itoa(dlMHz) + "MHz/" + strconv.Itoa(ulMHz) + "MHz)"
	}
	return strings.TrimSpace(out)
}
