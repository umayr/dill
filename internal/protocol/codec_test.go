package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedFixtures(t *testing.T) {
	var manifest struct {
		BootstrapVersion, ProtocolVersion string
		Fixtures                          []struct {
			Name, File, Invocation string
			Request                json.RawMessage
			ExpectedExitCode       *int
			Synthetic              bool
		}
	}
	data, err := os.ReadFile("testdata/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.BootstrapVersion != BootstrapVersion || manifest.ProtocolVersion != Version {
		t.Fatal("manifest versions")
	}
	seen := map[string]bool{}
	for _, f := range manifest.Fixtures {
		t.Run(f.Name, func(t *testing.T) {
			if seen[f.File] {
				t.Fatal("duplicate fixture")
			}
			seen[f.File] = true
			b, err := os.ReadFile(filepath.Join("testdata", f.File))
			if err != nil {
				t.Fatal(err)
			}
			records, err := DecodeStream(b)
			if err != nil {
				t.Fatal(err)
			}
			var encoded []byte
			for _, r := range records {
				part, err := Encode(r)
				if err != nil {
					t.Fatal(err)
				}
				encoded = append(encoded, part...)
			}
			if !bytes.Equal(b, encoded) {
				t.Fatalf("canonical fixture mismatch: %s", encoded)
			}
			if f.Synthetic {
				if f.ExpectedExitCode != nil || f.Invocation != "" {
					t.Fatal("synthetic fixture claims runtime")
				}
				return
			}
			if f.ExpectedExitCode == nil {
				t.Fatal("missing production exit")
			}
			mode := DiscoveryMode
			if f.Invocation == "run" {
				mode = CheckMode
			}
			m := &memoryTransport{input: f.Request}
			exit := serveTest(context.Background(), mode, m, fixedID)
			if exit != *f.ExpectedExitCode || !bytes.Equal(m.out.Bytes(), b) {
				t.Fatalf("exit %d fixture output: %s", exit, m.out.String())
			}
		})
	}
	files, err := filepath.Glob("testdata/*.ndjson")
	if err != nil || len(files) != len(seen) {
		t.Fatal("unmanifested fixtures", err)
	}
}
func TestCodecStrictRejections(t *testing.T) {
	good, _ := os.ReadFile("testdata/check-success.ndjson")
	reject, _ := os.ReadFile("testdata/validation-rejection.ndjson")
	for name, b := range map[string][]byte{
		"incomplete":            good[:len(good)-1],
		"accepted only":         bytes.SplitAfter(good, []byte{'\n'})[0],
		"duplicate final":       append(append([]byte{}, good...), bytes.SplitAfter(good, []byte{'\n'})[1]...),
		"changed ID":            []byte(strings.Replace(string(good), fixtureID, "op_11111111111111111111111111111111", 1)),
		"gap":                   bytes.Replace(good, []byte(`"sequence":2`), []byte(`"sequence":3`), 1),
		"case":                  bytes.Replace(good, []byte(`"sequence":1`), []byte(`"Sequence":1`), 1),
		"unknown":               bytes.Replace(good, []byte(`"sequence":1`), []byte(`"sequence":1,"SENTINEL":true`), 1),
		"duplicate":             bytes.Replace(good, []byte(`"sequence":1`), []byte(`"sequence":1,"sequence":1`), 1),
		"null errors":           bytes.Replace(good, []byte(`"errors":[]`), []byte(`"errors":null`), 1),
		"unknown error field":   bytes.Replace(reject, []byte(`"code":`), []byte(`"SENTINEL":0,"code":`), 1),
		"wrong error case":      bytes.Replace(reject, []byte(`"code":`), []byte(`"Code":`), 1),
		"raw error":             bytes.Replace(reject, []byte(`Invalid protocol request.`), []byte(`SENTINEL`), 1),
		"rejected after accept": bytes.Replace(good, []byte(`"outcome":"succeeded"`), []byte(`"outcome":"rejected"`), 1),
		"oversize":              bytes.Repeat([]byte{' '}, MaxOutput+1),
		"blank record":          append(append([]byte{}, good...), '\n'),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeStream(b); err == nil {
				t.Fatal("accepted malformed stream")
			}
		})
	}
	bad := final(fixtureID, InternalError)
	bad.Errors[0].Message = "SENTINEL"
	if b, err := Encode(bad); err == nil || b != nil {
		t.Fatal("encoder leaked invalid error")
	}
}
func TestNewID(t *testing.T) {
	a, code := newID(context.Background())
	if code == UnsupportedTransport {
		t.Skip("platform has no supported transport")
	}
	if code != "" || !validHeader(Version, a, Operation, 1) {
		t.Fatalf("id generation: %s", code)
	}
	b, code := newID(context.Background())
	if code != "" || a == b {
		t.Fatal("reused id")
	}
}
