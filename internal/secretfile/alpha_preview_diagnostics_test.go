//go:build integration && linux

package secretfile

import (
	"crypto/hmac"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/umayr/dill/internal/config"
)

// AlphaPreviewDiagnosticSnapshot is exported only to this package's external
// integration test. Private frames never leave protected fixture files. Verify
// the copied authority shape against the real CLI MAC before interpreting a diff.
// No production hook/API or source byte read is introduced.
func AlphaPreviewDiagnosticSnapshot(req Request, result *Result, state PreviewState, keyPath, proofPath, outputPath, baselinePath string) (bool, []string, error) {
	fail := func() (bool, []string, error) { return false, nil, config.ErrSecretUnavailable }
	if !result.AvailabilityChecked() || !result.Matches(req) {
		return fail()
	}
	observed, err := observeSources(result.mounts, result.runtime.mapping)
	if err != nil || !reflect.DeepEqual(observed, result.sources) {
		return fail()
	}
	authority, err := CanonicalPreviewJSON(struct {
		Target, Stack, Engine, Socket string
		Services                      []string
		NoDeps                        bool
		Policy                        *config.SecretPolicy
		Bindings                      *config.SecretBindings
		Mounts                        map[string][]config.ResolvedSecret
		Sources                       map[string][]sourceObservation
	}{req.Target, req.Stack, req.Engine, req.Socket, req.Services, req.NoDeps, result.policy, result.bindings, result.mounts, result.sources})
	if err != nil {
		return fail()
	}
	sections := [][]byte{authority}
	for _, section := range []json.RawMessage{state.Configuration, state.Live, state.Plan} {
		b, err := CanonicalPreviewJSON(section)
		if err != nil {
			return fail()
		}
		sections = append(sections, b)
	}
	owner := uint32(os.Geteuid())
	key, err := readPrivate(keyPath, owner)
	if err != nil {
		return fail()
	}
	defer clear(key)
	proof, err := readPrivate(proofPath, owner)
	var doc struct{ Contract, Proof string }
	if err != nil || len(key) != 32 || json.Unmarshal(proof, &doc) != nil || doc.Contract != PreviewContract {
		return fail()
	}
	matches := hmac.Equal([]byte(previewMAC(key, sections...)), []byte(doc.Proof))
	if !matches {
		// Never publish an unverified baseline for a later phase to consume.
		return false, nil, nil
	}
	data, err := json.Marshal(struct{ Authority, Configuration, Live, Plan json.RawMessage }{sections[0], sections[1], sections[2], sections[3]})
	if err != nil || len(data) > 65536 {
		return fail()
	}
	var changed []string
	if baselinePath != "" {
		before, err := readPrivate(baselinePath, owner)
		if err != nil {
			return fail()
		}
		changed, err = alphaPreviewChangedFields(before, data)
		if err != nil {
			return fail()
		}
	}
	finish, err := publishPrivate(outputPath, data)
	if err != nil {
		return fail()
	}
	finish(true)
	return matches, changed, nil
}

func TestAlphaPreviewDiagnosticMatchesPublishedMAC(t *testing.T) {
	req, key, output, recipe := previewFixture(t)
	r := authorizePreviewFixture(t, req)
	observePreviewFixture(t, r)
	state := PreviewState{Configuration: []byte(`{"services":{}}`), Live: []byte(`{"state":"running"}`), Plan: []byte(`{"changes":[]}`)}
	finish, err := PublishPreview(req, r, key, output, recipe, state)
	if err != nil {
		t.Fatal("synthetic proof publication failed")
	}
	finish(true)
	matched, _, err := AlphaPreviewDiagnosticSnapshot(req, r, state, key, output, output+"-snapshot", "")
	if err != nil || !matched {
		t.Fatal("diagnostic frames do not match production MAC")
	}
	state.Live = []byte(`{"state":"exited"}`)
	matched, changed, err := AlphaPreviewDiagnosticSnapshot(req, r, state, key, output, output+"-changed", output+"-snapshot")
	if err != nil || matched || len(changed) != 0 {
		t.Fatal("diagnostic accepted stale proof")
	}
	if _, err := os.Stat(output + "-changed"); !os.IsNotExist(err) {
		t.Fatal("unverified snapshot was published")
	}
	finish, err = PublishPreview(req, r, key, output+"-fresh", recipe, state)
	if err != nil {
		t.Fatal("changed synthetic proof publication failed")
	}
	finish(true)
	matched, changed, err = AlphaPreviewDiagnosticSnapshot(req, r, state, key, output+"-fresh", output+"-changed", output+"-snapshot")
	if err != nil || !matched || len(changed) != 1 {
		t.Fatal("diagnostic lost verified changed frame")
	}
}
