package secretform

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"tclaw/libraries/secret"
)

// setSecurityHeaders applies standard security headers to all form responses.
func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	// Only allow inline styles (needed for the form CSS), block everything else.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; form-action 'self'")
}

// newFormHTTPHandler returns an http.Handler that serves the form (GET) and
// processes submissions (POST) at /secret-form/{state}.
func newFormHTTPHandler(secretStore secret.Store, pending *sync.Map) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)

		state := strings.TrimPrefix(r.URL.Path, "/secret-form/")
		if state == "" || state == r.URL.Path {
			renderNotFound(w)
			return
		}

		entry, ok := pending.Load(state)
		if !ok {
			renderNotFound(w)
			return
		}
		req := entry.(*PendingRequest)

		// Check TTL before checking Done — expired forms should always 404
		// regardless of submission state.
		if time.Since(req.CreatedAt) > requestTTL {
			pending.Delete(state)
			renderNotFound(w)
			return
		}

		// Already submitted — Done channel is closed.
		select {
		case <-req.Done:
			renderNotFound(w)
			return
		default:
		}

		switch r.Method {
		case http.MethodGet:
			handleFormGET(w, req)
		case http.MethodPost:
			handleFormPOST(w, r, req, secretStore)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func handleFormGET(w http.ResponseWriter, req *PendingRequest) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := formTemplate.Execute(w, req); err != nil {
		slog.Error("render form template", "err", err)
	}
}

func handleFormPOST(w http.ResponseWriter, r *http.Request, req *PendingRequest, secretStore secret.Store) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	// Verify the challenge code before accepting any data.
	submittedCode := strings.TrimSpace(r.PostFormValue("_verify_code"))
	if submittedCode != req.VerifyCode {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		if err := verifyErrorTemplate.Execute(w, req); err != nil {
			slog.Error("render verify error template", "err", err)
		}
		return
	}

	// Server-side required field validation.
	for _, field := range req.Fields {
		if field.IsRequired() && strings.TrimSpace(r.PostFormValue(field.Key)) == "" {
			http.Error(w, "required field missing: "+field.Label, http.StatusBadRequest)
			return
		}
	}

	for _, field := range req.Fields {
		value := r.PostFormValue(field.Key)
		if value == "" {
			continue
		}
		if err := secretStore.Set(r.Context(), field.Key, value); err != nil {
			slog.Error("store secret form value", "key", field.Key, "err", err)
			http.Error(w, "failed to store value", http.StatusInternalServerError)
			return
		}
	}

	// Signal completion. The entry stays in the map so secret_form_wait can
	// still find it by request_id. The Done check at the top of the HTTP
	// handler prevents reuse.
	close(req.Done)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := confirmationTemplate.Execute(w, nil); err != nil {
		slog.Error("render confirmation template", "err", err)
	}
}

func renderNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if err := notFoundTemplate.Execute(w, nil); err != nil {
		slog.Error("render not found template", "err", err)
	}
}
