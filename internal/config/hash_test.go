package config

import (
	"encoding/json"
	"testing"
)

func volumeService(volume string) *Service {
	return &Service{
		Image:   "app:latest",
		BaseDir: "/tmp",
		Volumes: []json.RawMessage{json.RawMessage(volume)},
	}
}

func TestServiceConfigHash_stackScopesVolumes(t *testing.T) {
	svc := volumeService(`"data:/data"`)

	a, err := ServiceConfigHash("stack-a", "web", svc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ServiceConfigHash("stack-b", "web", svc)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("hash did not change with the stack name: %s", a)
	}
}

func TestServiceConfigHash_externalVolumeIsStable(t *testing.T) {
	svc := volumeService(`{"type":"volume","source":"shared","target":"/data","externalVolume":true}`)

	a, err := ServiceConfigHash("stack-a", "web", svc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ServiceConfigHash("stack-b", "web", svc)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("external volume hash should not depend on the stack name")
	}
}

func TestServiceConfigHash_explicitNameIsStable(t *testing.T) {
	svc := volumeService(`{"type":"volume","source":"data","target":"/data","name":"custom"}`)

	a, _ := ServiceConfigHash("stack-a", "web", svc)
	b, _ := ServiceConfigHash("stack-b", "web", svc)
	if a != b {
		t.Fatal("explicit volume name hash should not depend on the stack name")
	}
}
