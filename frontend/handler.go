// Package frontend serves the temporary browser client for the generic lobby API.
package frontend

import (
	"embed"
	"net/http"
)

//go:embed index.html app.js style.css vendor
var assets embed.FS

// Handler serves only the embedded UI assets. Mount it on the same origin as
// /lobby/v1 so guest credentials never need cross-origin configuration.
func Handler() http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(response, request)
	})
}
