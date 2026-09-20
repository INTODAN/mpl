package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// validationProblems supplies the same stable code on Huma's own input errors
// as on domain errors, without changing Huma's process-global error factory.
// Only 422 problem bodies are buffered; ordinary responses pass through.
func validationProblems(ctx huma.Context, next func(huma.Context)) {
	wrapped := &validationContext{delegatedContext: ctx}
	next(wrapped)
	if wrapped.status != http.StatusUnprocessableEntity {
		return
	}
	payload := wrapped.body.Bytes()
	var problem map[string]json.RawMessage
	if json.Unmarshal(payload, &problem) == nil && problem != nil {
		if _, present := problem["code"]; !present {
			problem["code"] = json.RawMessage(`"invalid_input"`)
		}
		if encoded, err := json.Marshal(problem); err == nil {
			payload = append(encoded, '\n')
		}
	}
	ctx.SetHeader("Content-Type", "application/problem+json")
	ctx.SetStatus(http.StatusUnprocessableEntity)
	_, _ = ctx.BodyWriter().Write(payload)
}

type delegatedContext interface{ huma.Context }

type validationContext struct {
	delegatedContext
	status int
	body   bytes.Buffer
}

func (ctx *validationContext) SetStatus(status int) {
	ctx.status = status
	if status != http.StatusUnprocessableEntity {
		ctx.delegatedContext.SetStatus(status)
	}
}

func (ctx *validationContext) Status() int {
	if ctx.status != 0 {
		return ctx.status
	}
	return ctx.delegatedContext.Status()
}

func (ctx *validationContext) BodyWriter() io.Writer {
	if ctx.status == http.StatusUnprocessableEntity {
		return &ctx.body
	}
	return ctx.delegatedContext.BodyWriter()
}
