package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateStdinReportsInputSource(t *testing.T) {
	dir := t.TempDir()
	anchor := filepath.Join(dir, "compose.pkl") // deliberately does not exist
	if err := os.WriteFile(filepath.Join(dir, "input.pkl"), []byte(`payload = #"{"config":{"services":{}}}"#`), 0600); err != nil {
		t.Fatal(err)
	}
	const input = "import \"input.pkl\"\noutput { text = input.payload }\n"
	for _, tc := range []struct{ format, want string }{
		{"text", "stdin is valid\n"},
		{"json", "{\"valid\":true}\n"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			out, _, err := captureCommand(t, input, func() error {
				return run([]string{"-f", anchor, "validate", "--stdin", "--format", tc.format})
			})
			if err != nil || out != tc.want {
				t.Fatalf("got output %q and error %v; want %q and success", out, err, tc.want)
			}
		})
	}
}
