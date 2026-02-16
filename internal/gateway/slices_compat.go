package gateway

import (
	"net/http"
	"strings"
)

// SlicePathCompatHandler rewrites FileService routes that embed slice IDs in the URL
// path to the query-param form that our gateway reliably supports.
//
// This allows slice IDs to contain '/' (e.g. "nic/my-slice") without breaking URL
// routing, and avoids grpc-gateway limitations around multi-segment path variables.
//
// Supported rewrites:
//   - /v1/slices/<sliceID>/entries[/<path...>] -> /v1/files/entries[/<path...>]?slice_version.slice_id=<sliceID>
//   - /v1/slices/<sliceID>/files[/<path...>]   -> /v1/files[/<path...>]?slice_version.slice_id=<sliceID>
//   - /v1/slices/<sliceID>/files/history/<path...> -> /v1/files/history/<path...>?slice_id=<sliceID>
//
// All other /v1/slices/* requests are passed through unchanged.
func SlicePathCompatHandler(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fallback == nil || r == nil || r.URL == nil {
			http.NotFound(w, r)
			return
		}

		const prefix = "/v1/slices/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			fallback.ServeHTTP(w, r)
			return
		}

		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if rest == "" {
			fallback.ServeHTTP(w, r)
			return
		}

		parts := strings.Split(rest, "/")

		// entries
		if idx := indexOf(parts, "entries"); idx >= 1 {
			sliceID := strings.Join(parts[:idx], "/")
			subPath := strings.Join(parts[idx+1:], "/")

			newPath := "/v1/files/entries"
			if subPath != "" {
				newPath += "/" + subPath
			}

			q := r.URL.Query()
			q.Set("slice_version.slice_id", sliceID)
			r.URL.Path = newPath
			r.URL.RawQuery = q.Encode()
			fallback.ServeHTTP(w, r)
			return
		}

		// files (+ history)
		if idx := indexOf(parts, "files"); idx >= 1 {
			sliceID := strings.Join(parts[:idx], "/")

			// files/history/<path...>
			if idx+1 < len(parts) && parts[idx+1] == "history" {
				subPath := strings.Join(parts[idx+2:], "/")
				if subPath == "" {
					fallback.ServeHTTP(w, r)
					return
				}

				newPath := "/v1/files/history/" + subPath
				q := r.URL.Query()
				q.Set("slice_id", sliceID)
				r.URL.Path = newPath
				r.URL.RawQuery = q.Encode()
				fallback.ServeHTTP(w, r)
				return
			}

			// files[/<path...>]
			subPath := strings.Join(parts[idx+1:], "/")
			newPath := "/v1/files"
			if subPath != "" {
				newPath += "/" + subPath
			}

			q := r.URL.Query()
			q.Set("slice_version.slice_id", sliceID)
			r.URL.Path = newPath
			r.URL.RawQuery = q.Encode()
			fallback.ServeHTTP(w, r)
			return
		}

		fallback.ServeHTTP(w, r)
	})
}

func indexOf(parts []string, target string) int {
	for i, p := range parts {
		if p == target {
			return i
		}
	}
	return -1
}
