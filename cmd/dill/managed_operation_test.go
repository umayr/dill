package main

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func TestManagedOperationTracksExactCreationAndRetainsNetwork(t *testing.T) {
	f := newCoordinatedDocker(t, false)
	useSyntheticConfig(t, syntheticConfig())
	invoke := func(action, mode string, rows []operation.ManagedResource) (operation.Result, int) {
		t.Helper()
		args := namedPreviewArgs(f, action)
		var output bytes.Buffer
		if err := runNamedPreview(context.Background(), args, &output); err != nil {
			t.Fatalf("preview %s: %v: %s", action, err, output.String())
		}
		var plan preflight.Result
		if err := json.Unmarshal(output.Bytes(), &plan); err != nil || plan.SelectionDigest == nil {
			t.Fatal("missing selection", err)
		}
		args[2] = operation.Version
		args = append(args, "--preflight-contract", preflight.Version, "--expect-preflight", *plan.SelectionDigest)
		body, err := json.Marshal(operation.ManagedExpectation{Mode: mode, Resources: rows})
		if err != nil {
			t.Fatal(err)
		}
		out, stderr, err := captureCommand(t, string(body), func() error { return runManagedOperation(context.Background(), args) })
		var result operation.Result
		if json.Unmarshal([]byte(out), &result) != nil || stderr != "" {
			t.Fatalf("invalid result: %v: %q / %q", err, out, stderr)
		}
		return result, exitCodeFor(err)
	}
	created, code := invoke("up", "create", []operation.ManagedResource{})
	if code != 0 || created.ManagedResources == nil || len(*created.ManagedResources) != 2 {
		t.Fatalf("missing managed receipt: %+v (exit %d)", created, code)
	}
	owned := *created.ManagedResources
	for _, action := range []string{"stop", "start", "restart"} {
		result, code := invoke(action, "update", owned)
		if code != 0 || result.ManagedResources == nil || !slices.Equal(*result.ManagedResources, owned) {
			t.Fatalf("lifecycle %s lost exact managed identity: %+v exit%d", action, result, code)
		}
	}
	wrong := slices.Clone(owned)
	wrong[0].ID = strings.Repeat("1", 64)
	f.mu.Lock()
	before := f.writes
	f.mu.Unlock()
	rejected, code := invoke("down", "remove", wrong)
	if code != 1 || rejected.Effects != "none" || rejected.ManagedResources != nil {
		t.Fatalf("mismatch not rejected: %+v", rejected)
	}
	f.mu.Lock()
	after := f.writes
	f.mu.Unlock()
	if after != before {
		t.Fatal("attempted engine write on mismatch")
	}
	updated, code := invoke("up", "update", owned)
	if code != 0 || updated.ManagedResources == nil || !slices.Equal(*updated.ManagedResources, owned) {
		t.Fatalf("update receipt does not match: %+v (exit %d)", updated, code)
	}
	removed, code := invoke("down", "remove", owned)
	if code != 0 || removed.Effects != "complete" || removed.ManagedResources == nil || len(*removed.ManagedResources) != 0 {
		t.Fatalf("managed removal failed: %+v (exit %d)", removed, code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.exists || !f.networkExists || f.uncoordinatedWrite {
		t.Fatal("unexpected engine effects")
	}
}

func TestManagedSelectionRequiresExactInventory(t *testing.T) {
	containerID, networkID := strings.Repeat("1", 64), strings.Repeat("2", 64)
	owned := []operation.ManagedResource{{Kind: "container", Name: "demo-app", ID: containerID}, {Kind: "network", Name: "demo", ID: networkID}}
	container := preflight.Resource{Kind: "container", Name: "demo-app", Identity: containerID, IdentityKind: "immutable-id", Presence: "present", Action: "recreate"}
	network := preflight.Resource{Kind: "network", Name: "demo", Identity: networkID, IdentityKind: "immutable-id", Presence: "present", Action: "ensure"}
	volume := preflight.Resource{Kind: "volume", Name: "demo-data", Identity: "demo-data", IdentityKind: "engine-name", Presence: "present", Action: "retain"}
	replaced := container
	replaced.Identity = strings.Repeat("3", 64)
	missing := container
	missing.Presence = "absent"
	unexpected := container
	unexpected.Name = "demo-other"
	wrongNetwork := network
	wrongNetwork.Identity = strings.Repeat("4", 64)
	reuseVolume := volume
	reuseVolume.Action = "ensure"
	addition := unexpected
	addition.Presence = "absent"
	addition.Action = "create"
	for _, tc := range []struct {
		name, mode string
		rows       []preflight.Resource
		allow      bool
	}{
		{"update same identities", "update", []preflight.Resource{container, network}, true},
		{"update can add new resource", "update", []preflight.Resource{container, network, addition}, true},
		{"missing managed resource", "update", []preflight.Resource{missing, network}, false},
		{"extra live resource", "update", []preflight.Resource{container, network, unexpected}, false},
		{"replaced managed container", "update", []preflight.Resource{replaced, network}, false},
		{"replaced managed network", "remove", []preflight.Resource{container, wrongNetwork}, false},
		{"duplicate live row", "update", []preflight.Resource{container, container, network}, false},
		{"existing volume cannot be ensured", "update", []preflight.Resource{container, network, reuseVolume}, false},
		{"existing volume can be retained", "remove", []preflight.Resource{container, network, volume}, true},
		{"creation never adopts containers", "create", []preflight.Resource{container}, false},
		{"creation never adopts volumes", "create", []preflight.Resource{volume}, false},
		{"creation absent only", "create", []preflight.Resource{addition}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := owned
			if tc.mode == "create" {
				resources = []operation.ManagedResource{}
			}
			if ok := checkManagedSelection(operation.ManagedExpectation{Mode: tc.mode, Resources: resources}, tc.rows) == nil; ok != tc.allow {
				t.Fatalf("allowed=%v, want %v", ok, tc.allow)
			}
		})
	}
}
