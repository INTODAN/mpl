package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"islandBattles/lobby"
)

type principalContextKey struct{}
type sessionContextKey struct{}

func principalFromContext(ctx context.Context) lobby.Principal {
	principal, _ := ctx.Value(principalContextKey{}).(lobby.Principal)
	return principal
}
func authenticate(authenticator lobby.Authenticator) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		verified, err := authenticator.Authenticate(ctx.Context(), ctx.Header("Authorization"))
		if err != nil {
			problem := problemFor(err).(*Problem)
			if lobby.ErrorCodeOf(err) == "" {
				problem = &Problem{Title: http.StatusText(http.StatusUnauthorized), Status: http.StatusUnauthorized, Detail: "authentication required", Code: lobby.CodeUnauthenticated}
			}
			writeProblem(ctx, problem)
			return
		}
		next(huma.WithValue(huma.WithValue(ctx, principalContextKey{}, verified.Principal), sessionContextKey{}, verified))
	}
}
func writeProblem(ctx huma.Context, problem *Problem) {
	ctx.SetStatus(problem.Status)
	ctx.SetHeader("Content-Type", "application/problem+json")
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(problem)
}
