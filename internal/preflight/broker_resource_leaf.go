package preflight

import (
	"net/netip"
	"path/filepath"
	"regexp"

	"github.com/umayr/dill/internal/orchestrator"
)

// BrokerResourceDigest never interprets Podman metadata as Docker settings.
// Only separately authorized broker producers/receipts may select this domain.
func BrokerResourceDigest(engine, stack string, fact orchestrator.PreviewResource) (string, error) {
	if engine == "docker" {
		if fact.Kind == "network" {
			return NetworkDigest(stack, fact)
		}
		if fact.Kind == "volume" {
			return VolumeDigest(stack, fact)
		}
	}
	if engine == "podman" {
		if fact.Kind == "network" {
			return brokerPodmanNetworkDigest(stack, fact)
		}
		if fact.Kind == "volume" {
			return brokerPodmanVolumeDigest(stack, fact)
		}
	}
	return "", ErrEvidenceUnavailable
}

type podmanSubnetLeaf struct {
	Subnet     string                           `json:"subnet"`
	Gateway    string                           `json:"gateway,omitempty"`
	LeaseRange *struct{ StartIP, EndIP string } `json:"lease_range,omitempty"`
}
type podmanNetworkLeaf struct {
	Name       *string            `json:"name"`
	ID         *string            `json:"id"`
	Driver     *string            `json:"driver"`
	Interface  *string            `json:"network_interface"`
	Created    string             `json:"created,omitempty"`
	Subnets    []podmanSubnetLeaf `json:"subnets"`
	IPv6       *bool              `json:"ipv6_enabled"`
	Internal   *bool              `json:"internal"`
	DNS        *bool              `json:"dns_enabled"`
	Labels     map[string]string  `json:"labels"`
	Options    map[string]string  `json:"options,omitempty"`
	IPAM       map[string]string  `json:"ipam_options"`
	Routes     []any              `json:"routes,omitempty"`
	DNSServers []string           `json:"network_dns_servers,omitempty"`
}

var podmanInterface = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)

func brokerPodmanNetworkDigest(stack string, fact orchestrator.PreviewResource) (string, error) {
	if fact.Kind != "network" || !fact.Exists || !ValidName(stack) || fact.Name != stack || fact.ActualName != stack || !ValidID(fact.ID) {
		return "", ErrEvidenceUnavailable
	}
	var n podmanNetworkLeaf
	if decodeKnownSettings(fact.Settings, []string{"name", "id", "driver", "network_interface", "subnets", "ipv6_enabled", "internal", "dns_enabled", "labels", "ipam_options"}, []string{"created", "options", "routes", "network_dns_servers"}, &n) != nil {
		return "", ErrEvidenceUnavailable
	}
	if n.Name == nil || *n.Name != stack || n.ID == nil || *n.ID != fact.ID || n.Driver == nil || *n.Driver != "bridge" || n.Interface == nil || !podmanInterface.MatchString(*n.Interface) || n.IPv6 == nil || n.Internal == nil || n.DNS == nil || !ownershipLabels(stack, n.Labels) || len(n.Options) != 0 || len(n.Routes) != 0 || len(n.DNSServers) != 0 || len(n.IPAM) != 1 || n.IPAM["driver"] != "host-local" || len(n.Subnets) == 0 || len(n.Subnets) > 8 {
		return "", ErrEvidenceUnavailable
	}
	for _, subnet := range n.Subnets {
		prefix, e := netip.ParsePrefix(subnet.Subnet)
		if e != nil || prefix != prefix.Masked() || subnet.LeaseRange != nil {
			return "", ErrEvidenceUnavailable
		}
		if subnet.Gateway != "" {
			gateway, e := netip.ParseAddr(subnet.Gateway)
			if e != nil || !prefix.Contains(gateway) {
				return "", ErrEvidenceUnavailable
			}
		}
	}
	// The adapter explicitly removes creation timestamps; endpoint attachments
	// are not network settings. Retain all remaining native fields unchanged.
	n.Created = ""
	return digest("dill.broker-podman-network/1", n)
}
func brokerPodmanVolumeDigest(stack string, fact orchestrator.PreviewResource) (string, error) {
	if fact.Kind != "volume" || !fact.Exists || !ValidName(stack) || !ValidName(fact.Name) || fact.ActualName != fact.Name {
		return "", ErrEvidenceUnavailable
	}
	var v struct {
		Name, Driver, CreatedAt, Mountpoint *string
		Options, Labels                     map[string]string
	}
	if decodeKnownSettings(fact.Settings, []string{"Name", "Driver", "CreatedAt", "Mountpoint", "Options", "Labels"}, nil, &v) != nil || v.Name == nil || *v.Name != fact.ActualName || v.Driver == nil || *v.Driver != "local" || v.CreatedAt == nil || v.Mountpoint == nil || !filepath.IsAbs(*v.Mountpoint) || filepath.Clean(*v.Mountpoint) != *v.Mountpoint || len(v.Options) != 0 || !ownershipLabels(stack, v.Labels) {
		return "", ErrEvidenceUnavailable
	}
	return digest("dill.broker-podman-volume/1", volumeLeaf{ActualName: fact.ActualName, Driver: *v.Driver, Options: nonnullMap(v.Options), Labels: nonnullMap(v.Labels)})
}
