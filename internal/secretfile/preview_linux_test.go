//go:build linux

package secretfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func previewFixture(t *testing.T) (Request, string, string, string) {
	t.Helper()
	dir := protectedTestDir(t)
	workspace := filepath.Join(dir, "workspace")
	keys := filepath.Join(dir, "keys")
	if os.Mkdir(workspace, 0700) != nil || os.Mkdir(keys, 0700) != nil {
		t.Fatal("synthetic private directory")
	}
	policy := filepath.Join(dir, "policy")
	bindings := filepath.Join(dir, "bindings")
	key := filepath.Join(keys, "key")
	p := fmt.Sprintf(`{"version":"dill.secret-policy/1","targets":[{"id":"lab","engine":"docker","socket":"/run/synthetic.sock","stacks":[{"name":"demo","sources":[{"id":"source","revision":"r1","kind":"hostFile","path":"%s","uid":%d,"gid":%d,"mode":256}]}]}]}`, filepath.Join(dir, "source"), os.Geteuid(), os.Getegid())
	for name, b := range map[string][]byte{policy: []byte(p), bindings: []byte(`{"version":"dill.secret-bindings/1","target":"lab","stack":"demo","bindings":[{"name":"db","source":"source","revision":"r1"}]}`), key: bytes.Repeat([]byte{0x42}, 32)} {
		if os.WriteFile(name, b, 0600) != nil {
			t.Fatal("synthetic metadata write")
		}
	}
	cfg := &config.DillConfig{Name: "demo", Engine: "docker", Secrets: []config.SecretRequirement{{Name: "db"}}, Services: map[string]*config.Service{"app": {Image: "image", Secrets: []config.SecretGrant{{Name: "db", Target: "/run/secrets/db", Mode: 256}}}}}
	req := Request{Config: cfg, PolicyPath: policy, BindingsPath: bindings, Target: "lab", Stack: "demo", Engine: "docker", Socket: "/run/synthetic.sock"}
	if os.WriteFile(filepath.Join(dir, "source"), []byte("synthetic-metadata-only"), 0400) != nil {
		t.Fatal("synthetic source provision")
	}
	return req, key, filepath.Join(dir, "proof"), filepath.Join(workspace, "compose.pkl")
}

// Daemon/mapping provenance is synthetic; source metadata validation is real.
func observePreviewFixture(t *testing.T, r *Result) {
	t.Helper()
	mapping := Mapping{UID: []IDRange{{0, uint64(os.Geteuid()), 1}}, GID: []IDRange{{0, uint64(os.Getegid()), 1}}}
	var err error
	r.sources, err = observeSources(r.mounts, mapping)
	if err != nil {
		t.Fatal("synthetic source metadata unavailable")
	}
	r.runtime = &runtimeEvidence{mapping: mapping}
}

