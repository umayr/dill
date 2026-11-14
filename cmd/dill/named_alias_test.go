package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func replaceNamedFlag(args []string, flag, value string) {
	for i := range args {
		if args[i] == flag && i+1 < len(args) {
			args[i+1] = value
			return
		}
	}
}
func TestNamedRuntimeStateChangeDoesNotAlterSelection(t *testing.T) {
	f := newCoordinatedDocker(t, true)
	useSyntheticConfig(t, syntheticConfig())
	args := namedPreviewArgs(f, "stop")
	var out bytes.Buffer
	if err := runNamedPreview(context.Background(), args, &out); err != nil {
		t.Fatal(err)
	}
	var review preflight.Result
	if err := json.Unmarshal(out.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.running = true
	f.mu.Unlock()
	apply := append([]string{}, args...)
	apply[2] = "dill.operation/1"
	apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *review.SelectionDigest)
	result, code, _ := captureOperation(t, "", apply)
	if code != 0 || result.Outcome != "succeeded" {
		t.Fatalf("state-only change rejected: %+v", result)
	}
}

func TestNamedStateAliasAndTargetSelectorBinding(t *testing.T) {
	for _, kind := range []string{"state-alias", "different-state", "copied-state", "socket-alias"} {
		t.Run(kind, func(t *testing.T) {
			f := newCoordinatedDocker(t, true)
			useSyntheticConfig(t, syntheticConfig())
			args := namedPreviewArgs(f, "stop")
			var out bytes.Buffer
			if err := runNamedPreview(context.Background(), args, &out); err != nil {
				t.Fatal(err)
			}
			var review preflight.Result
			if err := json.Unmarshal(out.Bytes(), &review); err != nil {
				t.Fatal(err)
			}
			apply := append([]string{}, args...)
			apply[2] = "dill.operation/1"
			apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *review.SelectionDigest)
			switch kind {
			case "state-alias":
				alias := filepath.Join(filepath.Dir(f.stateDir), "state-alias")
				if err := os.Symlink(f.stateDir, alias); err != nil {
					t.Fatal(err)
				}
				replaceNamedFlag(apply, "--coordination-state-dir", alias)
			case "different-state":
				dir := filepath.Join(filepath.Dir(f.stateDir), "other-state")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				replaceNamedFlag(apply, "--coordination-state-dir", dir)
			case "copied-state":
				dir := filepath.Join(filepath.Dir(f.stateDir), "copied-state")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(f.stateDir)
				if err != nil {
					t.Fatal(err)
				}
				var seen []struct {
					info os.FileInfo
					path string
				}
				for _, entry := range entries {
					if !entry.Type().IsRegular() {
						t.Fatal("unexpected state fixture entry")
					}
					info, err := entry.Info()
					if err != nil {
						t.Fatal(err)
					}
					dest := filepath.Join(dir, entry.Name())
					linked := false
					for _, prior := range seen {
						if os.SameFile(info, prior.info) {
							if err = os.Link(prior.path, dest); err != nil {
								t.Fatal(err)
							}
							linked = true
							break
						}
					}
					if !linked {
						data, err := os.ReadFile(filepath.Join(f.stateDir, entry.Name()))
						if err != nil {
							t.Fatal(err)
						}
						if err = os.WriteFile(dest, data, 0600); err != nil {
							t.Fatal(err)
						}
						seen = append(seen, struct {
							info os.FileInfo
							path string
						}{info, dest})
					}
				}
				replaceNamedFlag(apply, "--coordination-state-dir", dir)
			case "socket-alias":
				alias := filepath.Join(filepath.Dir(f.socket), "alias.sock")
				if err := os.Symlink(f.socket, alias); err != nil {
					t.Fatal(err)
				}
				replaceNamedFlag(apply, "--engine-socket", alias)
			}
			result, code, _ := captureOperation(t, "", apply)
			if kind == "state-alias" {
				if code != 0 || result.Outcome != "succeeded" {
					t.Fatalf("same store rejected: %+v", result)
				}
			} else if code != 1 || result.Outcome != "failed" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != "PREFLIGHT_MISMATCH" {
				t.Fatalf("changed scope accepted: %+v", result)
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if kind != "state-alias" && writes != 0 {
				t.Fatal("changed scope wrote engine")
			}
		})
	}
}
