package logx

import "testing"

func TestSanitizeNStopsAtLimit(t *testing.T) {
	input := "ab\x00cdefghijklmnopqrstuvwxyz"
	if got := SanitizeN(input, 4); got != "ab�c…" {
		t.Fatalf("SanitizeN = %q", got)
	}
}