func TestPreviewPrivatePublication(t *testing.T) {
	req, key, out, cfg := previewFixture(t)
	state := PreviewState{Configuration: []byte(`{"services":{}}`), Live: []byte(`{"state":"running"}`), Plan: []byte(`{"changes":[]}`)}
	// Synthetic IO/MAC evidence ONLY, not a claim of checked daemon availability.
	publish := func(path string) string {
		t.Helper()
		r, err := authorize(req)
		if err != nil {
			t.Fatal(err)
		}
		observePreviewFixture(t, r)
		finish, err := PublishPreview(req, r, key, path, cfg, state)
		if err != nil {
			t.Fatal(err)
		}
		finish(true)
		b, err := readPrivate(path, uint32(os.Geteuid()))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct{ Contract, Proof string }
		if json.Unmarshal(b, &doc) != nil || doc.Contract != PreviewContract || len(doc.Proof) != 43 {
			t.Fatal("unexpected proof wire file")
		}
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatal("proof not private")
		}
		return doc.Proof
	}
	first := publish(out)
	if first != publish(out+"-repeat") {
		t.Fatal("fresh output filename changed proof")
	}
	original, _ := os.ReadFile(out)
	r, _ := authorize(req)
	observePreviewFixture(t, r)
	if _, err := PublishPreview(req, r, key, out, cfg, state); err != config.ErrSecretUnavailable {
		t.Fatal("preexisting file overwritten")
	}
	if after, _ := os.ReadFile(out); !bytes.Equal(original, after) {
		t.Fatal("original proof changed")
	}
	state.Live = []byte(`{"state":"exited"}`)
	if first == publish(out+"-state") {
		t.Fatal("live change did not invalidate")
	}
	state.Live = []byte(`{"state":"running"}`)
	state.Configuration = []byte(`{"services":{"imported":"changed"}}`)
	if first == publish(out+"-cfg") {
		t.Fatal("effective imported config change did not invalidate")
	}
	state.Configuration = []byte(`{"services":{}}`)
	state.Plan = []byte(`{"changes":["recreate"]}`)
	if first == publish(out+"-plan") {
		t.Fatal("plan change did not invalidate")
	}
	state.Plan = []byte(`{"changes":[]}`)
	b, _ := os.ReadFile(req.PolicyPath)
	var policy map[string]any
	json.Unmarshal(b, &policy)
	policy["targets"].([]any)[0].(map[string]any)["stacks"].([]any)[0].(map[string]any)["sources"].([]any)[0].(map[string]any)["path"] = filepath.Join(filepath.Dir(out), "rotated-source")
	b, _ = json.Marshal(policy)
	os.WriteFile(req.PolicyPath, b, 0600)
	if os.WriteFile(filepath.Join(filepath.Dir(out), "rotated-source"), []byte("synthetic-metadata-only"), 0400) != nil {
		t.Fatal("synthetic rotated source provision")
	}
	if first == publish(out+"-policy") {
		t.Fatal("authorized policy path change did not invalidate")
	}
	// A successfully published file is withdrawn if plan rendering fails.
	r, _ = authorize(req)
	observePreviewFixture(t, r)
	finish, err := PublishPreview(req, r, key, out+"-failed", cfg, state)
	if err != nil {
		t.Fatal(err)
	}
	finish(false)
	if _, err := os.Lstat(out + "-failed"); !os.IsNotExist(err) {
		t.Fatal("failed rendering retained proof")
	}
}
func TestPreviewUnsafeFilesAndSourceKeyRejected(t *testing.T) {
	for _, kind := range []string{"short", "long", "public", "symlink", "hardlink", "workspace", "source", "existing directory", "existing symlink", "unprotected output", "authorization only", "absent source output", "unsafe catalog key alias", "unsafe catalog output alias"} {
		t.Run(kind, func(t *testing.T) {
			req, key, out, cfg := previewFixture(t)
			r, _ := authorize(req)
			observePreviewFixture(t, r)
			switch kind {
			case "short":
				os.WriteFile(key, []byte("short"), 0600)
			case "long":
				os.WriteFile(key, bytes.Repeat([]byte{1}, 33), 0600)
			case "public":
				os.Chmod(key, 0644)
			case "symlink":
				os.Symlink(key, key+"-link")
				key += "-link"
			case "hardlink":
				os.Link(key, key+"-link")
			case "workspace":
				key = filepath.Join(filepath.Dir(cfg), "key")
				os.WriteFile(key, bytes.Repeat([]byte{1}, 32), 0600)
			case "unsafe catalog key alias", "unsafe catalog output alias":
				unsafe := filepath.Join(filepath.Dir(out), "unsafe")
				if os.Mkdir(unsafe, 0700) != nil || os.Chmod(unsafe, 0777) != nil {
					t.Fatal("cannot provision unsafe synthetic ancestor")
				}
				target, leaf := filepath.Dir(key), filepath.Base(key)
				if kind == "unsafe catalog output alias" {
					target, leaf = filepath.Dir(out), filepath.Base(out)
				}
				if os.Symlink(target, filepath.Join(unsafe, "alias")) != nil {
					t.Fatal("cannot provision directory alias")
				}
				raw, _ := os.ReadFile(req.PolicyPath)
				var p config.SecretPolicy
				if json.Unmarshal(raw, &p) != nil {
					t.Fatal("synthetic policy parse")
				}
				extra := p.Targets[0].Stacks[0].Sources[0]
				extra.ID = "future"
				extra.Path = filepath.Join(unsafe, "alias", leaf)
				p.Targets[0].Stacks[0].Sources = append(p.Targets[0].Stacks[0].Sources, extra)
				raw, _ = json.Marshal(p)
				if os.WriteFile(req.PolicyPath, raw, 0600) != nil {
					t.Fatal("cannot provision alias catalog")
				}
			case "source":
				key = filepath.Join(filepath.Dir(out), "source")
				os.WriteFile(key, bytes.Repeat([]byte{1}, 32), 0400)
			case "existing directory":
				os.Mkdir(out, 0700)
			case "existing symlink":
				os.Symlink(key, out)
			case "unprotected output":
				bad := filepath.Join(filepath.Dir(out), "unprotected")
				os.Mkdir(bad, 0700)
				os.Chmod(bad, 0777)
				out = filepath.Join(bad, "proof")
			case "absent source output":
				out = filepath.Join(filepath.Dir(out), "source")
			case "authorization only":
				r.runtime = nil
			}
			if kind == "unsafe catalog key alias" || kind == "unsafe catalog output alias" {
				r, _ = authorize(req)
				observePreviewFixture(t, r)
			}
			_, err := PublishPreview(req, r, key, out, cfg, PreviewState{Configuration: []byte(`{}`), Live: []byte(`{}`), Plan: []byte(`{}`)})
			if err != config.ErrSecretUnavailable {
				t.Fatal("unsafe proof inputs accepted")
			}
		})
	}
}

