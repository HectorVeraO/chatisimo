package fakediscord

import (
	"embed"
	"net/http"
	"strconv"
)

// webFiles holds the web client served at "/". It is plain HTML, CSS and ES-module
// JavaScript with no build step.
//
//go:embed web
var webFiles embed.FS

const (
	webDir      = "web/"
	webPrefix   = "/web/"
	indexAsset  = "index.html"
	contentHTML = "text/html; charset=utf-8"
	contentCSS  = "text/css; charset=utf-8"
	contentJS   = "text/javascript; charset=utf-8"

	// webSecurityPolicy lets the page load only its own scripts and styles and talk only to
	// its own origin. The page has no inline script or style.
	webSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; " +
		"img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

// webAssetType returns the content type of a file served under /web/, or false for any
// other name. Content types are fixed here instead of derived from the file extension, which
// some platforms map to a type browsers refuse for ES modules.
func webAssetType(name string) (string, bool) {
	switch name {
	case "app.css":
		return contentCSS, true
	case "app.js", "api.js", "render.js", "slash.js", "modal.js":
		return contentJS, true
	default:
		return "", false
	}
}

func addWebRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", handleWebIndex)
	mux.HandleFunc("GET "+webPrefix+"{file}", handleWebAsset)
}

func handleWebIndex(w http.ResponseWriter, _ *http.Request) {
	serveWebFile(w, indexAsset, contentHTML)
}

func handleWebAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	contentType, isAsset := webAssetType(name)
	if !isAsset {
		handleNotFound(w, r)
		return
	}

	serveWebFile(w, name, contentType)
}

func serveWebFile(w http.ResponseWriter, name, contentType string) {
	content, err := webFiles.ReadFile(webDir + name)
	if err != nil {
		panic("fakediscord: embedded web file " + name + " is missing: " + err.Error())
	}

	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Content-Length", strconv.Itoa(len(content)))
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", webSecurityPolicy)
	w.WriteHeader(http.StatusOK)
	// The client may have gone away; there is nobody to tell.
	_, _ = w.Write(content)
}
