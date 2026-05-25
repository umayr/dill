package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/umayr/dill/internal/dag"
)

// --- pull sinks ---

// ttyPullSink renders in-place progress for a single image pull when stdout
// is a TTY. All writes share mu so concurrent pulls don't interleave mid-line.
//
// When multiple images pull simultaneously the cursor-up approach is unsafe:
// each sink assumes its line is the last on screen, but another sink may have
// printed below it. In that case Write() detects the concurrent pull via
// activeCount and skips the in-place update, leaving the initial "pulling
// <service>" line unchanged.
type ttyPullSink struct {
	service     string
	mu          *sync.Mutex
	activeCount *int32
}

func (s *ttyPullSink) Begin() {
	atomic.AddInt32(s.activeCount, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  %-14s %s\n", "pulling", s.service)
}

func (s *ttyPullSink) Write(p []byte) (int, error) {
	if atomic.LoadInt32(s.activeCount) > 1 {
		return len(p), nil
	}
	line := strings.TrimRight(string(p), "\n\r ")
	if line == "" {
		return len(p), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\033[A\r\033[K  %-14s %s  %s\n", "pulling", s.service, line)
	return len(p), nil
}

func (s *ttyPullSink) Done() {
	atomic.AddInt32(s.activeCount, -1)
}

// plainPullSink is used when stdout is not a TTY; it prints "pulling" once
// and discards streaming progress.
type plainPullSink struct {
	service string
}

func (s *plainPullSink) Begin()                    { printProgress(s.service, "pulling") }
func (s *plainPullSink) Write(p []byte) (int, error) { return len(p), nil }
func (s *plainPullSink) Done()                     {}

// newMakePullSink returns a MakePullSink factory. The shared mutex prevents
// concurrent pulls from interleaving characters; the shared activeCount lets
// each TTY sink detect concurrent pulls and suppress unsafe cursor-up updates.
func newMakePullSink(isTTY bool, mu *sync.Mutex) dag.MakePullSink {
	var activeCount int32
	return func(service string) dag.PullSink {
		if isTTY {
			return &ttyPullSink{service: service, mu: mu, activeCount: &activeCount}
		}
		return &plainPullSink{service: service}
	}
}

// --- log prefix writer ---

var logColors = []string{
	"\033[36m", // cyan
	"\033[32m", // green
	"\033[33m", // yellow
	"\033[35m", // magenta
	"\033[34m", // blue
	"\033[31m", // red
}

const logColorReset = "\033[0m"

func logPrefix(name string, idx int, isTTY bool) string {
	if !isTTY {
		return name + " |"
	}
	return logColors[idx%len(logColors)] + name + logColorReset + " |"
}

// prefixWriter buffers output line by line, prepending prefix to each line.
// The shared mu prevents lines from different goroutines interleaving on stdout.
type prefixWriter struct {
	prefix string
	out    *os.File
	mu     *sync.Mutex
	buf    bytes.Buffer
}

func (pw *prefixWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			pw.buf.Write(p)
			break
		}
		pw.buf.Write(p[:idx+1])
		p = p[idx+1:]
		pw.mu.Lock()
		fmt.Fprintf(pw.out, "%s %s", pw.prefix, pw.buf.String())
		pw.mu.Unlock()
		pw.buf.Reset()
	}
	return n, nil
}

// flush writes any buffered content that did not end with a newline.
// Call after the log stream closes so the last line is not silently dropped.
func (pw *prefixWriter) flush() {
	if pw.buf.Len() == 0 {
		return
	}
	pw.mu.Lock()
	fmt.Fprintf(pw.out, "%s %s\n", pw.prefix, pw.buf.String())
	pw.mu.Unlock()
	pw.buf.Reset()
}
