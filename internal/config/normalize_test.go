package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func raw(s string) json.RawMessage    { return json.RawMessage(s) }
func rawStr(s string) json.RawMessage { return json.RawMessage(`"` + s + `"`) }

// --- NormalizePorts ---

func TestNormalizePorts_shorthand(t *testing.T) {
	tests := []struct {
		in   string
		want PortBinding
	}{
		{"8080:80", PortBinding{Published: "8080", Target: "80", Protocol: "tcp"}},
		{"80", PortBinding{Target: "80", Protocol: "tcp"}},
		{"127.0.0.1:8080:80", PortBinding{HostIP: "127.0.0.1", Published: "8080", Target: "80", Protocol: "tcp"}},
		{"8080:80/udp", PortBinding{Published: "8080", Target: "80", Protocol: "udp"}},
		{"53:53/udp", PortBinding{Published: "53", Target: "53", Protocol: "udp"}},
	}
	for _, tc := range tests {
		got, err := NormalizePorts([]json.RawMessage{rawStr(tc.in)})
		if err != nil {
			t.Errorf("NormalizePorts(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("NormalizePorts(%q) = %+v, want %+v", tc.in, got[0], tc.want)
		}
	}
}

func TestNormalizePorts_objectForm(t *testing.T) {
	published := 8080
	hostIP := "127.0.0.1"
	raw := json.RawMessage(`{"target":80,"published":8080,"protocol":"tcp","host_ip":"127.0.0.1"}`)
	got, err := NormalizePorts([]json.RawMessage{raw})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := PortBinding{
		Target:    "80",
		Published: "8080",
		Protocol:  "tcp",
		HostIP:    hostIP,
	}
	_ = published
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}

func TestNormalizePorts_invalidSpec(t *testing.T) {
	_, err := NormalizePorts([]json.RawMessage{rawStr("a:b:c:d")})
	if err == nil {
		t.Error("expected error for 4-part port spec")
	}
}

// --- NormalizeVolumes ---

func TestNormalizeVolumes_named(t *testing.T) {
	got, err := NormalizeVolumes([]json.RawMessage{rawStr("mydata:/data")}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := VolumeMount{Type: "volume", Source: "mydata", Target: "/data"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}

func TestNormalizeVolumes_bindAbsolute(t *testing.T) {
	got, err := NormalizeVolumes([]json.RawMessage{rawStr("/host/path:/container")}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := VolumeMount{Type: "bind", Source: "/host/path", Target: "/container"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}

func TestNormalizeVolumes_bindRelative(t *testing.T) {
	baseDir := "/project"
	got, err := NormalizeVolumes([]json.RawMessage{rawStr("./data:/container")}, baseDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := VolumeMount{Type: "bind", Source: filepath.Join(baseDir, "data"), Target: "/container"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}

func TestNormalizeVolumes_readOnly(t *testing.T) {
	got, err := NormalizeVolumes([]json.RawMessage{rawStr("/host:/container:ro")}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || !got[0].ReadOnly {
		t.Errorf("expected ReadOnly=true, got %+v", got[0])
	}
}

func TestNormalizeVolumes_anonymousTarget(t *testing.T) {
	got, err := NormalizeVolumes([]json.RawMessage{rawStr("/data")}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := VolumeMount{Type: "volume", Target: "/data"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}

// --- ParsePklDuration ---

func TestParsePklDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{`{"value":1,"unit":"ns"}`, time.Nanosecond},
		{`{"value":1,"unit":"us"}`, time.Microsecond},
		{`{"value":1,"unit":"ms"}`, time.Millisecond},
		{`{"value":30,"unit":"s"}`, 30 * time.Second},
		{`{"value":5,"unit":"min"}`, 5 * time.Minute},
		{`{"value":2,"unit":"h"}`, 2 * time.Hour},
		{`{"value":1,"unit":"d"}`, 24 * time.Hour},
	}
	for _, tc := range tests {
		r := json.RawMessage(tc.input)
		got, err := ParsePklDuration(&r)
		if err != nil {
			t.Errorf("ParsePklDuration(%s): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParsePklDuration(%s) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestParsePklDuration_nil(t *testing.T) {
	got, err := ParsePklDuration(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("expected 0, got %v", got)
	}
}

func TestParsePklDuration_unknownUnit(t *testing.T) {
	r := json.RawMessage(`{"value":1,"unit":"fortnight"}`)
	_, err := ParsePklDuration(&r)
	if err == nil {
		t.Error("expected error for unknown unit")
	}
}
