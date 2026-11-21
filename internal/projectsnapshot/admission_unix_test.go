//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func admissionRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "stages")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func receiveForAdmission(ctx context.Context, root string, s Snapshot, budget admissionBudget, publish func(int, string, int, string) error) (string, error) {
	return receiveWithBudget(ctx, root, -1, s, MaximumLimits(), publish, cleanup, unix.Fstatat, identity, unix.Fsync, budget)
}

func assertAdmissionNames(t *testing.T, root string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("root entries: got %v, want %v", got, want)
	}
}

func TestAdmissionBudgetExhaustion(t *testing.T) {
	for _, tc := range []struct {
		name, content, next string
		budget              admissionBudget
	}{
		{"bytes", "four", "!", admissionBudget{bytes: 8, stages: 8}},
		{"count with empty files", "", "", admissionBudget{bytes: 8, stages: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := admissionRoot(t)
			ctx := context.Background()
			s := Snapshot{"nested/main.pkl", []Entry{entry("nested/main.pkl", tc.content)}}
			var stages []string
			for range 2 {
				p, err := receiveForAdmission(ctx, root, s, tc.budget, renameNoReplace)
				if err != nil {
					t.Fatalf("within budget: %v", err)
				}
				stages = append(stages, filepath.Base(p))
			}
			p, err := receiveForAdmission(ctx, root, Snapshot{"x", []Entry{entry("x", tc.next)}}, tc.budget, renameNoReplace)
			if p != "" || !errors.Is(err, ErrStagingCapacity) {
				t.Fatalf("over budget: %q, %v", p, err)
			}
			assertAdmissionNames(t, root, stages...)
			for _, stage := range stages {
				b, err := os.ReadFile(filepath.Join(root, stage, s.Entrypoint))
				if err != nil || string(b) != tc.content {
					t.Fatalf("existing stage changed: %q, %v", b, err)
				}
			}
		})
	}
}

func TestAdmissionChargesRetainedStages(t *testing.T) {
	for _, published := range []bool{false, true} {
		prefix := ".in-progress-"
		if published {
			prefix = "snapshot-"
		}
		t.Run(prefix, func(t *testing.T) {
			root := admissionRoot(t)
			ctx := context.Background()
			s := Snapshot{"x", []Entry{entry("x", "data")}}
			stage := ""
			unknown := func(fromfd int, from string, tofd int, to string) error {
				stage = from
				if published {
					if err := renameNoReplace(fromfd, from, tofd, to); err != nil {
						return err
					}
					stage = to
				}
				return unix.EIO
			}
			statAt := func(fd int, name string, st *unix.Stat_t, flags int) error {
				if stage != "" && !published {
					return unix.EIO // cannot establish safe cleanup after the rename error
				}
				return unix.Fstatat(fd, name, st, flags)
			}
			p, err := receive(ctx, root, s, MaximumLimits(), unknown, cleanup, statAt, identity)
			if p != "" || !errors.Is(err, ErrPublicationUnknown) || !strings.HasPrefix(stage, prefix) {
				t.Fatalf("unknown publication: %q, %v, retained %q", p, err, stage)
			}
			for _, budget := range []admissionBudget{{bytes: 7, stages: 2}, {bytes: 8, stages: 1}} {
				p, err := receiveForAdmission(ctx, root, s, budget, renameNoReplace)
				if p != "" || !errors.Is(err, ErrStagingCapacity) {
					t.Fatalf("retained stage not counted with %+v: %q, %v", budget, p, err)
				}
			}
			assertAdmissionNames(t, root, stage)
			if b, err := os.ReadFile(filepath.Join(root, stage, "x")); err != nil || string(b) != "data" {
				t.Fatalf("retained data changed: %q, %v", b, err)
			}
		})
	}
}

func TestAdmissionFailsClosedOnAmbiguousInventory(t *testing.T) {
	for _, kind := range []string{"unrecognized root entry", "stage is a file", "nested symlink", "nested hardlink", "excessive depth"} {
		t.Run(kind, func(t *testing.T) {
			root := admissionRoot(t)
			stage := "snapshot-" + strings.Repeat("a", 32)
			path := filepath.Join(root, stage)
			sentinel := filepath.Join(t.TempDir(), "sentinel")
			if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "unrecognized root entry":
				stage = "unrecognized"
				err = os.Mkdir(filepath.Join(root, stage), 0700)
			case "stage is a file":
				err = os.WriteFile(path, nil, 0600)
			case "excessive depth":
				err = os.MkdirAll(filepath.Join(path, strings.Repeat("d/", MaximumLimits().Depth)), 0700)
			default:
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				link := os.Symlink
				if kind == "nested hardlink" {
					link = os.Link
				}
				err = link(sentinel, filepath.Join(path, "unexpected"))
			}
			if err != nil {
				t.Fatal(err)
			}
			p, err := Receive(context.Background(), root, Snapshot{"x", []Entry{entry("x", "x")}}, MaximumLimits())
			if p != "" || !errors.Is(err, ErrStagingInventory) {
				t.Fatalf("ambiguous root: %q, %v", p, err)
			}
			assertAdmissionNames(t, root, stage)
			if b, err := os.ReadFile(sentinel); err != nil || string(b) != "preserve" {
				t.Fatalf("sentinel changed: %q, %v", b, err)
			}
		})
	}
}

func TestAdmissionSerializesIndependentReceivers(t *testing.T) {
	root := admissionRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	s := Snapshot{"x", []Entry{entry("x", "data")}}
	budget := admissionBudget{bytes: 4, stages: 2}
	publishing := make(chan string, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	var firstPath string
	var firstErr error
	go func() {
		defer close(done)
		firstPath, firstErr = receiveForAdmission(ctx, root, s, budget, func(fromfd int, from string, tofd int, to string) error {
			publishing <- from
			select {
			case <-release:
				return renameNoReplace(fromfd, from, tofd, to)
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	defer func() { cancel(); <-done }()
	var pending string
	select {
	case pending = <-publishing:
	case <-done:
		t.Fatalf("first receiver stopped before publication: %v", firstErr)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	p, err := receiveForAdmission(ctx, root, s, budget, renameNoReplace)
	if p != "" || !errors.Is(err, ErrStagingBusy) {
		t.Fatalf("competing receiver: %q, %v", p, err)
	}
	assertAdmissionNames(t, root, pending)
	close(release)
	select {
	case <-done:
		if firstErr != nil {
			t.Fatal(firstErr)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	p, err = receiveForAdmission(ctx, root, s, budget, renameNoReplace)
	if p != "" || !errors.Is(err, ErrStagingCapacity) {
		t.Fatalf("subsequent receiver did not account for winner: %q, %v", p, err)
	}
	assertAdmissionNames(t, root, filepath.Base(firstPath))
}
