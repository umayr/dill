package hostagent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

const monitorCandidateDomain = "brine-agent-monitor-response/1\x00"
const stockSessionDomain = "brine-agent-session/1\x00"

type monitorCandidateFields struct {
	path, requestID, agentID, engineID, epoch string
	generation                                uint64
}

// Independent test-only producer encoding from docs/design/brine-signed-monitor-aggregate-v1.md.
// Do not call stock Dill encoding helpers or derive any field from the golden preimage.
func monitorCandidatePreimage(f monitorCandidateFields, body []byte) []byte {
	appendString := func(out []byte, s string) []byte {
		return append(binary.BigEndian.AppendUint32(out, uint32(len(s))), s...)
	}
	out := []byte(monitorCandidateDomain)
	for _, s := range []string{"POST", f.path, f.requestID, f.agentID, f.engineID} {
		out = appendString(out, s)
	}
	out = binary.BigEndian.AppendUint64(out, f.generation)
	out = appendString(out, f.epoch)
	digest := sha256.Sum256(body)
	return append(out, digest[:]...)
}

func candidateGeneration(s string) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil && n != 0 && strconv.FormatUint(n, 10) == s
}

func katHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func katBase64URL(t *testing.T, s string, size int) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != size || base64.RawURLEncoding.EncodeToString(b) != s {
		t.Fatalf("invalid canonical base64url encoding for %d bytes", size)
	}
	return b
}

func TestMonitorCandidateKnownAnswers(t *testing.T) {
	// Unmodified public synthetic JSON from Brine commit
	// 400c4a316c3bf80938015787d156d6557a0afa20, internal/agentmetrics/testdata/aggregate-signing-candidate-kat.json.
	data, err := os.ReadFile("testdata/aggregate-signing-candidate-kat.json")
	if err != nil {
		t.Fatal(err)
	}
	const fixtureSHA256 = "ca2019477a750f0e0457ff321891dbbc4b67a751dbec7a704243dbf98f379784"
	if digest := sha256.Sum256(data); hex.EncodeToString(digest[:]) != fixtureSHA256 {
		t.Fatalf("fixture SHA-256 = %x; want %s", digest, fixtureSHA256)
	}
	var fixture struct {
		PublicKeyHex string `json:"publicKeyHex"`
		AgentID      string `json:"agentId"`
		EngineID     string `json:"engineId"`
		Epoch        string `json:"epoch"`
		Vectors      []struct {
			Case        string `json:"case"`
			Path        string `json:"path"`
			RequestID   string `json:"requestId"`
			Generation  string `json:"generation"`
			BodyHex     string `json:"bodyHex"`
			PreimageHex string `json:"preimageHex"`
			BodySHA256  string `json:"bodySha256"`
			Signature   string `json:"signature"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	public := ed25519.PublicKey(katHex(t, fixture.PublicKeyHex))
	if len(public) != ed25519.PublicKeySize {
		t.Fatal("bad fixture public key size")
	}
	if id := sha256.Sum256(public); hex.EncodeToString(id[:]) != fixture.AgentID {
		t.Fatalf("fixture agent ID = %s; key fingerprint = %x", fixture.AgentID, id)
	}
	expectedGeneration := map[string]uint64{"discover": 1, "measured-zero": 1<<53 + 1, "complete": 1<<64 - 1}
	if len(fixture.Vectors) != len(expectedGeneration) {
		t.Fatalf("got %d fixture vectors, want %d", len(fixture.Vectors), len(expectedGeneration))
	}
	for _, vector := range fixture.Vectors {
		t.Run(vector.Case, func(t *testing.T) {
			generation, ok := candidateGeneration(vector.Generation)
			if !ok || generation != expectedGeneration[vector.Case] {
				t.Fatalf("unexpected generation %q (%d)", vector.Generation, generation)
			}
			body := katHex(t, vector.BodyHex)
			if digest := sha256.Sum256(body); hex.EncodeToString(digest[:]) != vector.BodySHA256 {
				t.Fatalf("body SHA-256 = %x; want %s", digest, vector.BodySHA256)
			}
			fields := monitorCandidateFields{vector.Path, vector.RequestID, fixture.AgentID, fixture.EngineID, fixture.Epoch, generation}
			preimage := monitorCandidatePreimage(fields, body)
			if want := katHex(t, vector.PreimageHex); !bytes.Equal(preimage, want) {
				t.Fatalf("preimage = %x\n     want = %x", preimage, want)
			}
			sig := katBase64URL(t, vector.Signature, ed25519.SignatureSize)
			if !ed25519.Verify(public, preimage, sig) {
				t.Fatal("fixture Ed25519 signature did not verify")
			}

			if !bytes.HasSuffix(body, []byte("\n")) || !json.Valid(body) {
				t.Fatal("expected JSON with a final newline")
			}
			// The first two variants still decode to the same JSON; the last changes one digit.
			for _, mutation := range []struct {
				name string
				body []byte
			}{
				{"remove-final-newline", body[:len(body)-1]},
				{"prepend-json-whitespace", append([]byte(" \t"), body...)},
				{"change-version-byte", bytes.Replace(body, []byte(`"brine.agent.read/1"`), []byte(`"brine.agent.read/2"`), 1)},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					if !json.Valid(mutation.body) || bytes.Equal(mutation.body, body) {
						t.Fatal("test must change bytes without breaking JSON syntax")
					}
					if ed25519.Verify(public, monitorCandidatePreimage(fields, mutation.body), sig) {
						t.Fatal("signature verified over modified exact body bytes")
					}
				})
			}

			for _, mutation := range []struct {
				name   string
				change func(*monitorCandidateFields)
			}{
				{"swap-route", func(f *monitorCandidateFields) {
					if f.path == "/v1/monitor/capabilities" {
						f.path = "/v1/monitor/metrics"
					} else {
						f.path = "/v1/monitor/capabilities"
					}
				}},
				{"change-request-id", func(f *monitorCandidateFields) { f.requestID = "ffffffffffffffffffffffffffffffff" }},
				{"change-agent-id", func(f *monitorCandidateFields) {
					f.agentID = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
				}},
				{"change-engine-id", func(f *monitorCandidateFields) { f.engineID = "18500150-0000-4000-8000-000000000002" }},
				{"change-generation", func(f *monitorCandidateFields) {
					if f.generation == 1 {
						f.generation = 2
					} else {
						f.generation-- // Adjacent uint64, including 2^53 after a 2^53+1 positive.
					}
				}},
				{"change-epoch", func(f *monitorCandidateFields) { f.epoch = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" }},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					changed := fields
					mutation.change(&changed)
					if ed25519.Verify(public, monitorCandidatePreimage(changed, body), sig) {
						t.Fatal("signature verified with substituted binding field")
					}
				})
			}
			t.Run("swap-to-stock-domain", func(t *testing.T) {
				wrongDomain := append([]byte(stockSessionDomain), preimage[len(monitorCandidateDomain):]...)
				if ed25519.Verify(public, wrongDomain, sig) {
					t.Fatal("candidate signature verified with stock domain")
				}
			})
		})
	}
}

func TestMonitorCandidateGenerationTokens(t *testing.T) {
	// This exercises the test-only manifest reader, not an extension header parser.
	for _, token := range []string{"", "0", "01", "+1", "-1", " 1", "1 ", "1.0", "1e0", "18446744073709551616"} {
		if _, ok := candidateGeneration(token); ok {
			t.Errorf("accepted noncanonical or out-of-range generation %q", token)
		}
	}
}
