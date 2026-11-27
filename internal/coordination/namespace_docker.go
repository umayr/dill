//go:build linux || darwin

package coordination

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
)

const NativeDockerNamespaceLayout = "native-local-docker-userns-v1"

// NamespacedDockerIdentity binds offline enrollment to the exact operator
// mapping commitment; it is not live qualification or permission to mutate.
func NamespacedDockerIdentity(daemon string, profile NamespaceProfile) (string, error) {
	if !profile.Rootless {
		return "", ErrNamespace
	}
	id, err := DockerIdentity(daemon)
	if err != nil {
		return "", ErrNamespace
	}
	digest, err := profile.Digest()
	if err != nil {
		return "", ErrNamespace
	}
	sum := sha256.Sum256([]byte("dill.namespaced-docker/1\x00" + id + "\x00" + digest))
	return hex.EncodeToString(sum[:]), nil
}

// QualifyNamespacedDocker retains the same connection for profile observation
// and every later operation. It never enters a namespace or alters ownership.
// Existing broker CLI/adapter gates remain closed until explicitly wired.
func QualifyNamespacedDocker(ctx context.Context, q Qualification, expected NamespaceProfile) (*Target, error) {
	id, err := NamespacedDockerIdentity(q.DaemonID, expected)
	if err != nil {
		return nil, ErrNamespace
	}
	target, err := qualifyDocker(ctx, q, NativeDockerNamespaceLayout)
	if err != nil {
		return nil, ErrNamespace
	}
	fail := func() (*Target, error) { target.Close(); return nil, ErrNamespace }
	conn, ok := target.conn.(*net.UnixConn)
	if !ok || !target.brokerRootless || CheckNamespacePeer(conn, expected) != nil {
		return fail()
	}
	target.namespaceProfile = cloneNamespace(expected)
	target.identity = id
	target.namespaceDigest, _ = expected.Digest()
	return target, nil
}
func (t *Target) NamespaceDigest() string {
	if t == nil {
		return ""
	}
	return t.namespaceDigest
}
