package handlers

import (
	"testing"
	"time"
)

func TestFormatInterval(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-5 * time.Second, "<1m"},
		{30 * time.Second, "<1m"},
		{time.Minute, "1m"},
		{9*time.Minute + 40*time.Second, "10m"},
		{59 * time.Minute, "59m"},
		{90 * time.Minute, "2h"},
		{23 * time.Hour, "23h"},
		{36 * time.Hour, "2d"},
		{29 * 24 * time.Hour, "29d"},
		{61 * 24 * time.Hour, "2mo"},
		{45 * 24 * time.Hour, "1.5mo"},
		{400 * 24 * time.Hour, "1.1y"},
		{730 * 24 * time.Hour, "2y"},
	}
	for _, c := range cases {
		if got := formatInterval(c.in); got != c.want {
			t.Errorf("formatInterval(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
