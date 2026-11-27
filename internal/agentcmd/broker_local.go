package agentcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/serve"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type localBrokerOptions struct {
	action, socket, daemon, state, store, history, root, catalog, pkl, stage, journal, id string
	profile, target, endpoint, engine                                                     string
}

func parseLocalBroker(args []string) (localBrokerOptions, error) {
	var o localBrokerOptions
	if len(args) == 0 {
		return o, brokersecret.ErrInvalid
	}
	o.action = args[0]
	fields := map[string]*string{"contract": new(string), "engine-socket": &o.socket, "daemon-id": &o.daemon, "coordination-state-dir": &o.state, "broker-store": &o.store, "history-dir": &o.history, "staging-root": &o.root, "staging-catalog": &o.catalog, "pkl": &o.pkl}
	fields["broker-profile"] = &o.profile
	fields["target-digest"] = &o.target
	fields["endpoint-digest"] = &o.endpoint
	fields["engine-kind"] = &o.engine
	switch o.action {
	case "init", "journal", "stage":
	case "snapshot":
		fields["stage-id"] = &o.stage
	case "requirements", "prepare", "retire", "review", "apply":
		fields["stage-id"] = &o.stage
		fields["journal-id"] = &o.journal
	case "status":
		fields["request-id"] = &o.id
		fields["journal-id"] = &o.journal
	case "fence":
		fields["journal-id"] = &o.journal
	default:
		return o, brokersecret.ErrInvalid
	}
	fs := flag.NewFlagSet("broker-local", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	seen := map[string]bool{}
	for k, p := range fields {
		fs.Func(k, "", func(v string) error {
			if seen[k] || v == "" {
				return brokersecret.ErrInvalid
			}
			seen[k] = true
			*p = v
			return nil
		})
	}
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || *fields["contract"] != brokersecret.Version {
		return o, brokersecret.ErrInvalid
	}
	for name := range fields {
		if name == "engine-socket" || name == "daemon-id" || name == "broker-profile" || name == "target-digest" || name == "endpoint-digest" || name == "engine-kind" {
			continue
		}
		if !seen[name] {
			return o, brokersecret.ErrInvalid
		}
	}
	if seen["broker-profile"] {
		if seen["engine-socket"] || seen["daemon-id"] || !seen["target-digest"] || !seen["endpoint-digest"] || !seen["engine-kind"] || !validBrokerPin(o.target) || !validBrokerPin(o.endpoint) || (o.engine != "docker" && o.engine != "podman") {
			return o, brokersecret.ErrInvalid
		}
	} else if !seen["engine-socket"] || !seen["daemon-id"] || seen["target-digest"] || seen["endpoint-digest"] || seen["engine-kind"] {
		return o, brokersecret.ErrInvalid
	}
	paths := []string{o.state, o.store, o.history, o.root, o.catalog, o.pkl}
	if o.profile != "" {
		paths = append(paths, o.profile)
	} else {
		paths = append(paths, o.socket)
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return o, brokersecret.ErrInvalid
		}
	}
	dirs := []string{o.state, o.store, o.history, o.root, o.catalog}
	for i, a := range dirs {
		for j, b := range dirs {
			if i < j && (a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")) {
				return o, brokersecret.ErrInvalid
			}
		}
	}
	if _, err := coordination.DockerIdentity(o.daemon); o.profile == "" && err != nil {
		return o, brokersecret.ErrInvalid
	}
	for _, id := range []string{o.stage, o.id, o.journal} {
		if id != "" && !validApplyID(id) {
			return o, brokersecret.ErrInvalid
		}
	}
	return o, nil
}

type localBrokerReply struct {
	Contract string          `json:"contract"`
	Status   int             `json:"status"`
	Journal  string          `json:"journalId,omitempty"`
	Mode     string          `json:"mode,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`
	Code     string          `json:"code,omitempty"`
}
type localBrokerResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *localBrokerResponse) Header() http.Header { return w.header }
func (w *localBrokerResponse) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *localBrokerResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.body.Len()+len(p) > brokersecret.MaxMetadata {
		w.overflow = true
		return 0, brokersecret.ErrCapacity
	}
	return w.body.Write(p)
}

