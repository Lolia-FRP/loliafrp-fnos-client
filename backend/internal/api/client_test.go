package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoliaContracts(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth2_test" {
			t.Error("missing bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/user/info":
			fmt.Fprint(w, `{"code":200,"msg":"success","data":{"id":1,"username":"lolia","avatar":"https://cdn.example.com/avatars/lolia.png","role":"sponsor","max_tunnel_count":10}}`)
		case "/api/v1/user/tunnel":
			calls++
			page := r.URL.Query().Get("page")
			fmt.Fprintf(w, `{"code":200,"data":{"list":[{"id":%s,"name":"0000000000000000000000000000000%s"}],"total":2,"total_page":2}}`, page, page)
		case "/api/v1/user/nodes":
			if r.Method != "POST" {
				t.Error("nodes requires POST")
			}
			var body map[string]int
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("nodes requires JSON body")
			}
			fmt.Fprint(w, `{"code":200,"data":{"nodes":[]}}`)
		case "/api/v1/user/frpc/config":
			if r.URL.Query().Get("tunnel") != "abc" {
				t.Error("missing tunnel query")
			}
			fmt.Fprintf(w, `{"code":200,"data":{"config":"ignored","lolia_config":%q}}`, base64.StdEncoding.EncodeToString([]byte("serverAddr = 'example.com'")))
		case "/api/v1/create":
			w.WriteHeader(201)
			fmt.Fprint(w, `{"code":201,"data":{"id":3}}`)
		default:
			fmt.Fprint(w, `{"code":403,"msg":"scope denied"}`)
		}
	}))
	defer server.Close()
	c, err := New(server.URL+"/api/v1", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user, err := c.User(ctx, "oauth2_test")
	if err != nil || user.Role != "sponsor" || user.ID != 1 || user.Avatar != "https://cdn.example.com/avatars/lolia.png" {
		t.Fatalf("user: %+v %v", user, err)
	}
	tunnels, err := c.Tunnels(ctx, "oauth2_test")
	if err != nil || len(tunnels) != 2 || calls != 2 {
		t.Fatalf("pagination: %+v %v", tunnels, err)
	}
	if err = c.Do(ctx, "oauth2_test", "POST", "/user/nodes", map[string]int{"page": 1}, nil); err != nil {
		t.Fatal(err)
	}
	config, err := c.Config(ctx, "oauth2_test", "abc")
	if err != nil || string(config) != "serverAddr = 'example.com'" {
		t.Fatalf("config: %q %v", config, err)
	}
	if err = c.Do(ctx, "oauth2_test", "POST", "/create", map[string]int{}, nil); err != nil {
		t.Fatal(err)
	}
	err = c.Do(ctx, "oauth2_test", "GET", "/forbidden", nil, nil)
	var problem *Error
	if !errors.As(err, &problem) || problem.Status != 403 {
		t.Fatalf("business error: %v", err)
	}
}
func TestProductionURLPolicy(t *testing.T) {
	for _, raw := range []string{"http://example.com", "http://127.0.0.1:1234", "https://user:secret@example.com", "https://example.com?secret=1"} {
		if _, err := New(raw, false); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}
