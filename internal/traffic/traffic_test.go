package traffic

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	event, err := Parse("traffic: session=abc addr=1.1.1.1:443 in=120 out=340")
	if err != nil {
		t.Fatal(err)
	}
	if event.SessionID != "abc" || event.Upload != 120 || event.Download != 340 {
		t.Fatalf("event=%#v", event)
	}
}
func TestParseRejectsNoise(t *testing.T) {
	if _, err := Parse("token=secret"); err == nil {
		t.Fatal("noise accepted")
	}
}

func TestResetDue(t *testing.T) {
	loc := time.FixedZone("server", 4*3600)
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, loc)
	cases := []struct {
		policy string
		start  time.Time
		want   bool
	}{{"daily", now.Add(-24 * time.Hour), true}, {"daily", now.Add(-time.Hour), false}, {"weekly", now.Add(-8 * 24 * time.Hour), true}, {"monthly", time.Date(2026, 6, 30, 23, 0, 0, 0, loc), true}, {"never", now.AddDate(-1, 0, 0), false}}
	for _, tc := range cases {
		if got := ResetDue(tc.policy, tc.start, now); got != tc.want {
			t.Errorf("%s got %v want %v", tc.policy, got, tc.want)
		}
	}
}
