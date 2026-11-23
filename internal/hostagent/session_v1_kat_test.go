package hostagent

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestStockSessionV1KnownAnswers(t *testing.T) {
	const (
		requestID = "00000000000000000000000000000004"
		nonce     = "paWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaU"
		epoch     = "WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo"
		engineID  = "11111111-1111-4111-8111-111111111111"
		kind      = "docker"
		socket    = "unix:///synthetic-engine.sock"
		daemon    = "synthetic-daemon"
		agentID   = "21fe31dfa154a261626bf854046fd2271b7bed4b6abe45aa58877ef47f9721b9"
		publicB64 = "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"

		// Literal stock proof bytes around the 8-byte generation. Pinned separately from the candidate manifest.
		proofHead = "brine-agent-session/1\x00" +
			"\x00\x00\x00\x2bpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaU" +
			"\x00\x00\x00\x4021fe31dfa154a261626bf854046fd2271b7bed4b6abe45aa58877ef47f9721b9" +
			"\x00\x00\x00\x2b11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo" +
			"\x00\x00\x00\x2411111111-1111-4111-8111-111111111111"
		proofTail = "\x00\x00\x00\x06docker" +
			"\x00\x00\x00\x1dunix:///synthetic-engine.sock" +
			"\x00\x00\x00\x10synthetic-daemon" +
			"\x00\x00\x00\x2bWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo"
	)
	// RFC 8032 §7.1 TEST 1's public synthetic seed and public key, in Go's seed || public format.
	// In-memory only; no random key generation or credential files.
	const rfc8032TestSeedHex = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	const rfc8032TestPublicHex = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
	private := ed25519.PrivateKey(katHex(t, rfc8032TestSeedHex+rfc8032TestPublicHex))
	public := ed25519.PublicKey(katHex(t, rfc8032TestPublicHex))

	// Expected signatures were derived offline from the stock layout with Node's crypto,
	// not by calling SessionBytes or the production handler.
	for _, vector := range []struct {
		decimal, u64Hex, signature string
		generation                 uint64
	}{
		{"1", "0000000000000001", "DMrg4kgN6dgmEJ3jK9I6GEV5awMkugD6UH09kqMYcE2Y1cOJ5aWouYKUPKqur2ZQr4wkGCxEZCeL5NCoQqUfCw", 1},
		{"9007199254740993", "0020000000000001", "e_lQppvJjlIvvZd6asja7ZSA8ag-AHcE2cYHYd85PmmaElyenGvJ6qrqgwxkJYt36gsDiW6vHDM9zd0l3v8ODA", 1<<53 + 1},
		{"18446744073709551615", "ffffffffffffffff", "MSM8QkLGHBmqMg4g_C0NLq0JcvUrYg5oPkvXMcffO09SdfTURggEsoWK2F_Nr7zU2grXXPzWEi32vMmdlUWuBA", 1<<64 - 1},
	} {
		t.Run(vector.decimal, func(t *testing.T) {
			wantProof := []byte(proofHead + string(katHex(t, vector.u64Hex)) + proofTail)
			proof, err := SessionBytes(nonce, agentID, publicB64, engineID, vector.generation, kind, socket, daemon, epoch)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(proof, wantProof) {
				t.Fatalf("stock proof = %x\n       want = %x", proof, wantProof)
			}

			// The real handler signs and emits the stock response in-process. Use a fixed epoch
			// and fake identity; bypass New's random setup, and do not create a listener.
			s := &Server{
				c: Config{EngineID: engineID, Generation: vector.generation, EngineKind: kind,
					EngineSocket: socket, DaemonID: daemon, PrivateKey: private, Identity: identity(daemon)},
				epoch: epoch, agentID: agentID, public: publicB64, operations: make(chan struct{}, 1),
			}
			call := func(binding string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/v1/session", strings.NewReader(`{"nonce":"`+nonce+`"}`))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-Request-Id", requestID)
				r.Header.Set("X-Brine-Binding", binding)
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				return w
			}
			w := call(vector.decimal)
			wantBody := `{"version":"brine.agent.read/1","requestId":"` + requestID +
				`","data":{"agentId":"` + agentID + `","agentPublicKey":"` + publicB64 +
				`","daemonId":"` + daemon + `","engineId":"` + engineID + `","engineKind":"` + kind +
				`","engineSocket":"` + socket + `","epoch":"` + epoch + `","generation":` + vector.decimal +
				`,"nonce":"` + nonce + `","signature":"` + vector.signature + `"}}` + "\n"
			if w.Code != http.StatusOK || w.Body.String() != wantBody {
				t.Fatalf("stock response = %d %q\n         want = 200 %q", w.Code, w.Body.String(), wantBody)
			}
			if h := w.Header(); h.Get("Content-Type") != "application/json" || h.Get("Cache-Control") != "no-store" ||
				h.Get("Content-Length") != strconv.Itoa(len(wantBody)) || len(h.Values("X-Dill-Monitor-Signature")) != 0 {
				t.Fatalf("unexpected stock response headers: %v", h)
			}
			var reply struct {
				Data struct {
					Signature  string `json:"signature"`
					Generation uint64 `json:"generation"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || reply.Data.Generation != vector.generation {
				t.Fatalf("stock reply generation = %d; want %d (decode error: %v)", reply.Data.Generation, vector.generation, err)
			}
			sig := katBase64URL(t, reply.Data.Signature, ed25519.SignatureSize)
			if !ed25519.Verify(public, wantProof, sig) {
				t.Fatal("stock handler signature did not verify over pinned bytes")
			}
			wrongDomain := append([]byte(monitorCandidateDomain), wantProof[len(stockSessionDomain):]...)
			if ed25519.Verify(public, wrongDomain, sig) {
				t.Fatal("stock signature verified with candidate domain")
			}
			for _, binding := range []string{"0" + vector.decimal, "18446744073709551616", strconv.FormatUint(vector.generation-1, 10)} {
				if wrong := call(binding); wrong.Code != http.StatusConflict {
					t.Errorf("stock binding %q returned %d, want 409", binding, wrong.Code)
				}
			}
		})
	}
}
