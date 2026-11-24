package imagelock

import (
	"context"
	"strings"
	"testing"
)

func TestBuildReuseRefreshAndJobs(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	calls := []string{}
	resolver := func(_ context.Context, image string) (string, error) {
		calls = append(calls, image)
		return digest, nil
	}
	services := map[string]string{"web": "alpine:latest"}
	jobs := map[string]string{"task": "busybox:1"}
	l, err := Build(context.Background(), services, jobs, nil, nil, nil, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if l.Services["web"].Digest != "docker.io/library/alpine@"+digest || len(calls) != 2 {
		t.Fatalf("lock=%+v calls=%v", l, calls)
	}
	calls = nil
	_, err = Build(context.Background(), services, jobs, l, nil, map[string]bool{"task": true}, resolver)
	if err != nil || len(calls) != 1 || calls[0] != "busybox:1" {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
	if _, _, err = l.Resolve(map[string]string{"web": "alpine:other"}, jobs); err == nil {
		t.Fatal("accepted changed image")
	}
}

func TestBuildPinned(t *testing.T) {
	digest := "sha256:" + strings.Repeat("b", 64)
	resolver := func(context.Context, string) (string, error) { t.Fatal("unexpected registry call"); return "", nil }
	l, err := Build(context.Background(), map[string]string{"x": "alpine@" + digest}, nil, nil, nil, nil, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if l.Services["x"].Digest != "docker.io/library/alpine@"+digest {
		t.Fatal(l.Services["x"])
	}
}
