//go:build linux || darwin

package coordination

import (
	"bytes"
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"path/filepath"
)

const BrokerProfileVersion = "dill.broker-profile/1"

// BrokerProfile is private operator configuration. Never decode it from source,
// binding forms, target HTTP requests, or deployment receipts.
type BrokerProfile struct {
	Contract  string               `json:"contract"`
	Docker    *Qualification       `json:"docker,omitempty"`
	Podman    *PodmanQualification `json:"podman,omitempty"`
	Namespace *NamespaceProfile    `json:"namespace,omitempty"`
}

func (p BrokerProfile) Valid() bool {
	if p.Contract != BrokerProfileVersion || (p.Docker == nil) == (p.Podman == nil) {
		return false
	}
	if p.Podman != nil {
		return p.Namespace == nil && p.Podman.valid()
	}
	q := p.Docker
	if !filepath.IsAbs(q.Socket) || filepath.Clean(q.Socket) != q.Socket || !safeDaemonID(q.DaemonID) {
		return false
	}
	if p.Namespace == nil {
		return q.Layout == NativeDockerLayout
	}
	return q.Layout == NativeDockerNamespaceLayout && p.Namespace.Rootless && p.Namespace.Valid()
}
func (p BrokerProfile) Engine() string {
	if !p.Valid() {
		return ""
	}
	if p.Podman != nil {
		return "podman"
	}
	return "docker"
}
func (p BrokerProfile) Socket() string {
	if !p.Valid() {
		return ""
	}
	if p.Podman != nil {
		return p.Podman.Socket
	}
	return p.Docker.Socket
}
func (p BrokerProfile) TargetDigest() (string, error) {
	if !p.Valid() {
		return "", ErrNamespace
	}
	var id string
	var err error
	if p.Podman != nil {
		id, err = PodmanIdentity(*p.Podman)
	} else if p.Namespace != nil {
		id, err = NamespacedDockerIdentity(p.Docker.DaemonID, *p.Namespace)
	} else {
		id, err = DockerIdentity(p.Docker.DaemonID)
	}
	if err != nil {
		return "", ErrNamespace
	}
	return "sha256:" + id, nil
}
func (p BrokerProfile) Qualify(ctx context.Context) (*Target, *PodmanTarget, error) {
	if !p.Valid() {
		return nil, nil, ErrNamespace
	}
	if p.Podman != nil {
		target, err := QualifyPodman(ctx, *p.Podman)
		if err != nil {
			return nil, nil, err
		}
		return target.Target, target, nil
	}
	if p.Namespace != nil {
		target, err := QualifyNamespacedDocker(ctx, *p.Docker, *p.Namespace)
		return target, nil, err
	}
	target, err := Qualify(ctx, *p.Docker)
	if err != nil {
		return nil, nil, err
	}
	if !target.BrokerRootful() {
		target.Close()
		return nil, nil, ErrNamespace
	}
	return target, nil, nil
}
func decodeBrokerProfile(data []byte) (BrokerProfile, error) {
	var p BrokerProfile
	if len(data) == 0 || len(data) > 64<<10 || brokersecret.DecodeMetadata(bytes.NewReader(data), &p) != nil || !p.Valid() {
		return BrokerProfile{}, ErrNamespace
	}
	return p, nil
}