func TestPreviewAuthorityAndKeyRotation(t *testing.T) {
	req, key, out, cfg := previewFixture(t)
	state := PreviewState{Configuration: []byte(`{}`), Live: []byte(`{}`), Plan: []byte(`{}`)}
	counter := 0
	publish := func() string {
		t.Helper()
		counter++
		r, err := authorize(req)
		if err != nil {
			t.Fatal(err)
		}
		observePreviewFixture(t, r)
		path := fmt.Sprintf("%s-%d", out, counter)
		finish, err := PublishPreview(req, r, key, path, cfg, state)
		if err != nil {
			t.Fatal(err)
		}
		finish(true)
		b, err := readPrivate(path, uint32(os.Geteuid()))
		if err != nil {
			t.Fatal(err)
		}
		var d struct{ Proof string }
		json.Unmarshal(b, &d)
		return d.Proof
	}
	first := publish()
	os.WriteFile(key, bytes.Repeat([]byte{0x43}, 32), 0600)
	if publish() == first {
		t.Fatal("key rotation retained approval")
	}
	os.WriteFile(key, bytes.Repeat([]byte{0x42}, 32), 0600)
	req.NoDeps = true
	if publish() == first {
		t.Fatal("selection options retained approval")
	}
	req.NoDeps = false
	// Identical public plan, different authorized candidate identity.
	p, _ := os.ReadFile(req.PolicyPath)
	b, _ := os.ReadFile(req.BindingsPath)
	os.WriteFile(req.PolicyPath, bytes.ReplaceAll(p, []byte(`"lab"`), []byte(`"lab-other"`)), 0600)
	os.WriteFile(req.BindingsPath, bytes.ReplaceAll(b, []byte(`"lab"`), []byte(`"lab-other"`)), 0600)
	req.Target = "lab-other"
	if publish() == first {
		t.Fatal("target identity retained approval")
	}
}

func TestPreviewObservedSourceReplacementInvalidates(t *testing.T) {
	req, key, out, cfg := previewFixture(t)
	state := PreviewState{Configuration: []byte(`{}`), Live: []byte(`{}`), Plan: []byte(`{}`)}
	checked := func() *Result {
		r, err := authorize(req)
		if err != nil {
			t.Fatal(err)
		}
		observePreviewFixture(t, r)
		return r
	}
	publish := func(r *Result, path string) string {
		t.Helper()
		finish, err := PublishPreview(req, r, key, path, cfg, state)
		if err != nil {
			t.Fatal(err)
		}
		finish(true)
		b, err := readPrivate(path, uint32(os.Geteuid()))
		if err != nil {
			t.Fatal(err)
		}
		var d struct{ Proof string }
		if json.Unmarshal(b, &d) != nil {
			t.Fatal("proof parse")
		}
		return d.Proof
	}
	old := checked()
	first := publish(old, out)
	if publish(checked(), out+"-repeat") != first {
		t.Fatal("unchanged source invalidated proof")
	}
	source := old.mounts["app"][0].HostPath
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	replacement := source + "-replacement"
	if os.WriteFile(replacement, []byte("synthetic-metadata-only"), 0400) != nil || os.Chtimes(replacement, before.ModTime(), before.ModTime()) != nil || os.Rename(replacement, source) != nil {
		t.Fatal("cannot replace owned fixture source")
	}
	current := checked()
	a, b := old.sources["app"][0], current.sources["app"][0]
	if a.Device != b.Device || a.Inode == b.Inode || a.Size != b.Size || a.MTime != b.MTime || a.UID != b.UID || a.GID != b.GID || a.Mode != b.Mode || a.Links != b.Links {
		t.Fatal("replacement fixture did not preserve authorized stat fields")
	}
	if _, err := PublishPreview(req, old, key, out+"-stale", cfg, state); err != config.ErrSecretUnavailable {
		t.Fatal("stale preflight source evidence published")
	}
	changed := publish(current, out+"-changed")
	if changed == first || changed != publish(checked(), out+"-changed-repeat") {
		t.Fatal("valid replacement did not invalidate stably")
	}
}
