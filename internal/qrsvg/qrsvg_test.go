package qrsvg

import (
	"strings"
	"testing"
)

func TestEncode(t *testing.T) {
	svg, err := Encode("http://192.168.1.20:8790/u/abc")
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, `fill="#000"`) {
		t.Fatalf("svg = %.60s %v", svg, err)
	}
	term, err := Terminal("http://192.168.1.20:8790/u/abc")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(term), "\n")
	if len(lines) < 10 || !strings.ContainsAny(term, "▀▄") {
		t.Fatalf("terminal qr:\n%s", term)
	}
}
