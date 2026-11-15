package plan

import (
	"reflect"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestNormalizeLiveForSelectionCopiesCompleteFacts(t *testing.T) {
	v := &orchestrator.LiveConfig{Env: map[string]string{"SYNTHETIC": "original"}, SystemLabels: map[string]string{"dill.stack": "synthetic"}, UserLabels: map[string]string{"synthetic": "original"}, Ports: []config.PortBinding{{HostIP: "0.0.0.0", Published: "8000", Target: "80"}, {HostIP: "127.0.0.1", Published: "8001", Target: "80"}}, Volumes: []config.VolumeMount{{Type: "volume", Source: "b", Target: "/b"}, {Type: "volume", Source: "a", Target: "/a"}}, Networks: []string{"z", "a", "z"}, CapAdd: []string{"B", "A", "B"}, Command: []string{"z", "a"}, HealthTest: []string{"CMD", "z", "a"}, UIDMap: []string{"z", "a"}}
	n := NormalizeLiveForSelection(v)
	if n.RestartPolicy != "no" || n.NetworkMode != "" || n.Ports[0].HostIP != "0.0.0.0" || n.Ports[0].Protocol != "tcp" || len(n.Ports) != 2 || len(n.Volumes) != 2 {
		t.Fatal("normalized facts incomplete", n)
	}
	if !reflect.DeepEqual(n.Networks, []string{"a", "z"}) || !reflect.DeepEqual(n.CapAdd, []string{"A", "B"}) || !reflect.DeepEqual(n.Command, v.Command) || !reflect.DeepEqual(n.HealthTest, v.HealthTest) || !reflect.DeepEqual(n.UIDMap, v.UIDMap) {
		t.Fatal("set or ordered-vector semantics changed")
	}
	n.Env["SYNTHETIC"] = "changed"
	n.UserLabels["synthetic"] = "changed"
	n.SystemLabels["dill.stack"] = "changed"
	n.Ports[0].Published = "changed"
	n.Volumes[0].Source = "changed"
	n.Command[0] = "changed"
	n.HealthTest[0] = "changed"
	n.UIDMap[0] = "changed"
	if v.Env["SYNTHETIC"] != "original" || v.UserLabels["synthetic"] != "original" || v.SystemLabels["dill.stack"] != "synthetic" || v.Ports[0].Published != "8000" || v.Volumes[1].Source != "a" || v.Command[0] != "z" || v.HealthTest[0] != "CMD" || v.UIDMap[0] != "z" {
		t.Fatal("normalization aliased inspected mutable fields")
	}
	if NormalizeLiveForSelection(nil) != nil {
		t.Fatal("unknown evidence invented")
	}
}
