package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
)

// unknownKeys is how many unknown keys the bounded-problem tests send. Before
// the bound, 400 keys answered 1.8 MB and allocated about 70 MB, growing with
// the square of the count, so a regression stays far from the memory cap.
const unknownKeys = 400

// unknownKeysBody is a JSON object with the given fields followed by count
// unknown keys.
func unknownKeysBody(fields string, count int) string {
	var body strings.Builder
	body.WriteString("{" + fields)
	for index := range count {
		if index > 0 || fields != "" {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `"unknown%05d":0`, index)
	}
	body.WriteString("}")
	return body.String()
}

type boundedProblem struct {
	Code   string             `json:"code"`
	Status int                `json:"status"`
	Errors []huma.ErrorDetail `json:"errors"`
}

// serveProblem sends one request straight to handler, without humatest's
// logging, and fails unless the answer stays within 16 KiB and the request
// allocates at most 4 MiB.
func serveProblem(t *testing.T, handler http.Handler, method, path, body string) (int, boundedProblem) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer good")
	recorder := httptest.NewRecorder()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	handler.ServeHTTP(recorder, request)
	runtime.ReadMemStats(&after)
	size, allocated := recorder.Body.Len(), after.TotalAlloc-before.TotalAlloc
	if size > 16<<10 || allocated > 4<<20 {
		t.Fatalf("%s %s answered %d bytes and allocated %d bytes for a %d byte body", method, path, size, allocated, len(body))
	}
	var problem boundedProblem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return recorder.Code, problem
}

// checkUnknownKeysProblem checks the problem for a body with unknownKeys
// unknown keys: other problems first, then unknown keys, then a correct count.
func checkUnknownKeysProblem(t *testing.T, problem boundedProblem) {
	t.Helper()
	if len(problem.Errors) != MaxErrorDetails {
		t.Fatalf("%d errors, want %d", len(problem.Errors), MaxErrorDetails)
	}
	listed := problem.Errors[:MaxErrorDetails-1]
	others := 0
	for others < len(listed) && listed[others].Message != "unexpected property" {
		others++
	}
	for _, detail := range listed {
		if detail.Value != nil {
			t.Fatalf("detail echoes a value: %+v", detail)
		}
	}
	for _, detail := range listed[others:] {
		if detail.Message != "unexpected property" {
			t.Fatalf("problem %+v is listed after an unknown key", detail)
		}
	}
	remaining := others + unknownKeys - len(listed)
	if last := problem.Errors[MaxErrorDetails-1]; last.Location != "" || last.Message != fmt.Sprintf("and %d more problems", remaining) {
		t.Fatalf("last detail = %+v, want and %d more problems", last, remaining)
	}
}

func TestUnknownKeysGiveABoundedProblem(t *testing.T) {
	handler, _ := newTestAPI(t)
	scope := "/lobby/v1/games/island-battles/lobbies/" + testUUID
	for _, route := range []struct{ method, path, fields string }{
		{http.MethodPost, "/lobby/v1/sessions", `"display_name":"Guest"`},
		{http.MethodPost, "/lobby/v1/lobbies", `"name":"Lobby"`},
		{http.MethodPost, "/lobby/v1/memberships", ""},
		{http.MethodPut, scope + "/team", ""},
		{http.MethodPut, scope + "/ready", ""},
		{http.MethodPost, scope + "/heartbeat", ""},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			status, problem := serveProblem(t, handler, route.method, route.path, unknownKeysBody(route.fields, unknownKeys))
			if status != http.StatusUnprocessableEntity || problem.Code != "invalid_input" {
				t.Fatalf("status=%d code=%q", status, problem.Code)
			}
			checkUnknownKeysProblem(t, problem)
		})
	}
}

