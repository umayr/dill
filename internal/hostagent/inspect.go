package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// InspectBackend returns a fresh exact-ID inspection, with the normalized
// profile and labels obtained together. It must not include environment values.
type InspectBackend interface {
	Inspect(context.Context, string) (InspectResult, error)
}
type InspectResult struct {
	Labels  map[string]string
	Profile InspectProfile
}
type InspectPort struct {
	Protocol      string `json:"protocol"`
	HostIP        string `json:"hostIp"`
	HostPort      uint16 `json:"hostPort"`
	ContainerPort uint16 `json:"containerPort"`
}
type InspectMount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly"`
}
type InspectHealth struct {
	Status *string  `json:"status"`
	Test   []string `json:"test"`
}
type InspectResources struct {
	MemoryBytes *int64 `json:"memoryBytes"`
	CPUNano     *int64 `json:"cpuNano"`
	PidsLimit   *int64 `json:"pidsLimit"`
}
type InspectSecurity struct {
	Privileged  *bool    `json:"privileged"`
	CapAdd      []string `json:"capAdd"`
	NetworkMode *string  `json:"networkMode"`
	PIDMode     *string  `json:"pidMode"`
	IPCMode     *string  `json:"ipcMode"`
}
type InspectProfile struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	ImageID       string            `json:"imageId"`
	State         string            `json:"state"`
	CreatedAt     *string           `json:"createdAt"`
	Command       []string          `json:"command"`
	User          *string           `json:"user"`
	EnvKeys       []string          `json:"envKeys"`
	Labels        map[string]string `json:"labels"`
	Ports         []InspectPort     `json:"ports"`
	Mounts        []InspectMount    `json:"mounts"`
	Networks      []string          `json:"networks"`
	RestartPolicy *string           `json:"restartPolicy"`
	Health        InspectHealth     `json:"health"`
	Resources     InspectResources  `json:"resources"`
	Security      InspectSecurity   `json:"security"`
}

// validProfile enforces collection, string and integer bounds recursively. Nil
// pointers are allowed for unavailable values, but arrays/maps must be present.
func validProfile(v reflect.Value) bool {
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return true
		}
		return validProfile(v.Elem())
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return true
		}
		return validProfile(v.Elem())
	case reflect.String:
		s := v.String()
		return len(s) <= 8192 && utf8.ValidString(s)
	case reflect.Slice, reflect.Map:
		if v.IsNil() || v.Len() > 256 {
			return false
		}
		if v.Kind() == reflect.Map {
			it := v.MapRange()
			for it.Next() {
				if !validProfile(it.Key()) || !validProfile(it.Value()) {
					return false
				}
			}
		} else {
			for i := 0; i < v.Len(); i++ {
				if !validProfile(v.Index(i)) {
					return false
				}
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !validProfile(v.Field(i)) {
				return false
			}
		}
	case reflect.Int, reflect.Int64:
		if v.Int() < 0 {
			return false
		}
	}
	return true
}
func (s *Server) inspect(w http.ResponseWriter, r *http.Request, id string, body []byte) {
	var req struct {
		ContainerID string `json:"containerId"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if !exactObject(body, "containerId") || d.Decode(&req) != nil || !fullID.MatchString(req.ContainerID) {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	daemon, e := s.c.Identity.DaemonID(ctx)
	if e != nil || daemon == "" {
		inventoryError(w, id, ctx)
		return
	}
	if daemon != s.c.DaemonID {
		fail(w, id, 409, "BINDING_CHANGED", "Binding changed.", false)
		return
	}
	result, e := s.c.InspectBackend.Inspect(ctx, req.ContainerID)
	if e == ErrNotFound {
		fail(w, id, 404, "NOT_FOUND", "Not found.", false)
		return
	}
	if e != nil {
		inventoryError(w, id, ctx)
		return
	}
	p := result.Profile
	labels := result.Labels
	if p.ID != req.ContainerID || labels["dill.managed"] != "true" || labels["dill.stack"] == "" || labels["dill.service"] == "" {
		fail(w, id, 404, "NOT_FOUND", "Not found.", false)
		return
	}
	// Only these three labels are allowed in the profile, regardless of backend.
	p.Labels = map[string]string{"dill.managed": "true", "dill.stack": labels["dill.stack"], "dill.service": labels["dill.service"]}
	if !validProfile(reflect.ValueOf(p)) || !sort.StringsAreSorted(p.EnvKeys) {
		fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
		return
	}
	for i, k := range p.EnvKeys {
		if k == "" || strings.Contains(k, "=") || (i > 0 && p.EnvKeys[i-1] == k) {
			fail(w, id, 500, "INTERNAL", "Internal error.", false)
			return
		}
	}
	data := map[string]any{"containerId": p.ID, "stack": labels["dill.stack"], "service": labels["dill.service"], "observedAt": time.Now().UTC().Format(time.RFC3339Nano), "inspect": p}
	b, e := json.Marshal(envelope{Version: Version, RequestID: id, Data: data})
	if e != nil || len(b)+1 > 512<<10 {
		fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
		return
	}
	if ctx.Err() != nil {
		inventoryError(w, id, ctx)
		return
	}
	reply(w, id, 200, data, "", "", false)
}
