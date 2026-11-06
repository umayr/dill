package orchestrator

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckOwnership(t *testing.T) {
	for _, tc := range []struct {
		name    string
		live    *LiveConfig
		service string
		wantErr bool
	}{
		{"nil config", nil, "web", true},
		{"unmanaged", &LiveConfig{}, "web", true},
		{"missing managed", &LiveConfig{SystemLabels: map[string]string{"dill.stack": "app", "dill.service": "web"}}, "web", true},
		{"managed false", &LiveConfig{SystemLabels: map[string]string{"dill.managed": "false", "dill.stack": "app", "dill.service": "web"}}, "web", true},
		{"missing stack", &LiveConfig{SystemLabels: map[string]string{"dill.managed": "true", "dill.service": "web"}}, "web", true},
		{"other stack", &LiveConfig{SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "other", "dill.service": "web"}}, "web", true},
		{"missing service", &LiveConfig{SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app"}}, "web", true},
		{"other service", &LiveConfig{SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "db"}}, "web", true},
		{"empty expected service", &LiveConfig{SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app"}}, "", true},
		{"owned", &LiveConfig{SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}, "web", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckOwnership("shared-web", "app", tc.service, tc.live)
			if errors.Is(err, ErrOwnershipConflict) != tc.wantErr {
				t.Fatalf("CheckOwnership = %v, want conflict %v", err, tc.wantErr)
			}
			if tc.wantErr && (!strings.Contains(err.Error(), "shared-web") || !strings.Contains(err.Error(), "choose a different container name")) {
				t.Fatalf("error lacks container name or remediation: %v", err)
			}
		})
	}
}
