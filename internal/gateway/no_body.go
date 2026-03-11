package gateway

import "net/http"

// WithNoBodyWriteGuard suppresses response body writes for requests/status codes
// that must not emit a body. This prevents downstream handlers from logging
// write errors after setting HEAD/1xx/204/304 responses.
func WithNoBodyWriteGuard(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(&noBodyWriteGuard{
			ResponseWriter: w,
			headRequest:    r != nil && r.Method == http.MethodHead,
		}, r)
	})
}

type noBodyWriteGuard struct {
	http.ResponseWriter
	headRequest bool
	statusCode  int
	wroteHeader bool
}

func (w *noBodyWriteGuard) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *noBodyWriteGuard) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = statusCode
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *noBodyWriteGuard) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.headRequest || responseBodyForbidden(w.statusCode) {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}

func responseBodyForbidden(statusCode int) bool {
	return (statusCode >= 100 && statusCode < 200) ||
		statusCode == http.StatusNoContent ||
		statusCode == http.StatusNotModified
}
