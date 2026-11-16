// dill-serve exposes a fixed stack's read-only status on a private Unix socket.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/umayr/dill/core"
	"github.com/umayr/dill/internal/serve"
)

func run() error {
	engine := flag.String("engine", "", "fixed engine: docker or podman")
	endpoint := flag.String("endpoint", "", "fixed local engine endpoint (unix:///absolute/path)")
	stack := flag.String("stack", "", "fixed Dill stack name")
	socket := flag.String("socket", "", "absolute service socket path in an existing private directory")
	stagingRoot := flag.String("staging-root", "", "existing trusted private snapshot staging directory (optional)")
	catalogRoot := flag.String("staging-catalog", "", "existing trusted private catalog directory for newly received snapshots (optional)")
	flag.Parse()
	if flag.NArg() != 0 || *stack == "" || *socket == "" {
		return fmt.Errorf("engine, endpoint, stack and socket are required; positional arguments are not supported")
	}
	if err := serve.ValidateEndpoint(*engine, *endpoint); err != nil {
		return err
	}
	if *catalogRoot != "" && *stagingRoot == "" {
		return errors.New("staging catalog requires a staging root")
	}
	var stager *serve.Stager
	if *stagingRoot != "" {
		var err error
		if *catalogRoot != "" {
			stager, err = serve.NewCataloguedStager(*stagingRoot, *catalogRoot)
		} else {
			stager, err = serve.NewStager(*stagingRoot)
		}
		if err != nil {
			return err
		}
		defer stager.Close()
	}
	l, cleanup, err := serve.Listen(*socket)
	if err != nil {
		return err
	}
	defer cleanup()
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	source, err := core.OpenStatusEngine(ctx, *engine, *endpoint)
	cancel()
	if err != nil {
		return fmt.Errorf("open engine: %w", err)
	}
	defer source.Close()
	server := serve.Server(serve.API{Source: source, Stack: *stack, Engine: *engine, Stager: stager})
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-signals.Done():
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if server.Shutdown(ctx) != nil {
				_ = server.Close()
			}
		case <-done:
		}
	}()
	err = server.Serve(l)
	close(done)
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	_ = server.Close()
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dill-serve:", err)
		os.Exit(1)
	}
}
