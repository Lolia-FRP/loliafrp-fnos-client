package fnos

import (
	"context"
	"net/http"
	"regexp"
	"strings"
)

type Principal struct {
	UID      string `json:"uid"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
}
type key struct{}

var uidPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

func Middleware(dev bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Header.Get("X-Trim-Userid")
		name := r.Header.Get("X-Trim-Username")
		if dev && uid == "" {
			uid = "1000"
			name = "developer"
		}
		if !uidPattern.MatchString(uid) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		adminHeader := r.Header.Get("X-Trim-Isadmin")
		isAdmin := dev || strings.EqualFold(adminHeader, "true") || adminHeader == "1"
		p := Principal{UID: uid, Username: name, IsAdmin: isAdmin}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), key{}, p)))
	})
}
func Current(ctx context.Context) Principal { p, _ := ctx.Value(key{}).(Principal); return p }
