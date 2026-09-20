package httpapi

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// registerInvalidationSchema keeps the realtime event model in the generated
// SDK even though it is delivered by Centrifugo rather than an HTTP response.
func registerInvalidationSchema(registry huma.Registry) {
	registry.Schema(reflect.TypeOf(LobbyInvalidation{}), true, "LobbyInvalidation")
}
