// Package website embeds the SHDC operations dashboard.
//
// Cache nodes serve these files same-origin at / so the dashboard can call
// the cluster API with plain fetch: no CORS preflight pain, no mixed-content
// wall (a separately-hosted HTTPS page could not call the HTTP-only cluster).
// See Server.Handler: the file server is registered last so API routes
// (/set, /get, /health, …) keep precedence.
package website

import (
	"embed"
	"net/http"
)

//go:embed index.html css/* js/* assets/*
var content embed.FS

// Handler serves the embedded dashboard files.
func Handler() http.Handler {
	return http.FileServer(http.FS(content))
}
