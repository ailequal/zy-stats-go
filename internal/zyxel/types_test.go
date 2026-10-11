package zyxel_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ailequal/zy-stats-go/internal/zyxel"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "cellwan", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decode(t *testing.T, name string) *zyxel.CellwanStatusResponse {
	t.Helper()
	var resp zyxel.CellwanStatusResponse
	if err := json.Unmarshal(readFixture(t, name), &resp); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &resp
}

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }
func eqI(a, b *int64) bool   { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
func eqF(a, b *float64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

func TestDecodeFWA505(t *testing.T) {
	t.Parallel()
	resp := decode(t, "fwa505-V1.70.json")
	if resp.Result != "ZCFG_SUCCESS" || len(resp.Object) != 1 {
		t.Fatalf("result=%q objects=%d", resp.Result, len(resp.Object))
	}
	d := &resp.Object[0]
	checks := []struct {
		name string
		ok   bool
	}{
		{"technology", d.INTFCurrentAccessTechnology == "NR5G-NSA"},
		{"network", d.INTFNetworkInUse == "Current_WINDTRE_NR5G-NSA_22288"},
		{"cell id", eqI(d.INTFCellID, i64(55000324))},
		{"pci", eqI(d.INTFPhyCellID, i64(222))},
		{"rfcn string kept", d.INTFRFCN.IsSet() && d.INTFRFCN.String() == "1650"},
		{"rsrp", eqF(d.INTFRSRP, f64(-79))},
		{"dl bandwidth", d.INTFDownlinkBandwidth.MHz() == 20},
		{"ul bandwidth", d.INTFUplinkBandwidth.MHz() == 20},
		{"nsa enabled", d.NSAEnable},
		{"nsa pci from NSA_PhyCellID", eqI(d.NSAPCI(), i64(333))},
		{"nsa alias absent", d.NSAPCIAlias == nil},
		{"nsa rfcn", eqI(d.NSARFCN, i64(638016))},
		{"nsa bandwidth", d.NSADownlinkBandwidth.MHz() == 60},
		{"scc count", len(d.SCCInfo) == 2},
		// The FWA505 reports two SCC slots that are "Enable": true but idle (empty band,
		// PhysicalCellID -1, empty bandwidth); the stats layer must filter them out of CA.
		{"scc idle slots", len(d.SCCInfo) == 2 && d.SCCInfo[0].Enable && d.SCCInfo[0].Band == "" && eqI(d.SCCInfo[0].PhysicalCellID, i64(-1)) && d.SCCInfo[0].DownlinkBandwidth.MHz() == 0 && d.SCCInfo[1].Enable && d.SCCInfo[1].Band == ""},
		{"sim", d.USIMStatus == "DEVST_SIM_RDY" && eqI(d.USIMPINRemainingAttempts, i64(3))},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: unexpected value", c.name)
		}
	}
	// Marshal round trip keeps the raw tokens verbatim.
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if string(back["INTF_RFCN"]) != `"1650"` || string(back["INTF_Downlink_Bandwidth"]) != `"20M"` {
		t.Errorf("round trip changed tokens: %s %s", back["INTF_RFCN"], back["INTF_Downlink_Bandwidth"])
	}
}

func TestDecodeLTEPlain(t *testing.T) {
	t.Parallel()
	d := &decode(t, "lte-plain.json").Object[0]
	if d.INTFCurrentAccessTechnology != "LTE" || d.NSAEnable {
		t.Fatalf("unexpected header fields: %+v", d)
	}
	if got := d.INTFDownlinkBandwidth.MHz(); got != 20 {
		t.Errorf("numeric bandwidth index 5 → %d MHz, want 20", got)
	}
	if !d.INTFRFCN.IsSet() || d.INTFRFCN.String() != "3350" {
		t.Errorf("numeric INTF_RFCN: set=%v string=%q", d.INTFRFCN.IsSet(), d.INTFRFCN.String())
	}
	if out, _ := json.Marshal(d.INTFRFCN); string(out) != "3350" {
		t.Errorf("INTF_RFCN re-emitted as %s", out)
	}
	if !eqI(d.NSAPCI(), i64(0)) || d.NSAPhyCellID != nil {
		t.Errorf("NSA_PCI alias not used: %v", d.NSAPCI())
	}
	if len(d.SCCInfo) != 0 {
		t.Errorf("SCC_Info = %v", d.SCCInfo)
	}
}

func TestDecodeAbsentFields(t *testing.T) {
	t.Parallel()
	var d zyxel.CellwanStatusData
	if err := json.Unmarshal([]byte(`{"INTF_Status":"Up","INTF_RFCN":null}`), &d); err != nil {
		t.Fatal(err)
	}
	if d.INTFRSRP != nil || d.INTFCellID != nil || d.NSAPCI() != nil {
		t.Error("absent numeric fields must decode to nil")
	}
	if d.INTFRFCN.IsSet() || d.INTFRFCN.String() != "" {
		t.Error("null INTF_RFCN must count as unset")
	}
	if d.INTFDownlinkBandwidth.IsSet() || d.INTFDownlinkBandwidth.MHz() != 0 {
		t.Error("absent bandwidth must be unset and 0 MHz")
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]json.RawMessage
	_ = json.Unmarshal(out, &back)
	if string(back["INTF_RFCN"]) != "null" || string(back["INTF_Downlink_Bandwidth"]) != "null" {
		t.Errorf("unset tokens must marshal as null: %s %s", back["INTF_RFCN"], back["INTF_Downlink_Bandwidth"])
	}
}

func TestBandwidthMHz(t *testing.T) {
	t.Parallel()
	cases := []struct {
		token string
		want  int
	}{
		{`"20M"`, 20},
		{`"60M"`, 60},
		{`"1.4M"`, 1},
		{`" 15M"`, 15},
		{`"-5M"`, -5},
		{`""`, 0},
		{`"junk"`, 0},
		{`"M"`, 0},
		{`"abcM"`, 0},
		{`"20"`, 0},
		{`5`, 20},
		{`3`, 10},
		{`0`, -5},
		{`1`, 0},
		{`6`, 25},
		{`5.5`, 22},
		{`null`, 0},
		{`true`, 0},
		{`{}`, 0},
		{`[]`, 0},
	}
	for _, c := range cases {
		var b zyxel.Bandwidth
		if err := json.Unmarshal([]byte(c.token), &b); err != nil {
			t.Fatalf("%s: %v", c.token, err)
		}
		if got := b.MHz(); got != c.want {
			t.Errorf("Bandwidth(%s).MHz() = %d, want %d", c.token, got, c.want)
		}
		if out, _ := json.Marshal(b); string(out) != c.token {
			t.Errorf("Bandwidth(%s) marshals to %s", c.token, out)
		}
	}
	var zero zyxel.Bandwidth
	if zero.IsSet() || zero.MHz() != 0 {
		t.Error("zero Bandwidth must be unset and 0")
	}
}

func TestRawScalar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		token string
		set   bool
		str   string
	}{
		{`"1650"`, true, "1650"},
		{`3350`, true, "3350"},
		{`-1`, true, "-1"},
		{`1.5`, true, "1.5"},
		{`""`, true, ""},
		{`null`, false, ""},
		{`true`, true, "true"},
	}
	for _, c := range cases {
		var r zyxel.RawScalar
		if err := json.Unmarshal([]byte(c.token), &r); err != nil {
			t.Fatalf("%s: %v", c.token, err)
		}
		if r.IsSet() != c.set || r.String() != c.str {
			t.Errorf("RawScalar(%s): set=%v str=%q, want %v %q", c.token, r.IsSet(), r.String(), c.set, c.str)
		}
		if out, _ := json.Marshal(r); string(out) != c.token {
			t.Errorf("RawScalar(%s) marshals to %s", c.token, out)
		}
	}
}

func TestIsEncryptedEnvelope(t *testing.T) {
	t.Parallel()
	if !zyxel.IsEncryptedEnvelope(readFixture(t, "envelope.json")) {
		t.Error("envelope.json must be detected")
	}
	for name, raw := range map[string]string{
		"plaintext fixture": string(readFixture(t, "fwa505-V1.70.json")),
		"missing iv":        `{"content":"abc"}`,
		"empty iv":          `{"content":"abc","iv":""}`,
		"with Object":       `{"content":"abc","iv":"def","Object":[]}`,
		"not an object":     `[1,2]`,
		"invalid json":      `{`,
	} {
		if zyxel.IsEncryptedEnvelope(json.RawMessage(raw)) {
			t.Errorf("%s wrongly detected as envelope", name)
		}
	}
	var env zyxel.EncryptedEnvelope
	if err := json.Unmarshal(readFixture(t, "envelope.json"), &env); err != nil || env.Content == "" || env.IV == "" {
		t.Errorf("EncryptedEnvelope decode: %+v %v", env, err)
	}
}
