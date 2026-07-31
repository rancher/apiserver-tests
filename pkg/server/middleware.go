package server

import (
	"log"
	"net/http"
)

func AuditLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[AUDIT] Method: %s | Path: %s | Agent: %s", r.Method, r.URL.Path, r.UserAgent())
		next.ServeHTTP(w, r)
	})
}
