package fnos

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareAdminParsing(t *testing.T) {
	tests := []struct {
		name        string
		dev         bool
		adminHeader string
		wantAdmin   bool
	}{
		{"dev mode always admin", true, "", true},
		{"dev mode with false header", true, "false", true},
		{"prod lowercase true", false, "true", true},
		{"prod titlecase True", false, "True", true},
		{"prod uppercase TRUE", false, "TRUE", true},
		{"prod number 1", false, "1", true},
		{"prod false", false, "false", false},
		{"prod number 0", false, "0", false},
		{"prod empty", false, "", false},
		{"prod other string", false, "yes", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var recorded Principal
			handler := Middleware(tt.dev, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recorded = Current(r.Context())
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Trim-Userid", "1001")
			req.Header.Set("X-Trim-Username", "tester")
			if tt.adminHeader != "" {
				req.Header.Set("X-Trim-Isadmin", tt.adminHeader)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", w.Code)
			}
			if recorded.IsAdmin != tt.wantAdmin {
				t.Errorf("IsAdmin = %v, want %v", recorded.IsAdmin, tt.wantAdmin)
			}
			if recorded.UID != "1001" || recorded.Username != "tester" {
				t.Errorf("unexpected principal: %+v", recorded)
			}
		})
	}
}

func TestMiddlewareUnauthorized(t *testing.T) {
	handler := Middleware(false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
