package health

import (
	"os"
	"strings"
	"testing"
)

func load(t *testing.T, name string) Report {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParse(t *testing.T) {
	cases := []struct {
		file string
		want Status
	}{
		{"ata_healthy.json", Healthy},
		{"ata_warning.json", Warning},
		{"ata_failing.json", Critical},
		{"nvme_healthy.json", Healthy},
		{"permission_denied.json", Unknown},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			r := load(t, c.file)
			if r.Status != c.want {
				t.Fatalf("status = %s, want %s (reasons %v, msg %q)", r.Status, c.want, r.Reasons, r.Message)
			}
		})
	}
}

func TestParseDetails(t *testing.T) {
	r := load(t, "ata_healthy.json")
	if r.TemperatureC == nil || *r.TemperatureC != 34 {
		t.Errorf("temperature = %v", r.TemperatureC)
	}
	if r.PowerOnHours == nil || *r.PowerOnHours != 12045 {
		t.Errorf("power on hours = %v", r.PowerOnHours)
	}
	if r.Reallocated == nil || *r.Reallocated != 0 {
		t.Errorf("reallocated = %v", r.Reallocated)
	}

	w := load(t, "ata_warning.json")
	joined := strings.Join(w.Reasons, "; ")
	if !strings.Contains(joined, "8 reallocated sectors") || !strings.Contains(joined, "error log") {
		t.Errorf("warning reasons missing detail: %q", joined)
	}

	p := load(t, "permission_denied.json")
	if !strings.Contains(p.Message, "elevated access") {
		t.Errorf("permission message not friendly: %q", p.Message)
	}
}

func TestParseGarbage(t *testing.T) {
	r, err := Parse([]byte("not json"))
	if err == nil || r.Status != Unknown {
		t.Fatalf("expected unknown + error, got %v %v", r.Status, err)
	}
}
