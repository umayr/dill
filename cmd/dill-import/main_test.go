package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestImportCommand(t *testing.T) {
	var out, stderr bytes.Buffer
	code := run([]string{"compose", "--schema", "./dill.pkl", "-"}, strings.NewReader("services: {web: {image: nginx}}"), &out, &stderr)
	if code != 0 || !strings.Contains(out.String(), `amends "./dill.pkl"`) || !strings.Contains(out.String(), `image = "nginx"`) || stderr.Len() != 0 {
		t.Fatalf("code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}
func TestImportFailuresProduceNoPartialOutput(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		input string
		code  int
	}{
		{nil, "", 2}, {[]string{"compose"}, "", 2}, {[]string{"compose", "-"}, "services: {web: {image: nginx, build: .}}", 2}, {[]string{"compose", "missing"}, "", 1},
	} {
		var out, stderr bytes.Buffer
		code := run(tc.args, strings.NewReader(tc.input), &out, &stderr)
		if code != tc.code || out.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("args=%v code=%d out=%q stderr=%q", tc.args, code, out.String(), stderr.String())
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return 0, nil }
func TestImportRejectsShortWrites(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"compose", "-"}, strings.NewReader("services: {web: {image: nginx}}"), shortWriter{}, &stderr); code != 1 || !strings.Contains(stderr.String(), io.ErrShortWrite.Error()) {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}
