package zyxel

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// CellwanStatusResponse is the body of GET /cgi-bin/DAL?oid=cellwan_status.
type CellwanStatusResponse struct {
	Result            string              `json:"result"`
	ReplyMsg          string              `json:"ReplyMsg"`
	ReplyMsgMultiLang string              `json:"ReplyMsgMultiLang"`
	Object            []CellwanStatusData `json:"Object"`
}

// CellwanStatusData is Object[0] of the cellwan_status response. Signal
// values are *float64 (nil when the key is absent, so the stats layer can
// tell a missing value from zero, and a firmware sending decimals still
// decodes); identifiers are *int64.
type CellwanStatusData struct {
	CELLRoamingEnable bool   `json:"CELL_Roaming_Enable"`
	CELLRoamingStatus string `json:"CELL_Roaming_Status"`
	AntennaStatus     string `json:"Antenna_Status"`

	INTFStatus                  string    `json:"INTF_Status"`
	INTFIMEI                    string    `json:"INTF_IMEI"`
	INTFCurrentAccessTechnology string    `json:"INTF_Current_Access_Technology"`
	INTFNetworkInUse            string    `json:"INTF_Network_In_Use"`
	INTFRSSI                    *float64  `json:"INTF_RSSI"`
	INTFSupportedBands          string    `json:"INTF_Supported_Bands"`
	INTFCurrentBand             string    `json:"INTF_Current_Band"`
	INTFCellID                  *int64    `json:"INTF_Cell_ID"`
	INTFPhyCellID               *int64    `json:"INTF_PhyCell_ID"`
	INTFUplinkBandwidth         Bandwidth `json:"INTF_Uplink_Bandwidth"`
	INTFDownlinkBandwidth       Bandwidth `json:"INTF_Downlink_Bandwidth"`
	INTFRFCN                    RawScalar `json:"INTF_RFCN"`
	INTFRSRP                    *float64  `json:"INTF_RSRP"`
	INTFRSRQ                    *float64  `json:"INTF_RSRQ"`
	INTFRSCP                    *float64  `json:"INTF_RSCP"`
	INTFEcNo                    *float64  `json:"INTF_EcNo"`
	INTFTAC                     *int64    `json:"INTF_TAC"`
	INTFLAC                     *int64    `json:"INTF_LAC"`
	INTFRAC                     *int64    `json:"INTF_RAC"`
	INTFBSIC                    *int64    `json:"INTF_BSIC"`
	INTFSINR                    *float64  `json:"INTF_SINR"`
	INTFCQI                     *float64  `json:"INTF_CQI"`
	INTFMCS                     *float64  `json:"INTF_MCS"`
	INTFRI                      *float64  `json:"INTF_RI"`
	INTFPMI                     *float64  `json:"INTF_PMI"`
	INTFModuleSoftwareVersion   string    `json:"INTF_Module_Software_Version"`

	USIMStatus               string `json:"USIM_Status"`
	USIMIMSI                 string `json:"USIM_IMSI"`
	USIMICCID                string `json:"USIM_ICCID"`
	USIMPINProtection        bool   `json:"USIM_PIN_Protection"`
	USIMPINRemainingAttempts *int64 `json:"USIM_PIN_Remaining_Attempts"`

	PassthruEnable  bool   `json:"Passthru_Enable"`
	PassthruMode    string `json:"Passthru_Mode"`
	PassthruMacAddr string `json:"Passthru_MacAddr"`

	NSAEnable            bool      `json:"NSA_Enable"`
	NSAMCC               string    `json:"NSA_MCC"`
	NSAMNC               string    `json:"NSA_MNC"`
	NSAPhyCellID         *int64    `json:"NSA_PhyCellID"`
	NSAPCIAlias          *int64    `json:"NSA_PCI"` // other firmwares; the FWA505 sends NSA_PhyCellID
	NSARFCN              *int64    `json:"NSA_RFCN"`
	NSABand              string    `json:"NSA_Band"`
	NSARSSI              *float64  `json:"NSA_RSSI"`
	NSAUplinkBandwidth   Bandwidth `json:"NSA_UplinkBandwidth"`
	NSADownlinkBandwidth Bandwidth `json:"NSA_DownlinkBandwidth"`
	NSARSRP              *float64  `json:"NSA_RSRP"`
	NSARSRQ              *float64  `json:"NSA_RSRQ"`
	NSASINR              *float64  `json:"NSA_SINR"`
	SCCInfo              []SCCInfo `json:"SCC_Info"`
}

// NSAPCI returns the 5G physical cell id: NSA_PhyCellID (verified on the
// FWA505) or, when absent, the NSA_PCI alias used by other firmwares. nil when
// neither is present.
func (d *CellwanStatusData) NSAPCI() *int64 {
	if d.NSAPhyCellID != nil {
		return d.NSAPhyCellID
	}
	return d.NSAPCIAlias
}

