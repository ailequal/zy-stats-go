package stats

import "testing"

func TestFormatBandDetails(t *testing.T) {
	t.Parallel()
	cases := []struct {
		band   string
		dl, ul int
		want   string
	}{
		{"LTE_BC7", 20, 10, "B7 (20MHz/10MHz)"},
		{"N78", 60, 60, "N78 (60MHz/60MHz)"},
		{"n78", 80, 0, "N78 (80MHz/0MHz)"},
		{"B3", 0, 0, "B3"},
		{"B3", 20, 20, "B3 (20MHz/20MHz)"},
		{"7", 0, 0, "B7"},
		{"b28", 0, 10, "Bb28 (0MHz/10MHz)"}, // lowercase prefix kept as is
		{"", 0, 0, ""},
		{"", 20, 20, "(20MHz/20MHz)"},
		{"LTE_BC20", 0, 0, "B20"},
	}
	for _, c := range cases {
		if got := formatBandDetails(c.band, c.dl, c.ul); got != c.want {
			t.Errorf("formatBandDetails(%q,%d,%d) = %q, want %q", c.band, c.dl, c.ul, got, c.want)
		}
	}
}

func TestPLMNFromNetwork(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                               "N/A",
		"Current_WINDTRE_NR5G-NSA_22288": "22288",
		"Current_TIM_LTE_22201":          "2221",
		"Current_Vodafone_LTE_22299":     "22288",
		"Current_Iliad_NR5G-NSA_22250":   "22250", // remapped only in Derive, with a six-digit eNB id
		"Current_Op_LTE":                 "N/A",
		"Current_Op_LTE_22210_extra":     "22210",
	}
	for in, want := range cases {
		if got := plmnFromNetwork(in); got != want {
			t.Errorf("plmnFromNetwork(%q) = %q, want %q", in, got, want)
		}
	}
}
