package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func TestNamedAbsentCreateProjectsVolumeEnsure(t *testing.T) {
	f := newCoordinatedDocker(t, false)
	f.missingVolume = true
	cfg := syntheticConfig()
	cfg.Services["web"].Volumes = []json.RawMessage{json.RawMessage(`"data:/data"`)}
	useSyntheticConfig(t, cfg)
	var output bytes.Buffer
	if err := runNamedPreview(context.Background(), namedPreviewArgs(f, "up"), &output); err != nil {
		t.Fatalf("%v: %s", err, output.String())
	}
	var result preflight.Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range result.Resources {
		if row.Kind == "volume" {
			found = true
			if row.Presence != "absent" || row.Action != "ensure" {
				t.Fatalf("wrong volume intent: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("missing declared volume")
	}
}

func TestNamedLiveMountedVolumeMustExist(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{true: "missing", false: "present"}[missing], func(t *testing.T) {
			f := newCoordinatedDocker(t, true)
			f.liveNamedVolume = true
			f.missingVolume = missing
			cfg := syntheticConfig()
			cfg.Services["web"].Volumes = []json.RawMessage{json.RawMessage(`"data:/data"`)}
			useSyntheticConfig(t, cfg)
			var output bytes.Buffer
			err := runNamedPreview(context.Background(), namedPreviewArgs(f, "up"), &output)
			var result preflight.Result
			if e := json.Unmarshal(output.Bytes(), &result); e != nil {
				t.Fatal(e)
			}
			if missing {
				if err == nil || result.Outcome != "failed" || len(result.Resources) != 0 || result.SelectionDigest != nil {
					t.Fatalf("missing mounted volume exposed review: %+v", result)
				}
			} else if err != nil || result.Outcome != "succeeded" || result.Coverage.Volumes.PresentCount != 1 {
				t.Fatalf("present volume failed: %+v err=%v", result, err)
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatalf("preview wrote %d times", writes)
			}
		})
	}
}
