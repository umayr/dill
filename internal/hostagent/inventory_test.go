package hostagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

type inventoryMock struct {
	rows        []InventoryContainer
	inspections map[string]InventoryContainer
}

func (m *inventoryMock) InventoryList(context.Context) ([]InventoryContainer, error) {
	return append([]InventoryContainer(nil), m.rows...), nil
}
func (m *inventoryMock) InventoryInspect(_ context.Context, id string) (InventoryContainer, error) {
	v, ok := m.inspections[id]
	if !ok {
		return v, ErrNotFound
	}
	return v, nil
}
func TestInventoryHTTP(t *testing.T) {
	labels := map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "v"}
	id1 := fmt.Sprintf("%064x", 1)
	id2 := fmt.Sprintf("%064x", 2)
	id3 := fmt.Sprintf("%064x", 3)
	m := &inventoryMock{rows: []InventoryContainer{{ID: id3, Labels: labels}, {ID: id2, Labels: labels}, {ID: id1, Labels: labels}}, inspections: map[string]InventoryContainer{id1: {ID: id1, Labels: labels, Name: "one"}, id2: {ID: id2, Labels: map[string]string{"dill.managed": "false"}}, id3: {ID: id3, Labels: labels, Name: "three"}}}
	s, e := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: identity("id"), InventoryBackend: m})
	if e != nil {
		t.Fatal(e)
	}
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/inventory", bytes.NewBufferString(body))
		r.Header.Set("X-Request-Id", strings.Repeat("a", 32))
		r.Header.Set("X-Brine-Binding", "1")
		r.Header.Set("X-Agent-Epoch", s.epoch)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := call(`{"cursor":null,"limit":1}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Data struct {
			Items      []struct{ ID string }
			NextCursor *string
		}
	}
	if e = json.Unmarshal(w.Body.Bytes(), &response); e != nil || len(response.Data.Items) != 1 || response.Data.Items[0].ID != id1 || response.Data.NextCursor == nil {
		t.Fatalf("page %s %v", w.Body.String(), e)
	}
	cursor := *response.Data.NextCursor
	b, _ := json.Marshal(map[string]any{"cursor": cursor, "limit": 1})
	w = call(string(b))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	response.Data.Items = nil
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if len(response.Data.Items) != 1 || response.Data.Items[0].ID != id3 || response.Data.NextCursor != nil {
		t.Fatal(w.Body.String())
	}
	b, _ = json.Marshal(map[string]any{"cursor": cursor + "x", "limit": 1})
	if call(string(b)).Code != 400 {
		t.Fatal("forged cursor")
	}
	m.rows = make([]InventoryContainer, 1001)
	if call(`{"cursor":null,"limit":100}`).Code != 413 {
		t.Fatal("truncated scan")
	}
}
