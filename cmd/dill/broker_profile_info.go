package main

import (
	"encoding/json"
	"flag"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/coordination"
	"io"
	"path/filepath"
)

type brokerProfileInfo struct {
	Contract       string `json:"contract"`
	Engine         string `json:"engine,omitempty"`
	TargetDigest   string `json:"targetDigest,omitempty"`
	EndpointDigest string `json:"endpointDigest,omitempty"`
	RuntimeOutcome string `json:"runtimeOutcome,omitempty"`
	Code           string `json:"code,omitempty"`
}

func parseBrokerProfileInfo(args []string) (string, error) {
	fs := flag.NewFlagSet("broker-profile-info", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	seen := map[string]bool{}
	contract, path := "", ""
	for k, p := range map[string]*string{"contract": &contract, "broker-profile": &path} {
		fs.Func(k, "", func(v string) error {
			if seen[k] || v == "" {
				return brokersecret.ErrInvalid
			}
			seen[k] = true
			*p = v
			return nil
		})
	}
	if fs.Parse(args) != nil || fs.NArg() != 0 || len(seen) != 2 || contract != coordination.BrokerProfileVersion || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", brokersecret.ErrInvalid
	}
	return path, nil
}
func describeBrokerProfile(p coordination.BrokerProfile) (brokerProfileInfo, error) {
	digest, err := p.TargetDigest()
	if err != nil {
		return brokerProfileInfo{}, brokersecret.ErrUnavailable
	}
	return brokerProfileInfo{Contract: coordination.BrokerProfileVersion, Engine: p.Engine(), TargetDigest: digest, EndpointDigest: brokersecret.Digest([]byte("unix://" + p.Socket())), RuntimeOutcome: "not-run"}, nil
}
func runBrokerProfileInfo(args []string, out io.Writer) error {
	reply := brokerProfileInfo{Contract: coordination.BrokerProfileVersion}
	code := 0
	path, err := parseBrokerProfileInfo(args)
	if err != nil {
		code = 2
		reply.Code = "INVALID_INPUT"
	} else {
		p, e := coordination.ReadBrokerProfile(path)
		if e != nil {
			code = 1
			reply.Code = "PROFILE_UNAVAILABLE"
		} else {
			reply, err = describeBrokerProfile(p)
			if err != nil {
				code = 1
				reply = brokerProfileInfo{Contract: coordination.BrokerProfileVersion, Code: "PROFILE_UNAVAILABLE"}
			}
		}
	}
	if json.NewEncoder(out).Encode(reply) != nil {
		return protocolExit{1}
	}
	if code != 0 {
		return protocolExit{code}
	}
	return nil
}
