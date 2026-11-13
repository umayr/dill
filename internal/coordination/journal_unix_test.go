//go:build linux || darwin

package coordination

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func testTarget() *Target {
	sum := sha256.Sum256([]byte(testDaemon))
	return &Target{identity: hex.EncodeToString(sum[:])}
}
func openState(t *testing.T, dir string) *State {
	t.Helper()
	ctx, cancel := bounded()
	defer cancel()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func begin(t *testing.T, s *State, stack, source string, resources []ResourceInput) *Session {
	t.Helper()
	ctx, cancel := bounded()
	defer cancel()
	session, err := s.Begin(ctx, testTarget(), stack, source, resources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}
func inspection(t *testing.T, s *State, stack string) *Record {
	t.Helper()
	ctx, cancel := bounded()
	defer cancel()
	r, err := s.Inspect(ctx, testTarget().Identity(), stack)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestJournalCrashRequiresExactAcknowledgement(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s := openState(t, dir)
	first := begin(t, s, "ExactStack", "brine", []ResourceInput{{"container", "private-name", "create"}})
	id := first.OperationID()
	first.Close()
	ctx, cancel := bounded()
	defer cancel()
	_, err := s.Begin(ctx, testTarget(), "ExactStack", "cli", nil)
	var unresolved *InspectionRequired
	if !errors.As(err, &unresolved) || unresolved.Record.OperationID != id {
		t.Fatalf("got %v", err)
	}
	if r := inspection(t, s, "ExactStack"); r.State != "in_progress" || r.Outcome != "unknown" || r.Effects != "unknown" {
		t.Fatalf("bad crash outcome: %+v", r)
	}
	if err = s.Acknowledge(ctx, testTarget().Identity(), "ExactStack", "wrong-id"); err == nil {
		t.Fatal("acknowledged wrong operation")
	}
	if err = s.Acknowledge(ctx, testTarget().Identity(), "ExactStack", id); err != nil {
		t.Fatal(err)
	}
	next := begin(t, s, "ExactStack", "cli", nil)
	if next.OperationID() == id {
		t.Fatal("replayed operation ID")
	}
	if err = next.Finish("succeeded", "complete"); err != nil {
		t.Fatal(err)
	}
	next.Close()
	files, err := filepath.Glob(filepath.Join(dir, "*.result"))
	if err != nil || len(files) != 2 {
		t.Fatalf("lost retained unknown/acknowledgement: %v %v", files, err)
	}
	for _, path := range append(files, filepath.Join(dir, nameForMust(t, s, "ExactStack"))) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"ExactStack", "private-name", testDaemon, dir} {
			if strings.Contains(string(data), secret) {
				t.Fatalf("raw identity leaked: %s", secret)
			}
		}
	}
}
func nameForMust(t *testing.T, s *State, stack string) string {
	t.Helper()
	key, err := s.scope(testTarget().Identity(), stack)
	if err != nil {
		t.Fatal(err)
	}
	return nameFor(key)
}
func TestStackAndResourceConcurrency(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s := openState(t, dir)
	a := begin(t, s, "same", "cli", []ResourceInput{{"container", "global-name", "reconcile"}})
	other := begin(t, s, "different", "brine", []ResourceInput{{"container", "different-name", "reconcile"}})
	defer other.Close()
	for _, request := range []struct {
		stack     string
		resources []ResourceInput
	}{{"same", nil}, {"different2", []ResourceInput{{"container", "global-name", "remove"}}}} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		_, err := s.Begin(ctx, testTarget(), request.stack, "brine", request.resources)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("did not contend: %v", err)
		}
	}
	// Exact labels are case sensitive; config filenames never enter scope.
	c := begin(t, s, "Same", "brine", nil)
	c.Finish("succeeded", "none")
	c.Close()
	a.Finish("succeeded", "complete")
	a.Close()
	next := begin(t, s, "same", "brine", nil)
	next.Finish("succeeded", "none")
	next.Close()
}
func TestResourceLockOrderAndCancellation(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s := openState(t, dir)
	a := begin(t, s, "a", "cli", []ResourceInput{{"container", "one", "start"}, {"volume", "two", "create"}})
	ctx, cancel := bounded()
	done := make(chan error, 1)
	go func() {
		session, err := s.Begin(ctx, testTarget(), "b", "brine", []ResourceInput{{"volume", "two", "remove"}, {"container", "one", "stop"}})
		if session != nil {
			session.Close()
		}
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	a.Finish("succeeded", "none")
	a.Close()
	b := begin(t, s, "b", "cli", []ResourceInput{{"volume", "two", "remove"}, {"container", "one", "stop"}})
	b.Finish("succeeded", "complete")
	b.Close()
}
func TestTerminalWriteFaultRemainsUnknown(t *testing.T) {
	for _, stage := range []string{"created", "written", "synced", "renamed", "committed", "publishing"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			s := openState(t, dir)
			session := begin(t, s, "stack", "cli", nil)
			s.fault = func(at string) error {
				if at == stage {
					return errors.New("injected private diagnostic")
				}
				return nil
			}
			err := session.Finish("succeeded", "complete")
			if err == nil || strings.Contains(err.Error(), "private diagnostic") {
				t.Fatal("write fault leaked or succeeded")
			}
			if session.Finish("succeeded", "complete") == nil {
				t.Fatal("retried poisoned terminal commit")
			}
			session.Close()
			s.fault = nil
			reopened := openState(t, dir)
			r := inspection(t, reopened, "stack")
			if r.State != "in_progress" || !needsInspection(*r) {
				t.Fatalf("failure became success: %+v", r)
			}
			ctx, cancel := bounded()
			defer cancel()
			if _, err := reopened.Begin(ctx, testTarget(), "stack", "brine", nil); err == nil {
				t.Fatal("persistent write failure became permission to mutate on reopen")
			}
		})
	}
}
func TestRejectCorruptStaleAndUnsafeState(t *testing.T) {
	for _, data := range []string{"", `{"version":"future"}`, "truncated", strings.Repeat("x", maxRecord+1)} {
		dir := t.TempDir()
		os.Chmod(dir, 0700)
		s := openState(t, dir)
		if err := os.WriteFile(filepath.Join(dir, nameForMust(t, s, "s")), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := bounded()
		_, err := s.Begin(ctx, testTarget(), "s", "cli", nil)
		cancel()
		var unresolved *InspectionRequired
		if !errors.As(err, &unresolved) {
			t.Fatalf("accepted corrupt state: %v", err)
		}
	}
	for _, kind := range []string{"symlink", "hardlink", "public", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			s := openState(t, dir)
			path := filepath.Join(dir, nameForMust(t, s, "s"))
			switch kind {
			case "symlink":
				os.Symlink("missing", path)
			case "hardlink":
				os.WriteFile(filepath.Join(dir, "other"), nil, 0600)
				os.Link(filepath.Join(dir, "other"), path)
			case "public":
				os.WriteFile(path, nil, 0644)
			case "fifo":
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := bounded()
			_, err := s.Begin(ctx, testTarget(), "s", "cli", nil)
			cancel()
			if err == nil {
				t.Fatal("accepted unsafe state")
			}
		})
	}
}
func TestStateQualificationAndAliases(t *testing.T) {
	ctx, cancel := bounded()
	defer cancel()
	for _, mode := range []os.FileMode{0755, 0770, 0777} {
		dir := t.TempDir()
		os.Chmod(dir, mode)
		if s, err := Open(ctx, dir); err == nil {
			s.Close()
			t.Fatal("accepted nonprivate directory")
		}
	}
	if _, err := Open(ctx, "relative"); err == nil {
		t.Fatal("accepted relative state")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s := openState(t, dir)
	alias := filepath.Join(t.TempDir(), "alias")
	os.Symlink(dir, alias)
	same := openState(t, alias)
	if string(s.key) != string(same.key) {
		t.Fatal("state alias changed HMAC key")
	}
	renamed := dir + "-old"
	if err := os.Rename(dir, renamed); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(renamed)
	os.Mkdir(dir, 0700)
	session := begin(t, s, "pinned", "cli", nil)
	session.Finish("succeeded", "complete")
	session.Close()
	if _, err := os.Stat(filepath.Join(renamed, nameForMust(t, s, "pinned"))); err != nil {
		t.Fatal("state descriptor not pinned")
	}
	if _, err := os.Stat(filepath.Join(dir, nameForMust(t, s, "pinned"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("writes retargeted")
	}
}
func TestValidateMetadataAndAxes(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s := openState(t, dir)
	ctx, cancel := bounded()
	defer cancel()
	for _, r := range []ResourceInput{{"image", "global", "remove"}, {"container", "", "create"}, {"volume", "name", "raw-secret-action"}} {
		if _, err := s.Begin(ctx, testTarget(), "s", "cli", []ResourceInput{r}); err == nil {
			t.Fatal("accepted unsupported resources")
		}
	}
	if _, err := s.Begin(ctx, testTarget(), "s", "secret-source", nil); err == nil {
		t.Fatal("accepted arbitrary source")
	}
	session := begin(t, s, "s", "cli", nil)
	if session.Finish("succeeded", "partial") == nil {
		t.Fatal("accepted contradictory success")
	}
	if err := session.Finish("cancelled", "partial"); err != nil {
		t.Fatal(err)
	}
	session.Close()
	if _, err := s.Begin(ctx, testTarget(), "s", "brine", nil); err == nil {
		t.Fatal("automatically replayed partial cancellation")
	}
}

func TestJournalProcessCrash(t *testing.T) {
	if mode := os.Getenv("DILL_COORD_CHILD"); mode != "" {
		ctx, cancel := bounded()
		defer cancel()
		s, err := Open(ctx, os.Getenv("DILL_COORD_DIR"))
		if err != nil {
			os.Exit(21)
		}
		if mode != "hold" {
			s.fault = func(stage string) error {
				if stage == mode {
					os.Exit(91)
				}
				return nil
			}
		}
		session, err := s.Begin(ctx, testTarget(), "stack", "brine", []ResourceInput{{"container", "cross-process-shared", "reconcile"}})
		if err != nil {
			os.Exit(22)
		}
		_ = session
		os.Stdout.WriteString("ready\n")
		select {}
	}
	for _, mode := range []string{"hold", "created", "written", "synced", "renamed", "committed"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			s := openState(t, dir)
			cmd := exec.Command(os.Args[0], "-test.run=^TestJournalProcessCrash$")
			cmd.Env = append(os.Environ(), "DILL_COORD_CHILD="+mode, "DILL_COORD_DIR="+dir)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if cmd.ProcessState == nil {
					cmd.Process.Kill()
					cmd.Wait()
				}
			})
			if mode == "hold" {
				done := make(chan error, 1)
				go func() {
					b := make([]byte, 6)
					_, err := io.ReadFull(stdout, b)
					if err == nil && string(b) != "ready\n" {
						err = errors.New("bad readiness")
					}
					done <- err
				}()
				select {
				case err = <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("child not ready")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
				_, err = s.Begin(ctx, testTarget(), "stack", "cli", nil)
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("stole active Brine lock: %v", err)
				}
				cmd.Process.Kill()
			}
			if err = cmd.Wait(); err == nil {
				t.Fatal("child unexpectedly succeeded")
			}
			r := inspection(t, s, "stack")
			committed := mode == "hold" || mode == "renamed" || mode == "committed"
			if committed && (r == nil || !needsInspection(*r)) {
				t.Fatalf("lost crash record: %+v", r)
			}
			if !committed && r != nil {
				t.Fatalf("treated incomplete temp as committed: %+v", r)
			}
			if mode == "hold" {
				ctx, cancel := bounded()
				_, err = s.Begin(ctx, testTarget(), "different-stack", "cli", []ResourceInput{{"container", "cross-process-shared", "remove"}})
				cancel()
				var unknown *InspectionRequired
				if !errors.As(err, &unknown) {
					t.Fatalf("cross-stack process crash bypassed: %v", err)
				}
			}
			if committed {
				ctx, cancel := bounded()
				_, err = s.Begin(ctx, testTarget(), "stack", "cli", nil)
				cancel()
				var unknown *InspectionRequired
				if !errors.As(err, &unknown) {
					t.Fatalf("replayed crashed Brine operation: %v", err)
				}
			} else {
				session := begin(t, s, "stack", "cli", nil)
				session.Finish("succeeded", "none")
				session.Close()
			}
		})
	}
}
