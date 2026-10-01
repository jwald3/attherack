package store

import "testing"

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"27:08", 1628},    // the run time that used to round to 1620
		{"27", 1620},       // plain minutes
		{"27.5", 1650},     // decimal minutes = 27m30s
		{"1:05:30", 3930},  // h:mm:ss
		{"0:45", 45},       // seconds only
		{"0:90", 90},       // over-range seconds still add up
		{"  28:26 ", 1706}, // trimmed
		{"", 0},            // blank logs no duration
		{"abc", 0},         // garbage
		{"27:0x", 0},       // partially unparseable
		{"-5", 0},          // negative rejected
	}
	for _, c := range cases {
		if got := ParseDuration(c.in); got != c.want {
			t.Errorf("ParseDuration(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
