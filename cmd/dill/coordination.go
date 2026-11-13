package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"time"

	"github.com/umayr/dill/internal/coordination"
)

// runCoordination has no evaluator, engine, socket, replay or repair path.
// Acknowledgement is an explicit operator inspection decision, not rollback.
func runCoordination(ctx context.Context, args []string) error {
	emit := func(code int, record *coordination.Record, message string) error {
		result := struct {
			Version string               `json:"version"`
			Record  *coordination.Record `json:"record"`
			Error   string               `json:"error"`
		}{coordination.JournalVersion, record, message}
		if json.NewEncoder(os.Stdout).Encode(result) != nil {
			return protocolExit{1}
		}
		if code != 0 {
			return protocolExit{code}
		}
		return nil
	}
	if len(args) == 0 || (args[0] != "inspect" && args[0] != "acknowledge") {
		return emit(2, nil, "Unsupported coordination command.")
	}
	action := args[0]
	fs := flag.NewFlagSet("coordination", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("state-dir", "", "shared private coordination directory")
	target := fs.String("target-id", "", "recorded actual-daemon fingerprint")
	daemon := fs.String("daemon-id", "", "independently known daemon ID (offline fingerprint derivation)")
	stack := fs.String("stack", "", "exact resolved stack label")
	kind := fs.String("resource-kind", "", "resource namespace for resource inspection")
	name := fs.String("resource-name", "", "exact engine resource name")
	id := fs.String("operation-id", "", "exact inspected operation ID")
	inspected := fs.Bool("inspected", false, "explicitly attest target/resource effects were inspected")
	timeout := fs.Duration("timeout", 5*time.Second, "bounded local lock wait")
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || *timeout <= 0 || (*target == "") == (*daemon == "") || (*stack == "") == (*kind == "" && *name == "") || (*stack != "" && (*kind != "" || *name != "")) || (action == "acknowledge" && (!*inspected || *id == "")) || (action == "inspect" && (*inspected || *id != "")) {
		return emit(2, nil, "Invalid coordination arguments.")
	}
	if *daemon != "" {
		fingerprint, err := coordination.DockerIdentity(*daemon)
		if err != nil {
			return emit(2, nil, "Invalid coordination target.")
		}
		*target = fingerprint
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	state, err := coordination.Open(ctx, *dir)
	if err != nil {
		return emit(1, nil, "Trusted coordination state unavailable.")
	}
	defer state.Close()
	var record *coordination.Record
	if action == "inspect" {
		if *stack != "" {
			record, err = state.Inspect(ctx, *target, *stack)
		} else {
			record, err = state.InspectResource(ctx, *target, *kind, *name)
		}
	} else {
		if *stack != "" {
			err = state.Acknowledge(ctx, *target, *stack, *id)
		} else {
			err = state.AcknowledgeResource(ctx, *target, *kind, *name, *id)
		}
		if err == nil {
			if *stack != "" {
				record, err = state.Inspect(ctx, *target, *stack)
			} else {
				record, err = state.InspectResource(ctx, *target, *kind, *name)
			}
		}
	}
	if err != nil {
		return emit(1, nil, "Coordination inspection or acknowledgement failed; no replay or repair performed.")
	}
	return emit(0, record, "")
}
