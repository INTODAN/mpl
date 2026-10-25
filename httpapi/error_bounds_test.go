package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// unknownKeysBody is a JSON object with the given fields followed by count
// unknown keys.
func unknownKeysBody(fields string, count int) string {
	var body strings.Builder
	body.WriteString("{" + fields)
	for index := range count {
		fmt.Fprintf(&body, `,"unknown%05d":0`, index)
	}
	body.WriteString("}")
	return body.String()
}

func TestUnknownKeysGiveABoundedProblem(t *testing.T) {
	_, api := newTestAPI(t)
	body := unknownKeysBody(`"display_name":"Guest"`, 2000)
	response := api.Post("/lobby/v1/lobbies", "Authorization: Bearer good", strings.NewReader(body))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", response.Code)
	}
	// Unbounded, each of the 2000 errors echoes the whole 30 KB body.
	if size := response.Body.Len(); size > 16<<10 {
		t.Fatalf("problem is %d bytes for a %d byte body", size, len(body))
	}
	var problem struct {
		Code   string           `json:"code"`
		Status int              `json:"status"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "invalid_input" || problem.Status != http.StatusUnprocessableEntity || len(problem.Errors) != MaxErrorDetails {
		t.Fatalf("code=%q status=%d errors=%d", problem.Code, problem.Status, len(problem.Errors))
	}
	for _, detail := range problem.Errors[:MaxErrorDetails-1] {
		if _, echoed := detail["value"]; echoed {
			t.Fatalf("detail echoes its object: %v", detail)
		}
	}
	if last := problem.Errors[MaxErrorDetails-1]["message"]; !strings.HasPrefix(fmt.Sprint(last), "and ") || !strings.HasSuffix(fmt.Sprint(last), " more errors") {
		t.Fatalf("last detail = %v", problem.Errors[MaxErrorDetails-1])
	}
}

func TestBoundErrorDetailsKeepsScalarValuesAndIsIdempotent(t *testing.T) {
	scalar := &huma.ErrorDetail{Message: "expected length <= 40", Location: "body.display_name", Value: "long"}
	original := &huma.ErrorModel{Status: http.StatusUnprocessableEntity, Errors: []*huma.ErrorDetail{scalar}}
	for range 150 {
		original.Errors = append(original.Errors, &huma.ErrorDetail{Message: "unexpected property", Location: "body.x", Value: map[string]any{"x": 0}})
	}
	transformed, err := BoundErrorDetails(nil, "422", original)
	if err != nil {
		t.Fatal(err)
	}
	bounded := transformed.(*huma.ErrorModel)
	if len(original.Errors) != 151 || original.Errors[1].Value == nil {
		t.Fatal("the transformer edited the original error")
	}
	if len(bounded.Errors) != MaxErrorDetails || bounded.Errors[0] != scalar || bounded.Errors[1].Value != nil {
		t.Fatalf("bounded errors = %d, first %v, second %v", len(bounded.Errors), bounded.Errors[0], bounded.Errors[1])
	}
	if last := bounded.Errors[MaxErrorDetails-1].Message; last != "and 52 more errors" {
		t.Fatalf("last message = %q", last)
	}
	again, _ := BoundErrorDetails(nil, "422", bounded)
	if again != any(bounded) {
		t.Fatal("a bounded error changed on a second pass")
	}
	plain := &huma.ErrorModel{Errors: []*huma.ErrorDetail{scalar}}
	if same, _ := BoundErrorDetails(nil, "422", plain); same != any(plain) {
		t.Fatal("a small error with scalar values was copied")
	}
}
