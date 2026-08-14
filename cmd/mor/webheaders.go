package main

import "net/http"

// securityHeaders is what the browser is told to enforce on every response.
//
// The panel is one self-contained file — no CDN, no external font, no analytics
// — so everything is locked to this origin and the browser is told to refuse
// anything else. That is the part that matters most here: even if a script were
// somehow injected, it has nowhere to send what it steals.
//
// script-src and style-src still allow inline code, because the panel *is* one
// inline <script> and one inline <style>, plus event handlers written into the
// markup. Hashes cover the two blocks but not the handlers, so tightening this
// waits until the handlers move out to addEventListener — which is the same
// change the keyboard-accessibility work needs.
const contentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"form-action 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"

// withSecurityHeaders wraps the whole mux rather than each handler: a header
// that protects only the routes somebody remembered to decorate protects
// nothing.
func withSecurityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		head := w.Header()
		head.Set("Content-Security-Policy", contentSecurityPolicy)
		// frame-ancestors above already says this to modern browsers; the
		// header stays for the ones that never learned it.
		head.Set("X-Frame-Options", "DENY")
		// The panel's address contains the server's IP. Nothing outside should
		// ever be told it, including by a link somebody pastes into a key name.
		head.Set("Referrer-Policy", "no-referrer")
		// Stops a browser from guessing a content type and, say, running a
		// stored string as a script because it looked like one.
		head.Set("X-Content-Type-Options", "nosniff")
		// The panel is HTTPS-only, so the browser is told to remember that and
		// stop trying plain HTTP on its own.
		head.Set("Strict-Transport-Security", "max-age=31536000")
		h.ServeHTTP(w, r)
	})
}
