package main

import (
	"bytes"
	"errors"
	"testing"
)

type doctorFailOnceWriter struct {
	writes int
	failAt int
	err    error
}

func (w *doctorFailOnceWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

func TestDoctorTextWriteFailure(t *testing.T) {
	want := errors.New("synthetic stdout failure")
	for _, tc := range []struct {
		name   string
		failAt int
	}{
		{"check name", 1}, {"detail", 2}, {"newline", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &doctorFailOnceWriter{failAt: tc.failAt, err: want}
			err := renderDoctorText(w, []doctorCheck{{Name: "pkl", OK: true, Detail: "synthetic"}, {Name: "config"}})
			if !errors.Is(err, want) || w.writes != tc.failAt {
				t.Fatalf("want original write error with no further writes; got %v after %d writes", err, w.writes)
			}
		})
	}
}

func TestDoctorTextOutput(t *testing.T) {
	var out bytes.Buffer
	checks := []doctorCheck{{Name: "pkl", OK: true, Detail: "synthetic"}}
	if err := renderDoctorText(&out, checks); err != nil || out.String() != "ok         pkl (synthetic)\n" {
		t.Fatalf("successful check: output %q, error %v", out.String(), err)
	}
	out.Reset()
	checks = append(checks, doctorCheck{Name: "config"})
	if err := renderDoctorText(&out, checks); !errors.Is(err, errDoctorFoundIssues) || out.String() != "ok         pkl (synthetic)\nfail       config\n" {
		t.Fatalf("failed check: output %q, error %v", out.String(), err)
	}
}
