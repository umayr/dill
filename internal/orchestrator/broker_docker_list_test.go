package orchestrator

import (
	"io"
	"net/http"
	"testing"
)

func TestBrokerDockerListIsNotContainerInspect(t *testing.T) {
	for _, path := range []string{"/containers/json", "/v1.44/containers/json", "/images/json", "/v1.44/images/example/json"} {
		t.Run(path, func(t *testing.T) {
			transport := brokerDockerInspectTransport{contextTransport(func(r *http.Request) (*http.Response, error) { return contextResponse(r, 200, "[]"), nil })}
			request, _ := http.NewRequest(http.MethodGet, "http://docker"+path, nil)
			response, err := transport.RoundTrip(request)
			if err != nil || response == nil {
				t.Fatal("non-container-inspect endpoint filtered", err)
			}
			defer response.Body.Close()
			raw, _ := io.ReadAll(response.Body)
			if string(raw) != "[]" {
				t.Fatal("list body changed")
			}
		})
	}
	for _, path := range []string{"/containers/immutable/json", "/v1.44/containers/immutable/json"} {
		if !brokerDockerContainerInspectPath(path) {
			t.Fatal("actual inspect escaped guard")
		}
	}
}
