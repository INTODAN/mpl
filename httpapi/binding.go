package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
)

type bindInput struct {
	Body domain.Scope
}

type bindOutput[Result any] struct {
	Body Result
}

// RegisterGameBinding installs an explicit game-owned endpoint using shared
// lobby authentication and problem responses. Register does not call it, so
// these operations stay outside the lobby-only OpenAPI/SDK surface.
func RegisterGameBinding[Result any](api huma.API, operation huma.Operation, binding *application.GameBinding[Result], authenticator domain.Authenticator) {
	registerSchemas(api)
	group := huma.NewGroup(api)
	group.UseTransformer(BoundErrorDetails)
	group.UseMiddleware(authenticate(authenticator))
	operation.Method = http.MethodPost
	operation.DefaultStatus = http.StatusOK
	operation.Security = lobbyBearer
	operation.Errors = []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable}
	huma.Register(group, operation, func(ctx context.Context, input *bindInput) (*bindOutput[Result], error) {
		result, err := binding.Bind(ctx, principalFromContext(ctx), input.Body)
		if err != nil {
			return nil, problemFor(err)
		}
		return &bindOutput[Result]{Body: result}, nil
	})
	registerProblemResponses(api)
}