func TestGameBindingGivesABoundedProblem(t *testing.T) {
	// A lobby-only API, without any transformer of the host's own.
	handler, api := humatest.New(t)
	binding, err := application.NewGameBinding("quiz-demo", &gameBindingReader{}, func(context.Context, domain.Snapshot) (quizBindingResult, error) {
		return quizBindingResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	RegisterGameBinding(api, huma.Operation{OperationID: "bindQuiz", Path: "/quiz/from-lobby"}, binding, fakeAuthenticator{})
	status, problem := serveProblem(t, handler, http.MethodPost, "/quiz/from-lobby", unknownKeysBody(`"game_key":"quiz-demo"`, unknownKeys))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", status)
	}
	checkUnknownKeysProblem(t, problem)
}

func TestMalformedBodyEchoIsCut(t *testing.T) {
	handler, _ := newTestAPI(t)
	status, problem := serveProblem(t, handler, http.MethodPost, "/lobby/v1/sessions", `{"display_name":"`+strings.Repeat("x", 4096))
	if status != http.StatusBadRequest || len(problem.Errors) != 1 {
		t.Fatalf("status=%d problem=%+v", status, problem)
	}
	if echoed, _ := problem.Errors[0].Value.(string); len(echoed) != MaxErrorValueLength || !strings.HasSuffix(echoed, "...") {
		t.Fatalf("echoed %d bytes: %q", len(echoed), echoed)
	}
}

func TestLimitErrorDetails(t *testing.T) {
	unexpected := func(key string) *huma.ErrorDetail {
		return &huma.ErrorDetail{Message: "unexpected property", Location: "body." + key, Value: map[string]any{key: 0}}
	}
	scalar := &huma.ErrorDetail{Message: "expected number", Location: "body.capacity", Value: true}
	long := &huma.ErrorDetail{Message: "expected length <= 40", Location: "body.name", Value: "xx" + strings.Repeat("é", MaxErrorValueLength)}
	original := []*huma.ErrorDetail{}
	for index := range 150 {
		original = append(original, unexpected(fmt.Sprint(index)))
	}
	original = append(original, scalar, long)

	limited := LimitErrorDetails(original)
	if len(original) != 152 || original[0].Value == nil || original[151] != long {
		t.Fatal("LimitErrorDetails edited the details it was given")
	}
	if len(limited) != MaxErrorDetails || limited[0] != scalar {
		t.Fatalf("%d details, first %+v", len(limited), limited[0])
	}
	if cut, _ := limited[1].Value.(string); len(cut) > MaxErrorValueLength || !utf8.ValidString(cut) || !strings.HasSuffix(cut, "...") || !strings.HasPrefix(cut, "xxé") {
		t.Fatalf("long value cut to %q", cut)
	}
	if limited[2].Message != "unexpected property" || limited[2].Value != nil {
		t.Fatalf("an unknown key echoes its object: %+v", limited[2])
	}
	if last := limited[MaxErrorDetails-1]; last.Message != "and 53 more problems" || last.Location != "" {
		t.Fatalf("last detail = %+v", last)
	}
	if again := LimitErrorDetails(limited); &again[0] != &limited[0] {
		t.Fatal("limited details changed on a second pass")
	}
	plain := []*huma.ErrorDetail{scalar}
	if same := LimitErrorDetails(plain); &same[0] != &plain[0] {
		t.Fatal("small details with scalar values were copied")
	}

	model := &huma.ErrorModel{Status: http.StatusUnprocessableEntity, Errors: original}
	transformed, err := BoundErrorDetails(nil, "422", model)
	if err != nil {
		t.Fatal(err)
	}
	if bounded := transformed.(*huma.ErrorModel); bounded == model || len(bounded.Errors) != MaxErrorDetails || len(model.Errors) != 152 {
		t.Fatal("BoundErrorDetails did not bound a copy of the problem")
	}
	if same, _ := BoundErrorDetails(nil, "422", &huma.ErrorModel{Errors: plain}); same.(*huma.ErrorModel).Errors[0] != scalar {
		t.Fatal("BoundErrorDetails changed a small problem")
	}
}
