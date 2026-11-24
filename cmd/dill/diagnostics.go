package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/umayr/dill/internal/agentcmd"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

type diagLog struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *diagLog) Write(p []byte) (int, error) {
	room := (256 << 10) - b.buf.Len()
	if len(p) > room {
		b.buf.Write(p[:room])
		b.truncated = true
		return room, io.ErrShortWrite
	}
	return b.buf.Write(p)
}
func runDiagnostics(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("diagnostics", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	out := fs.String("output", "", "new private .tar.gz path")
	stateDir := fs.String("state-dir", "", "offline journal directory for an original request")
	journal := fs.String("journal-id", "", "original journal ID")
	request := fs.String("request-id", "", "original request ID")
	selected := map[string]bool{}
	fs.Func("include-logs", "include bounded raw application logs for this service (repeatable)", func(v string) error { selected[v] = true; return nil })
	since := fs.Duration("since", 15*time.Minute, "maximum log lookback")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *out == "" || len(selected) > 8 || *since <= 0 || *since > 24*time.Hour {
		return usageError{"expected --output PATH and at most 8 log selections, lookback <=24h"}
	}
	journalRequested := *stateDir != "" || *journal != "" || *request != ""
	if journalRequested && (*stateDir == "" || *journal == "" || *request == "") {
		return usageError{"supply --state-dir, --journal-id and --request-id together"}
	}
	if opts.secretMode() {
		return config.ErrSecretUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return errors.New("diagnostics configuration unavailable")
	}
	if config.HasSecretDeclarations(cfg) {
		return config.ErrSecretUnavailable
	}
	if len(cfg.Services) > 512 {
		return errors.New("too many services")
	}
	for n := range selected {
		if cfg.Services[n] == nil {
			return usageError{"unknown log selection"}
		}
	}
	stack := resolveStackNameWithOptions(cfg, opts)
	e, err := openEngine(ctx, cfg, opts)
	available := err == nil && e != nil
	if available {
		defer e.Close()
	}
	f, err := os.CreateTemp(filepath.Dir(*out), ".dill-diag-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	add := func(name string, b []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(b))}); err != nil {
			return err
		}
		_, err := tw.Write(b)
		return err
	}
	type row struct {
		Name      string `json:"service"`
		State     string `json:"state"`
		Log       string `json:"log,omitempty"`
		Partial   bool   `json:"partial,omitempty"`
		Readiness string `json:"readiness,omitempty"`
	}
	report := struct {
		Format        int            `json:"format"`
		Dill          string         `json:"dill"`
		Engine        string         `json:"engine"`
		EngineVersion string         `json:"engineVersion"`
		Plan          map[string]int `json:"plannedChanges,omitempty"`
		Operation     string         `json:"operation"`
		Effects       string         `json:"effects,omitempty"`
		Services      []row          `json:"services"`
	}{Format: 1, Dill: version, Engine: "unavailable", EngineVersion: "unknown", Operation: "not collected"}
	if journalRequested {
		report.Operation = "unknown"
		var buf bytes.Buffer
		if agentcmd.Main([]string{"journal-recovery", "--state-dir", *stateDir, "--journal-id", *journal, "--request-id", *request}, &buf, io.Discard, true) == 0 {
			var ev struct{ State, Effects string }
			if json.Unmarshal(buf.Bytes(), &ev) == nil {
				report.Operation = ev.State
				report.Effects = ev.Effects
			}
		}
	}
	if available {
		report.Engine = engineBinary(e)
		if v, ok := e.(interface{ DiagnosticVersion(context.Context) string }); ok {
			ver := v.DiagnosticVersion(ctx)
			if regexp.MustCompile(`^[0-9][0-9A-Za-z.+_-]{0,63}$`).MatchString(ver) {
				report.EngineVersion = ver
			}
		}
		if p, err := plan.Compute(ctx, cfg, e, stack); err == nil {
			report.Plan = map[string]int{}
			for _, ch := range p.Changes {
				key, _ := ch.Kind.MarshalJSON()
				report.Plan[string(key[1:len(key)-1])]++
			}
		}
	}
	var names []string
	for n := range cfg.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		r := row{Name: n, State: "unknown"}
		name := serviceContainerName(stack, n, cfg.Services[n])
		if !available {
			report.Services = append(report.Services, r)
			continue
		}
		live, err := e.InspectConfig(ctx, name)
		if errors.Is(err, orchestrator.ErrNotFound) {
			r.State = "absent"
		} else if err == nil && live != nil && live.ID != "" && orchestrator.CheckOwnership(name, stack, n, live) == nil {
			if _, protected := live.SystemLabels[orchestrator.SecretEnabledLabel]; protected {
				r.State = "protected"
			} else {
				if st, err := e.ServiceStatus(ctx, live.ID); err == nil && st != nil {
					switch st.State {
					case "running", "exited", "paused", "created", "restarting", "stopped", "dead":
						r.State = st.State
					}
				}
				if r.State == "running" && cfg.Services[n].HealthCheck != nil {
					r.Readiness = "unknown"
					if ready, err := e.IsReady(ctx, live.ID, true); err == nil {
						if ready {
							r.Readiness = "ready"
						} else {
							r.Readiness = "not ready"
						}
					}
				}
				if selected[n] {
					var b diagLog
					err := e.Logs(ctx, live.ID, orchestrator.LogOptions{Tail: 200, Since: since.String(), Timestamps: true}, &b)
					r.Partial = b.truncated || err != nil
					r.Log = fmt.Sprintf("logs/%03d.log", i)
					if err := add(r.Log, b.buf.Bytes()); err != nil {
						return err
					}
				}
			}
		}
		report.Services = append(report.Services, r)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := add("report.json", data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Link(f.Name(), *out)
}
