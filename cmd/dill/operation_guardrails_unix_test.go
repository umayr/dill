//go:build darwin || linux

package main

import (
	"encoding/json"
	"testing"
)

// Exercise the active qualified decorator and native SDK without the disabled
// Alpha flag. The Unix engine is an in-process synthetic HTTP server.
func TestQualifiedSDKRejectsImplicitAndUnknownImageBeforeWrites(t *testing.T) {
	for _, kind := range []string{"implicit", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			f := newCoordinatedDocker(t, false)
			f.implicitVolumes = kind == "implicit"
			f.unknownPreparedImage = kind == "unknown"
			useSyntheticConfig(t, syntheticConfig())
			result, code, stderr := captureOperation(t, "", f.args("up"))
			f.mu.Lock()
			writes, reads := f.writes, f.imageReads
			f.mu.Unlock()
			if code != 1 || result.Effects != "none" || writes != 0 || stderr != "" || reads == 0 || len(result.Errors) != 1 || result.Errors[0].Code != "OPERATION_FAILED" || result.Outcome != "failed" {
				t.Fatalf("result=%+v code=%d stderr=%q writes=%d reads=%d", result, code, stderr, writes, reads)
			}
		})
	}
}

func TestQualifiedSDKAcceptsKnownEmptyImage(t *testing.T) {
	f := newCoordinatedDocker(t, false)
	useSyntheticConfig(t, syntheticConfig())
	result, code, stderr := captureOperation(t, "", f.args("up"))
	f.mu.Lock()
	reads, writes := f.imageReads, f.writes
	f.mu.Unlock()
	if code != 0 || result.Outcome != "succeeded" || stderr != "" || reads == 0 || writes == 0 {
		t.Fatalf("result=%+v code=%d stderr=%q imageReads=%d writes=%d", result, code, stderr, reads, writes)
	}
}

func TestQualifiedSDKRejectsReplacedVolumeAtCreation(t *testing.T) {
	f := newCoordinatedDocker(t, false)
	f.networkExists = true
	f.replaceVolumeAt = 5
	cfg := syntheticConfig()
	cfg.Services["web"].Volumes = []json.RawMessage{json.RawMessage(`"data:/data"`)}
	useSyntheticConfig(t, cfg)
	result, code, stderr := captureOperation(t, "", f.args("up"))
	f.mu.Lock()
	reads, writes := f.volumeReads, f.writes
	f.mu.Unlock()
	if code != 1 || result.Effects != "unknown" || writes != 0 || reads < 5 || stderr != "" {
		t.Fatalf("result=%+v code=%d stderr=%q reads=%d writes=%d", result, code, stderr, reads, writes)
	}
}
