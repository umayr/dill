package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func TestNamedDownEmptyRetainedAndAbsentNetwork(t *testing.T) {
	for _, network := range []bool{false, true} {
		t.Run(map[bool]string{true: "retained", false: "absent"}[network], func(t *testing.T) {
			f := newCoordinatedDocker(t, false)
			f.networkExists = network
			useSyntheticConfig(t, syntheticConfig())
			args := namedPreviewArgs(f, "down")
			var out bytes.Buffer
			if err := runNamedPreview(context.Background(), args, &out); err != nil {
				t.Fatalf("%v %s", err, out.String())
			}
			var preview preflight.Result
			if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			apply := append([]string{}, args...)
			apply[2] = "dill.operation/1"
			apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *preview.SelectionDigest)
			result, code, _ := captureOperation(t, "", apply)
			if network {
				if code != 1 || result.Outcome != "failed" || result.Effects != "none" {
					t.Fatalf("retained down %+v code=%d", result, code)
				}
			} else if code != 0 || result.Outcome != "succeeded" || result.Effects != "none" {
				t.Fatalf("absent down %+v code=%d", result, code)
			}
		})
	}
}
