package preflight

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"unicode/utf8"
)

var integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// CanonicalJSON recursively sorts object keys, preserves explicit array order,
// permits only integer JSON numbers, and uses encoding/json's UTF8/HTML escaping.
// Inputs must be finite typed private projections, never unfiltered SDK Settings.
func CanonicalJSON(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var normalized any
	if err = decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	if err = integerJSON(normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}
func integerJSON(value any) error {
	switch v := value.(type) {
	case json.Number:
		if !integerPattern.MatchString(string(v)) {
			return errors.New("non-integer canonical number")
		}
	case []any:
		for _, item := range v {
			if err := integerJSON(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range v {
			if err := integerJSON(item); err != nil {
				return err
			}
		}
	}
	return nil
}
func digest(domain string, value any) (string, error) {
	b, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = io.WriteString(h, domain)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(b)
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// StoreScope is PRIVATE. It binds the retained directory/key identity, never
// changing lock/journal metadata or path spelling. Do not publish this token.
func StoreScope(key []byte, device, inode, euid uint64) (string, error) {
	if len(key) != 32 {
		return "", errors.New("invalid private store identity")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, "dill.named-store/1")
	_, _ = mac.Write([]byte{0})
	var encoded [24]byte
	binary.BigEndian.PutUint64(encoded[0:8], device)
	binary.BigEndian.PutUint64(encoded[8:16], inode)
	binary.BigEndian.PutUint64(encoded[16:24], euid)
	_, _ = mac.Write(encoded[:])
	return hex.EncodeToString(mac.Sum(nil)), nil
}

type EffectiveFlags struct {
	TimeoutNanos        int64  `json:"timeoutNanos"`
	ForceRecreate       bool   `json:"forceRecreate"`
	NoPrune             bool   `json:"noPrune"`
	EffectivePullPolicy string `json:"effectivePullPolicy"`
	NoDeps              bool   `json:"noDeps"`
	CallerSource        string `json:"callerSource"`
}
type ServiceInput struct {
	Name          string `json:"name"`
	ContainerName string `json:"containerName"`
	// DesiredConfigHash MUST retain the original desired image provenance;
	// PreparedImageID is a separate observation, not a replacement hash input.
	DesiredConfigHash   string   `json:"desiredConfigHash"`
	DependsOn           []string `json:"dependsOn"`
	EffectivePullPolicy string   `json:"effectivePullPolicy"`
	PreparedImageID     string   `json:"preparedImageId"`
}
type Input struct {
	LogicalContract string         `json:"logicalContract"`
	Engine          string         `json:"engine"`
	Stack           string         `json:"stack"`
	Operation       string         `json:"operation"`
	Flags           EffectiveFlags `json:"flags"`
	Services        []ServiceInput `json:"services"`
}

func (input Input) normalized() (Input, error) {
	if input.LogicalContract != Version || input.Engine != "docker" || !ValidName(input.Stack) || !SupportedOperation(input.Operation) || input.Flags.TimeoutNanos <= 0 || input.Flags.NoDeps || input.Flags.EffectivePullPolicy != "never" || (input.Flags.CallerSource != "cli" && input.Flags.CallerSource != "brine") {
		return Input{}, errors.New("unsupported effective input")
	}
	if input.Operation != "up" && (input.Flags.ForceRecreate || input.Flags.NoPrune) {
		return Input{}, errors.New("unsupported effective action flags")
	}
	if len(input.Services) >= MaxResources {
		return Input{}, errors.New("effective input resource limit")
	}
	input.Services = append([]ServiceInput{}, input.Services...)
	sort.Slice(input.Services, func(i, j int) bool { return input.Services[i].Name < input.Services[j].Name })
	seen := map[string]bool{}
	for i := range input.Services {
		s := &input.Services[i]
		if s.Name == "" || len(s.Name) > 1024 || !utf8.ValidString(s.Name) || !ValidName(s.ContainerName) || !idPattern.MatchString(s.DesiredConfigHash) || !ValidDigest(s.PreparedImageID) || s.EffectivePullPolicy != "never" || seen[s.Name] {
			return Input{}, errors.New("invalid effective service input")
		}
		seen[s.Name] = true
		s.DependsOn = append([]string{}, s.DependsOn...)
		sort.Strings(s.DependsOn)
		unique := s.DependsOn[:0]
		for _, dep := range s.DependsOn {
			if len(unique) == 0 || unique[len(unique)-1] != dep {
				unique = append(unique, dep)
			}
		}
		s.DependsOn = unique
	}
	for _, s := range input.Services {
		for _, dep := range s.DependsOn {
			if !seen[dep] {
				return Input{}, errors.New("unknown effective dependency")
			}
		}
	}
	return input, nil
}
func (input Input) Digest() (string, error) {
	normalized, err := input.normalized()
	if err != nil {
		return "", err
	}
	return digest("dill.effective-input/1", normalized)
}

type ContainerFact struct {
	Name         string  `json:"name"`
	Exists       bool    `json:"exists"`
	ID           *string `json:"id"`
	ConfigDigest *string `json:"configDigest"`
}
type NetworkFact struct {
	Name           string  `json:"name"`
	Exists         bool    `json:"exists"`
	ID             *string `json:"id"`
	SettingsDigest *string `json:"settingsDigest"`
}
type VolumeFact struct {
	Name           string  `json:"name"`
	Exists         bool    `json:"exists"`
	SettingsDigest *string `json:"settingsDigest"`
}
type Facts struct {
	Containers []ContainerFact `json:"containers"`
	Network    NetworkFact     `json:"network"`
	Volumes    []VolumeFact    `json:"volumes"`
}
type Selection struct {
	LogicalContract string     `json:"logicalContract"`
	Target          Target     `json:"target"`
	Stack           string     `json:"stack"`
	Operation       string     `json:"operation"`
	InputDigest     string     `json:"inputDigest"`
	StateScope      string     `json:"stateScope"`
	Coverage        Coverage   `json:"coverage"`
	Resources       []Resource `json:"resources"`
	Facts           Facts      `json:"facts"`
}

func (s Selection) Digest() (string, error) {
	if s.LogicalContract != Version || !s.Target.Valid() || !ValidName(s.Stack) || !SupportedOperation(s.Operation) || !ValidDigest(s.InputDigest) || !idPattern.MatchString(s.StateScope) {
		return "", errors.New("invalid selection scope")
	}
	s.Resources = append([]Resource{}, s.Resources...)
	sort.Slice(s.Resources, func(i, j int) bool {
		if s.Resources[i].Kind != s.Resources[j].Kind {
			return s.Resources[i].Kind < s.Resources[j].Kind
		}
		return s.Resources[i].Name < s.Resources[j].Name
	})
	for _, r := range s.Resources {
		if err := r.validate(s.Operation, s.Stack); err != nil {
			return "", err
		}
	}
	coverage, err := CoverageFor(s.Resources)
	if err != nil {
		return "", err
	}
	if coverage != s.Coverage || coverage.Network.Count != 1 {
		return "", errors.New("incomplete selection coverage")
	}
	s.Facts.Containers = append([]ContainerFact{}, s.Facts.Containers...)
	s.Facts.Volumes = append([]VolumeFact{}, s.Facts.Volumes...)
	sort.Slice(s.Facts.Containers, func(i, j int) bool { return s.Facts.Containers[i].Name < s.Facts.Containers[j].Name })
	sort.Slice(s.Facts.Volumes, func(i, j int) bool { return s.Facts.Volumes[i].Name < s.Facts.Volumes[j].Name })
	if err := s.validateFacts(); err != nil {
		return "", err
	}
	return digest("dill.named-selection/1", s)
}
func optionalDigest(d *string) bool { return d != nil && ValidDigest(*d) }
func (s Selection) validateFacts() error {
	if len(s.Facts.Containers) != s.Coverage.Containers.Count || len(s.Facts.Volumes) != s.Coverage.Volumes.Count {
		return errors.New("incomplete private facts")
	}
	resources := map[string]Resource{}
	for _, r := range s.Resources {
		resources[r.Kind+"\x00"+r.Name] = r
	}
	seen := map[string]bool{}
	for _, f := range s.Facts.Containers {
		key := "container\x00" + f.Name
		r, ok := resources[key]
		if !ok || seen[key] || f.Exists != (r.Presence == "present") {
			return errors.New("mismatched container fact")
		}
		seen[key] = true
		if f.Exists {
			if f.ID == nil || *f.ID != r.Identity || !optionalDigest(f.ConfigDigest) {
				return errors.New("unavailable container fact")
			}
		} else if f.ID != nil || f.ConfigDigest != nil {
			return errors.New("invented absent container fact")
		}
	}
	n := s.Facts.Network
	r, ok := resources["network\x00"+n.Name]
	if !ok || n.Exists != (r.Presence == "present") {
		return errors.New("mismatched network fact")
	}
	if n.Exists {
		if n.ID == nil || *n.ID != r.Identity || !optionalDigest(n.SettingsDigest) {
			return errors.New("unavailable network fact")
		}
	} else if n.ID != nil || n.SettingsDigest != nil {
		return errors.New("invented absent network fact")
	}
	for _, f := range s.Facts.Volumes {
		key := "volume\x00" + f.Name
		r, ok := resources[key]
		if !ok || seen[key] || f.Exists != (r.Presence == "present") {
			return errors.New("mismatched volume fact")
		}
		seen[key] = true
		if f.Exists {
			if !optionalDigest(f.SettingsDigest) {
				return errors.New("unavailable volume fact")
			}
		} else if f.SettingsDigest != nil {
			return errors.New("invented absent volume fact")
		}
	}
	return nil
}
