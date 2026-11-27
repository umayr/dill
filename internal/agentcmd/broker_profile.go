package agentcmd

import (
	"encoding/hex"
	"errors"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/coordination"
	"strings"
)

func validBrokerPin(s string) bool {
	if !strings.HasPrefix(s, "sha256:") || len(s) != 71 || strings.ToLower(s) != s {
		return false
	}
	b, e := hex.DecodeString(s[7:])
	return e == nil && len(b) == 32
}
func httpsBrokerProfile(path, socket, daemon string) (string, string, error) {
	p, err := coordination.ReadBrokerProfile(path)
	if err != nil {
		return "", "", errors.New("broker operator profile unavailable")
	}
	return httpsBrokerProfileScope(p, socket, daemon)
}
func httpsBrokerProfileScope(p coordination.BrokerProfile, socket, daemon string) (string, string, error) {
	if !p.Valid() || p.Engine() != "docker" || p.Docker == nil || p.Socket() != socket || p.Docker.DaemonID != daemon {
		return "", "", errors.New("broker profile must match the enrolled Docker monitor target")
	}
	target, err := p.TargetDigest()
	if err != nil {
		return "", "", brokersecret.ErrUnavailable
	}
	return target, brokersecret.Digest([]byte("unix://" + socket)), nil
}
func brokerEngine(engine string) string {
	if engine == "" {
		return "docker"
	}
	return engine
}
func operatorBrokerTarget(profile, socket, daemon string) (engine, target, endpoint string, err error) {
	if profile != "" {
		p, e := coordination.ReadBrokerProfile(profile)
		if e != nil {
			return "", "", "", brokersecret.ErrUnavailable
		}
		digest, e := p.TargetDigest()
		if e != nil {
			return "", "", "", brokersecret.ErrUnavailable
		}
		return p.Engine(), digest, brokersecret.Digest([]byte("unix://" + p.Socket())), nil
	}
	id, e := coordination.DockerIdentity(daemon)
	if e != nil {
		return "", "", "", brokersecret.ErrInvalid
	}
	return "docker", "sha256:" + id, brokersecret.Digest([]byte("unix://" + socket)), nil
}
