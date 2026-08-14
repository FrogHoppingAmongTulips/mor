package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
)

// contentSecurityPolicy names the exact <script> and <style> the panel ships
// with and forbids everything else.
//
// The panel is one self-contained file — no CDN, no external font, no
// analytics — so every source is locked to this origin. The two inline blocks
// are allowed by their own SHA-256, computed from the file at startup: an
// injected script has no matching hash and simply does not run, and even if
// one did, connect-src leaves it nowhere to send what it stole.
//
// This is only possible because no handler is written into the markup any
// more. Every control carries data-act and one delegated listener dispatches
// them, which is what let 'unsafe-inline' go.
func contentSecurityPolicy(page []byte) string {
	return "default-src 'none'; " +
		"script-src " + inlineHash(page, "<script>", "</script>") + "; " +
		"style-src " + inlineHash(page, "<style>", "</style>") + "; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"form-action 'none'; " +
		"base-uri 'none'; " +
		"frame-ancestors 'none'"
}

// inlineHash returns the CSP source expression for the one block between open
// and close. A page that somehow lacks the block falls back to allowing
// nothing, which is the safe direction: the panel would visibly break rather
// than quietly run unverified code.
func inlineHash(page []byte, open, close string) string {
	start := bytes.Index(page, []byte(open))
	if start < 0 {
		return "'none'"
	}
	start += len(open)
	end := bytes.Index(page[start:], []byte(close))
	if end < 0 {
		return "'none'"
	}
	sum := sha256.Sum256(page[start : start+end])
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// withSecurityHeaders wraps the whole mux rather than each handler: a header
// that protects only the routes somebody remembered to decorate protects
// nothing.
func withSecurityHeaders(h http.Handler, csp string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		head := w.Header()
		head.Set("Content-Security-Policy", csp)
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
