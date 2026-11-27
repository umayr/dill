//go:build linux || darwin

package coordination

import (
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestNamespacedDockerIdentitySeparatesMapsAndLayout(t *testing.T) {
	p := rootlessProfile()
	a, err := NamespacedDockerIdentity(testDaemon, p)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := DockerIdentity(testDaemon)
	if a == plain {
		t.Fatal("ordinary qualification substituted for namespaced target")
	}
	p.UIDMap[1].Outside++
	b, err := NamespacedDockerIdentity(testDaemon, p)
	if err != nil || a == b {
		t.Fatal("mapping identity not bound")
	}
	p.Rootless = false
	if _, err = NamespacedDockerIdentity(testDaemon, p); err == nil {
		t.Fatal("rootful substituted")
	}
	var requests atomic.Int32
	path := filepath.Join(socketDir(t), "engine.sock")
	fakeDocker(t, path, testDaemon, &requests)
	ctx, cancel := bounded()
	defer cancel()
	if _, err = Qualify(ctx, Qualification{NativeDockerNamespaceLayout, path, testDaemon}); err == nil {
		t.Fatal("namespaced layout entered ordinary qualifier")
	}
	if _, err = QualifyNamespacedDocker(ctx, Qualification{NativeDockerNamespaceLayout, path, testDaemon}, rootlessProfile()); err == nil {
		t.Fatal("missing rootless/namespace evidence admitted")
	}
}
