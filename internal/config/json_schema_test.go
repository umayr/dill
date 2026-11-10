package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func validateJSONFixture(raw string) error {
	var cfg DillConfig
	if err := decodeStrict([]byte(raw), &cfg); err != nil {
		return err
	}
	if err := validateDecodedShape([]byte(raw), &cfg); err != nil {
		return err
	}
	return ValidateRuntime(&cfg)
}

func TestCurrentSchemaJSONBoundaries(t *testing.T) {
	tests := []struct{ name, fields, want string }{
		{"command null element", `"command":[null]`, `command[0]`},
		{"dependsOn null element", `"dependsOn":[null]`, `dependsOn[0]`},
		{"capAdd null element", `"capAdd":[null]`, `capAdd[0]`},
		{"devices null element", `"devices":[null]`, `devices[0]`},
		{"environment null value", `"environment":{"X":null}`, `environment["X"]`},
		{"labels null value", `"labels":{"X":null}`, `labels["X"]`},
		{"port null element", `"ports":[null]`, `ports[0]`},
		{"mount null element", `"volumes":[null]`, `volumes[0]`},
		{"probe null element", `"healthcheck":{"test":["CMD",null]}`, `healthcheck test`},
		{"image null", `"image":null`, `.image`},
		{"restart null", `"restart":null`, `.restart`},
		{"pull null", `"pullPolicy":null`, `.pullPolicy`},
		{"port target missing", `"ports":[{}]`, `target`},
		{"port target null", `"ports":[{"target":null}]`, `target`},
		{"port protocol null", `"ports":[{"target":80,"protocol":null}]`, `protocol`},
		{"mount target missing", `"volumes":[{}]`, `target`},
		{"mount target null", `"volumes":[{"target":null}]`, `target`},
		{"mount type null", `"volumes":[{"target":"/x","type":null}]`, `type`},
		{"mount readonly null", `"volumes":[{"target":"/x","readOnly":null}]`, `readOnly`},
		{"mount external null", `"volumes":[{"target":"/x","externalVolume":null}]`, `externalVolume`},
		{"probe missing", `"healthcheck":{}`, `healthcheck.test`},
		{"probe null", `"healthcheck":{"test":null}`, `.healthcheck.test`},
		{"duration missing value", `"healthcheck":{"test":"x","interval":{"unit":"s"}}`, `value`},
		{"duration missing unit", `"healthcheck":{"test":"x","interval":{"value":1}}`, `unit`},
		{"duration null value", `"healthcheck":{"test":"x","interval":{"value":null,"unit":"s"}}`, `value`},
		{"duration null unit", `"healthcheck":{"test":"x","interval":{"value":1,"unit":null}}`, `unit`},
		{"wrong element type", `"command":[3]`, `command[0]`},
		{"optional nullable", `"command":null,"environment":null,"healthcheck":null,"containerName":null`, ""},
		{"nested optional nullable", `"ports":[{"target":80,"published":null,"hostIp":null}],"volumes":[{"target":"/x","source":null,"name":null}],"healthcheck":{"test":["CMD","true"],"interval":null,"retries":null}`, ""},
		{"omitted defaults", `"ports":[{"target":80}],"volumes":[{"target":"/x"}]`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image := `"image":"x",`
			if strings.HasPrefix(tt.fields, `"image":`) {
				image = ""
			}
			raw := fmt.Sprintf(`{"services":{"app":{%s%s}}}`, image, tt.fields)
			err := validateJSONFixture(raw)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v; want %q", err, tt.want)
			}
		})
	}
	for _, tc := range []struct{ raw, want string }{
		{`{"services":null}`, `services`}, {`{"engine":null,"services":{}}`, `engine`},
		{`{"services":{"app":null}}`, `app`},
		{`{"services":{"z":{"image":null},"a":{"image":null}}}`, `["a"].image`},
	} {
		if err := validateJSONFixture(tc.raw); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.raw, err)
		}
	}
}

func TestDurationJSONRange(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"value":-1,"unit":"ns"}`, false}, {`{"value":0,"unit":"ns"}`, true},
		{`{"value":9223372036854774784,"unit":"ns"}`, true},
		{`{"value":9223372036854775807,"unit":"ns"}`, true},
		{`{"value":9223372036854775808,"unit":"ns"}`, false},
		{`{"value":1e30,"unit":"h"}`, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			raw := json.RawMessage(tc.raw)
			_, err := ParsePklDuration(&raw)
			if (err == nil) != tc.valid {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
