package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/docker/docker/client"
)

type cancelEOFBody struct {
	cancel  context.CancelFunc
	closed  bool
	partial bool
}

func (b *cancelEOFBody) Read(p []byte) (int, error) {
	if b.partial {
		b.partial = false
		return copy(p, `{"status":`), nil
	}
	b.cancel()
	return 0, io.EOF
}
func (b *cancelEOFBody) Close() error { b.closed = true; return nil }

func TestDockerPullCanceledStreamEOF(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) { testDockerPullCanceledStream(t, partial) })
	}
}

func testDockerPullCanceledStream(t *testing.T, partial bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &cancelEOFBody{cancel: cancel, partial: partial}
	calls := 0
	transport := contextTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
	})
	cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	err = (&DockerEngine{cli: cli}).PullImage(ctx, "synthetic", io.Discard)
	if !errors.Is(err, context.Canceled) || calls != 1 || !body.closed {
		t.Fatalf("err=%v calls=%d closed=%v", err, calls, body.closed)
	}
}
