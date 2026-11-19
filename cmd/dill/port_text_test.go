package main

import (
	"bytes"
	"errors"
	"testing"
)

type portTextWriterFunc func([]byte) (int, error)

func (f portTextWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestPortTextReturnsFirstWriteError(t *testing.T) {
	ports := []string{"0.0.0.0:8080->80/tcp", "0.0.0.0:8443->443/tcp", "0.0.0.0:9090->90/tcp"}
	var out bytes.Buffer
	if err := renderPortText(&out, ports); err != nil || out.String() != "0.0.0.0:8080->80/tcp\n0.0.0.0:8443->443/tcp\n0.0.0.0:9090->90/tcp\n" {
		t.Fatalf("unexpected successful output %q, err=%v", out.String(), err)
	}

	out.Reset()
	want := errors.New("synthetic port output failure")
	writes := 0
	w := portTextWriterFunc(func(p []byte) (int, error) {
		writes++
		if writes == 2 {
			return 0, want
		}
		return out.Write(p)
	})
	if err := renderPortText(w, ports); !errors.Is(err, want) || writes != 2 || out.String() != ports[0]+"\n" {
		t.Fatalf("want first write error and no further writes; got err=%v writes=%d output=%q", err, writes, out.String())
	}
}
