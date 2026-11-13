package preflight

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"slices"

	"github.com/umayr/dill/internal/orchestrator"
)

// These are finite PRIVATE leaves, not generic inventory or public Settings.
// The v1 qualifier supports native Docker only. Unknown adapter metadata must
// fail; a Podman report is not silently interpreted as absent Docker fields.
type subnetLeaf struct {
	Subnet       string            `json:"subnet"`
	IPRange      string            `json:"ipRange"`
	Gateway      string            `json:"gateway"`
	AuxAddresses map[string]string `json:"auxAddresses"`
}
type networkLeaf struct {
	ActualName  string            `json:"actualName"`
	ID          string            `json:"id"`
	Driver      string            `json:"driver"`
	Scope       string            `json:"scope"`
	Internal    bool              `json:"internal"`
	IPv6        bool              `json:"ipv6"`
	Attachable  bool              `json:"attachable"`
	Ingress     bool              `json:"ingress"`
	IPAMDriver  string            `json:"ipamDriver"`
	IPAMOptions map[string]string `json:"ipamOptions"`
	Subnets     []subnetLeaf      `json:"subnets"`
	Options     map[string]string `json:"options"`
	Labels      map[string]string `json:"labels"`
}
type volumeLeaf struct {
	ActualName string            `json:"actualName"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope"`
	Options    map[string]string `json:"options"`
	Labels     map[string]string `json:"labels"`
}

func nonnullMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	return maps.Clone(in)
}

// decodeKnownSettings accepts only the exact private SDK projection's keys.
// Presence of a known-null optional map differs from missing capability data.
// No SDK diagnostic or raw Settings can escape via an error.
func decodeKnownSettings(raw []byte, required, optional []string, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ErrEvidenceUnavailable
	}
	allowed := map[string]bool{}
	for _, key := range append(append([]string{}, required...), optional...) {
		allowed[key] = true
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return ErrEvidenceUnavailable
		}
		name, ok := key.(string)
		if !ok || !allowed[name] || seen[name] {
			return ErrEvidenceUnavailable
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return ErrEvidenceUnavailable
		}
	}
	if _, err = d.Token(); err != nil {
		return ErrEvidenceUnavailable
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return ErrEvidenceUnavailable
	}
	for _, key := range required {
		if !seen[key] {
			return ErrEvidenceUnavailable
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrEvidenceUnavailable
	}
	return nil
}
func ownershipLabels(stack string, labels map[string]string) bool {
	return labels["dill.managed"] == "true" && labels["dill.stack"] == stack
}

// NetworkDigest commits checked actual identity and stable settings only. It
// excludes endpoint attachments, metrics and timestamps. It never substitutes
// a lookup name or treats absent/unknown evidence as a present network.
func NetworkDigest(stack string, fact orchestrator.PreviewResource) (string, error) {
	if fact.Kind != "network" || !fact.Exists || !ValidName(stack) || fact.Name != stack || fact.ActualName != stack || !ValidID(fact.ID) {
		return "", ErrEvidenceUnavailable
	}
	var source struct {
		Driver, Scope                       *string
		Internal, IPv6, Attachable, Ingress *bool
		IPAM                                *struct {
			Driver  *string
			Options map[string]string
			Config  []struct {
				Subnet, IPRange, Gateway string
				AuxAddresses             map[string]string `json:"AuxiliaryAddresses"`
			}
		}
		Options, Labels map[string]string
	}
	if decodeKnownSettings(fact.Settings, []string{"Driver", "Scope", "Internal", "IPv6", "Attachable", "Ingress", "IPAM", "Options", "Labels"}, nil, &source) != nil {
		return "", ErrEvidenceUnavailable
	}
	if source.Driver == nil || *source.Driver != "bridge" || source.Scope == nil || source.Internal == nil || source.IPv6 == nil || source.Attachable == nil || source.Ingress == nil || source.IPAM == nil || source.IPAM.Driver == nil || *source.IPAM.Driver == "" || len(source.Options) != 0 || !ownershipLabels(stack, source.Labels) {
		return "", ErrEvidenceUnavailable
	}
	n := networkLeaf{ActualName: fact.ActualName, ID: fact.ID, Driver: *source.Driver, Scope: *source.Scope, Internal: *source.Internal, IPv6: *source.IPv6, Attachable: *source.Attachable, Ingress: *source.Ingress, IPAMDriver: *source.IPAM.Driver, IPAMOptions: nonnullMap(source.IPAM.Options), Subnets: []subnetLeaf{}, Options: nonnullMap(source.Options), Labels: nonnullMap(source.Labels)}
	for _, subnet := range source.IPAM.Config {
		n.Subnets = append(n.Subnets, subnetLeaf{Subnet: subnet.Subnet, IPRange: subnet.IPRange, Gateway: subnet.Gateway, AuxAddresses: nonnullMap(subnet.AuxAddresses)})
	}
	slices.SortFunc(n.Subnets, func(a, b subnetLeaf) int {
		left, _ := CanonicalJSON(a)
		right, _ := CanonicalJSON(b)
		return bytes.Compare(left, right)
	})
	return digest("dill.named-network/1", n)
}

// VolumeDigest binds a checked local engine NAME/configuration, not immutable
// incarnation or contents. CreatedAt/Mountpoint are deliberately NOT committed.
func VolumeDigest(stack string, fact orchestrator.PreviewResource) (string, error) {
	if fact.Kind != "volume" || !fact.Exists || !ValidName(stack) || !ValidName(fact.Name) || fact.ActualName != fact.Name {
		return "", ErrEvidenceUnavailable
	}
	var source struct {
		Name, Driver, Scope   *string
		CreatedAt, Mountpoint string
		Options, Labels       map[string]string
	}
	if decodeKnownSettings(fact.Settings, []string{"Name", "Driver", "Scope", "Options", "Labels"}, []string{"CreatedAt", "Mountpoint"}, &source) != nil {
		return "", ErrEvidenceUnavailable
	}
	if source.Name == nil || *source.Name != fact.ActualName || source.Driver == nil || *source.Driver != "local" || source.Scope == nil || len(source.Options) != 0 || !ownershipLabels(stack, source.Labels) {
		return "", ErrEvidenceUnavailable
	}
	return digest("dill.named-volume/1", volumeLeaf{ActualName: fact.ActualName, Driver: *source.Driver, Scope: *source.Scope, Options: nonnullMap(source.Options), Labels: nonnullMap(source.Labels)})
}
