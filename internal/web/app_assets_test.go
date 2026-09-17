package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppAssetsAreServedFromEmbeddedFilesystem(t *testing.T) {
	if _, err := appDist.Open("."); err != nil {
		t.Fatalf("embedded app asset filesystem unavailable: %v", err)
	}

	mux := http.NewServeMux()
	(&App{}).Register(mux)
	request := httptest.NewRequest(http.MethodGet, "/app/", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("embedded app response status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.Len() == 0 {
		t.Fatal("embedded app response was empty")
	}
}
