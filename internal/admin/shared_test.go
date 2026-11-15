package admin

import (
	"encoding/json"
	"testing"
)

// The console must stay usable when the fnOS open API socket is missing (local
// development, older hosts); the diagnostic endpoint reports it as data instead
// of failing the request.
func TestSharedFoldersUnavailable(t *testing.T) {
	t.Setenv("TRIM_API_TOKEN", "")
	a := archiveAdmin(t)
	rec := adminGet(t, a, "/api/shared-folders")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Available bool     `json:"available"`
		Paths     []string `json:"paths"`
		Error     string   `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if body.Available {
		t.Fatal("available = true without a token, want false")
	}
	if body.Error == "" {
		t.Fatal("expected an explanatory error")
	}
	if body.Paths == nil {
		t.Fatal("paths should be an empty array, not null")
	}
}