// SCCInfo is one secondary component carrier of SCC_Info. Field names are
// not prefixed (verified on the FWA505). Idle carriers have Enable false,
// PhysicalCellID -1, an empty Band and sentinel signal values.
type SCCInfo struct {
	Enable            bool      `json:"Enable"`
	PhysicalCellID    *int64    `json:"PhysicalCellID"`
	RFCN              *int64    `json:"RFCN"`
	Band              string    `json:"Band"`
	RSSI              *float64  `json:"RSSI"`
	RSRP              *float64  `json:"RSRP"`
	RSRQ              *float64  `json:"RSRQ"`
	SINR              *float64  `json:"SINR"`
	UplinkBandwidth   Bandwidth `json:"UplinkBandwidth"`
	DownlinkBandwidth Bandwidth `json:"DownlinkBandwidth"`
	CCType            string    `json:"CC_Type"`
	RSCP              *float64  `json:"RSCP"`
	EcNo              *float64  `json:"EcNo"`
	CQI               *float64  `json:"CQI"`
	MCS               *float64  `json:"MCS"`
	RI                *float64  `json:"RI"`
	PMI               *float64  `json:"PMI"`
}

// Bandwidth is a channel bandwidth as sent by the router: a string such as
// "20M" (FWA505) or a numeric 3GPP index (older firmwares). The original JSON
// token is kept and re-emitted verbatim.
type Bandwidth struct{ raw json.RawMessage }

// UnmarshalJSON stores the token. Any valid JSON value is accepted; MHz
// decides what it means.
func (b *Bandwidth) UnmarshalJSON(data []byte) error {
	b.raw = append(b.raw[:0], data...)
	return nil
}

// MarshalJSON re-emits the original token, or null when the field was absent.
func (b Bandwidth) MarshalJSON() ([]byte, error) {
	if len(b.raw) == 0 {
		return []byte("null"), nil
	}
	return append([]byte(nil), b.raw...), nil
}

// IsSet reports whether the key was present in the JSON (null counts as set).
func (b Bandwidth) IsSet() bool { return len(b.raw) > 0 }

// MHz converts the value: a string ending in "M" gives the leading integer
// of the prefix (see parseLeadingInt), or 0 when there is none; a number n
// is a bandwidth index and gives 5*(n-1); anything else (absent, null, other strings, other types)
// gives 0. A fractional index is truncated towards zero.
func (b Bandwidth) MHz() int {
	if len(b.raw) == 0 {
		return 0
	}
	var s string
	if err := json.Unmarshal(b.raw, &s); err == nil {
		if !strings.HasSuffix(s, "M") {
			return 0
		}
		return parseLeadingInt(strings.TrimSuffix(s, "M"))
	}
	var n float64
	if err := json.Unmarshal(b.raw, &n); err == nil {
		return int(5 * (n - 1))
	}
	return 0
}

// parseLeadingInt skips leading white space, takes an optional sign and the
// longest run of decimal digits and ignores the rest; no digits gives 0.
func parseLeadingInt(s string) int {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil { // overflow: 0 is the safe fallback
		return 0
	}
	if neg {
		return -n
	}
	return n
}

// RawScalar keeps the original JSON token of a field that some firmwares send
// as a string and others as a number (INTF_RFCN: "1650" on the FWA505, 3350
// in older logs). It marshals back to the very same token.
type RawScalar struct{ raw json.RawMessage }

// UnmarshalJSON stores the token verbatim.
func (r *RawScalar) UnmarshalJSON(data []byte) error {
	r.raw = append(r.raw[:0], data...)
	return nil
}

// MarshalJSON re-emits the original token, or null when the field was absent.
func (r RawScalar) MarshalJSON() ([]byte, error) {
	if len(r.raw) == 0 {
		return []byte("null"), nil
	}
	return append([]byte(nil), r.raw...), nil
}

// IsSet reports whether the field carried a value (present and not null).
// The stats layer prints "N/A" when it is false.
func (r RawScalar) IsSet() bool {
	return len(r.raw) > 0 && !bytes.Equal(r.raw, []byte("null"))
}

// String returns the token without quotes for a string, the literal text
// otherwise, "" when unset.
func (r RawScalar) String() string {
	if !r.IsSet() {
		return ""
	}
	var s string
	if err := json.Unmarshal(r.raw, &s); err == nil {
		return s
	}
	return string(r.raw)
}

// EncryptedEnvelope is the {content, iv} body returned by firmwares that
// encrypt API responses. It is detected, not decrypted.
type EncryptedEnvelope struct {
	Content string `json:"content"`
	IV      string `json:"iv"`
}

// IsEncryptedEnvelope reports whether raw is an object with non-empty
// "content" and "iv" and no "Object" key.
func IsEncryptedEnvelope(raw json.RawMessage) bool {
	var probe struct {
		Content string          `json:"content"`
		IV      string          `json:"iv"`
		Object  json.RawMessage `json:"Object"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Content != "" && probe.IV != "" && len(probe.Object) == 0
}