// LocalBroker is an administrator-only, no-listener adapter. It uses the SAME
// catalog, ownership journal, broker handler and native checked child as HTTPS.
// All paths are trusted operator argv; no path is accepted from stdin.
func LocalBroker(ctx context.Context, args []string, in io.Reader, out io.Writer) int {
	o, err := parseLocalBroker(args)
	reply := localBrokerReply{Contract: brokersecret.Version}
	code := 1
	if err == nil {
		reply, err = runLocalBroker(ctx, o, in)
	}
	if err == nil {
		code = 0
	} else {
		reply.Code = "BROKER_LOCAL_UNAVAILABLE"
		if errors.Is(err, brokersecret.ErrInvalid) {
			code = 2
			reply.Code = "INVALID_INPUT"
		}
	}
	if json.NewEncoder(out).Encode(reply) != nil {
		return 1
	}
	return code
}
func runLocalBroker(ctx context.Context, o localBrokerOptions, in io.Reader) (reply localBrokerReply, retErr error) {
	reply.Contract = brokersecret.Version
	if ctx == nil || ctx.Err() != nil {
		return reply, brokersecret.ErrUnavailable
	}
	if o.profile != "" && o.action != "status" && o.action != "fence" && o.action != "journal" {
		engine, target, endpoint, err := operatorBrokerTarget(o.profile, "", "")
		if err != nil || engine != o.engine || target != o.target || endpoint != o.endpoint {
			return reply, brokersecret.ErrUnavailable
		}
	}
	// Status/fence remain available when runtime material is unreadable. They
	// never infer successful execution from material, or recreate missing history.
	if o.action == "init" || o.action == "prepare" || o.action == "retire" || o.action == "apply" || o.action == "review" {
		store, err := brokersecret.Open(o.store)
		if err != nil {
			return reply, err
		}
		if store.Close() != nil {
			return reply, brokersecret.ErrUnavailable
		}
	}
	if o.action == "init" {
		if os.Mkdir(o.history, 0700) != nil {
			return reply, brokersecret.ErrUnavailable
		}
	} else {
		if _, err := readPrivateHTTPSFile(filepath.Join(o.history, "operations.json"), historyByteLimit); err != nil {
			return reply, brokersecret.ErrUnavailable
		}
	}
	target := brokersecret.Digest([]byte("dill-broker-local/1\x00" + o.socket + "\x00" + o.daemon))
	if o.profile != "" {
		target = brokersecret.Digest([]byte("dill-broker-local-profile/1\x00" + o.engine + "\x00" + o.target + "\x00" + o.endpoint))
	}
	history, err := openDeploymentHistory(o.history, target)
	if err != nil {
		return reply, brokersecret.ErrUnavailable
	}
	defer func() {
		if history.dir.Close() != nil {
			retErr = brokersecret.ErrUnavailable
		}
	}()
	if o.action == "init" {
		reply.Status = 200
		reply.Journal = history.journal
		reply.Body = json.RawMessage("{}")
		return reply, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	response := &localBrokerResponse{header: make(http.Header)}
	method, path := "POST", "/v1/broker-secrets/"+o.action+"/"+o.stage
	switch o.action {
	case "journal":
		method, path = "GET", "/v1/broker-secrets/journal"
	case "status":
		method, path = "GET", "/v1/broker-secrets/operations/"+o.id
	case "stage":
		path = "/v1/snapshots"
	case "snapshot":
		method, path = "GET", "/v1/snapshots/"+o.stage
	case "fence":
		path = "/v1/deploy/fence"
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://local"+path, in)
	if err != nil {
		return reply, brokersecret.ErrInvalid
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(brokerContractHeader, brokersecret.Version)
	if o.journal != "" {
		request.Header.Set(journalHeader, o.journal)
	}
	if method == "GET" {
		request.Body = http.NoBody
		request.ContentLength = 0
	}
	if o.action == "fence" {
		(&applyEndpoint{history: history}).ServeHTTP(response, request)
	} else {
		// Journal/status need no source catalog or Pkl evaluation.
		id, _ := coordination.DockerIdentity(o.daemon)
		backend := &brokerEndpoint{history: history, slot: make(chan struct{}, 1), target: "sha256:" + id, endpoint: brokersecret.Digest([]byte("unix://" + o.socket))}
		if o.profile != "" {
			backend.engine = o.engine
			backend.target = o.target
			backend.endpoint = o.endpoint
		}
		if o.action == "journal" || o.action == "status" {
			backend.ServeHTTP(response, request)
		} else {
			stager, err := serve.NewCataloguedStager(o.root, o.catalog)
			if err != nil {
				return reply, brokersecret.ErrUnavailable
			}
			defer stager.Close()
			if o.action == "stage" || o.action == "snapshot" {
				serve.API{Stager: stager}.ServeHTTP(response, request)
			} else {
				executable, err := os.Executable()
				if err != nil || !filepath.IsAbs(executable) {
					return reply, brokersecret.ErrUnavailable
				}
				cli := brokerCLI{exe: executable, pkl: o.pkl, socket: o.socket, daemon: o.daemon, state: o.state, store: o.store, profile: o.profile, engine: o.engine}
				backend.stages = stager
				backend.requirements = cli.requirements
				backend.prepare = cli.prepare
				backend.retire = cli.retire
				backend.review = cli.review
				backend.apply = cli.apply
				backend.ServeHTTP(response, request)
			}
		}
	}
	reply.Status = response.status
	reply.Journal = response.header.Get(journalHeader)
	reply.Mode = response.header.Get("X-Dill-Deployment-Mode")
	expected := 200
	if o.action == "stage" {
		expected = 201
	}
	if response.overflow || response.status != expected || !json.Valid(response.body.Bytes()) {
		return reply, brokersecret.ErrUnavailable
	}
	reply.Body = append(json.RawMessage(nil), response.body.Bytes()...)
	if o.action == "fence" {
		var status deploymentStatus
		if json.Unmarshal(reply.Body, &status) != nil {
			return reply, brokersecret.ErrUnavailable
		}
		wrapped, err := json.Marshal(struct {
			Contract  string                  `json:"contract"`
			Status    deploymentStatus        `json:"status"`
			Reference *brokersecret.Reference `json:"reference"`
		}{brokersecret.Version, status, history.brokerReference(status.RequestID)})
		if err != nil {
			return reply, brokersecret.ErrUnavailable
		}
		reply.Body = wrapped
	}
	return reply, nil
}
