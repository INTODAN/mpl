package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestFrameworkValidationHasStableProblemCode(t *testing.T) {
	_, api := newTestAPI(t)
	response := api.Put("/lobby/v1/games/island-battles/lobbies/"+testUUID+"/ready", "Authorization: Bearer good", map[string]any{})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", response.Code)
	}
	var problem map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem["code"] != "invalid_input" || problem["status"] != float64(422) || problem["errors"] == nil {
		t.Fatalf("validation details/code lost: %s", response.Body.String())
	}
	response = api.Post("/lobby/v1/sessions", map[string]any{})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("anonymous validation status %d", response.Code)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem["code"] != "invalid_input" {
		t.Fatal("public session validation missing code")
	}
}
