package serve

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStagingUnixSocketNoEngineCalls(t *testing.T) {
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "ds-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "stages")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	st, err := NewStager(root)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "api.sock")
	l, cleanup, err := Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer l.Close()
	fake := &fake{}
	srv := Server(API{Source: fake, Stager: st})
	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()
	defer func() { srv.Close(); <-done }()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	data := []byte("safe")
	sum := sha256.Sum256(data)
	body := fmt.Sprintf(`{"schema":"dill.snapshot.v1","entrypoint":"a","files":[{"path":"a","bytes":"%s","length":4,"sha256":"%s"}]}`, base64.StdEncoding.EncodeToString(data), hex.EncodeToString(sum[:]))
	resp, err := client.Post("http://unix/v1/snapshots", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if fake.stack != "" || fake.inspected != "" {
		t.Fatal("engine called during staging")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	file := filepath.Join(root, entries[0].Name(), "a")
	got, err := os.ReadFile(file)
	if err != nil || string(got) != "safe" {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions %v %v", info, err)
	}
}
