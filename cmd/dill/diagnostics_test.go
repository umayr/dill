package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"github.com/umayr/dill/internal/orchestrator"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type logFixture struct {
	*fakeEngine
	calls int
}

func (f *logFixture) Logs(_ context.Context, id string, o orchestrator.LogOptions, w io.Writer) error {
	f.calls++
	if id != "app_web" || o.Follow || o.Tail != 200 {
		return io.ErrUnexpectedEOF
	}
	_, e := io.WriteString(w, strings.Repeat("x", 300<<10))
	return e
}
func TestDiagnosticsLocalBundle(t *testing.T) {
	f := &logFixture{fakeEngine: newFakeEngine()}
	f.statuses["app_web"] = &orchestrator.ContainerStatus{State: "running", Status: "sensitive-status-marker"}
	cfg := testConfig()
	cfg.Services["web"].Environment = map[string]string{"SECRET": "sensitive-env-marker"}
	withTestRuntime(t, f, cfg, func() {
		for _, logs := range []bool{false, true} {
			out := filepath.Join(t.TempDir(), "dump.tar.gz")
			args := []string{"--output", out}
			if logs {
				args = append(args, "--include-logs", "web")
			}
			if err := runDiagnostics(context.Background(), commandOptions{}, args); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(out)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			s, _ := file.Stat()
			if s.Mode().Perm() != 0600 {
				t.Fatal("unsafe mode")
			}
			gz, err := gzip.NewReader(file)
			if err != nil {
				t.Fatal(err)
			}
			tr := tar.NewReader(gz)
			files := map[string][]byte{}
			for {
				h, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				files[h.Name] = b
			}
			report := files["report.json"]
			if len(report) == 0 || bytes.Contains(report, []byte("sensitive-")) {
				t.Fatal("unexpected diagnostics metadata")
			}
			if logs {
				if len(files["logs/000.log"]) != 256<<10 || !bytes.Contains(report, []byte(`"partial": true`)) {
					t.Fatal("missing bounded log")
				}
			} else if len(files) != 1 || f.calls != 0 {
				t.Fatal("default read logs")
			}
			if err := runDiagnostics(context.Background(), commandOptions{}, args); err == nil {
				t.Fatal("overwrote existing bundle")
			}
		}
	})
}
