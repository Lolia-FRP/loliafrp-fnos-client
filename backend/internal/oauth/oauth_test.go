package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeviceRefreshAndRevoke(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("expected form body")
		}
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "public_client" {
			t.Error("missing client_id")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("must not use HTTP Basic")
		}
		switch r.URL.Path {
		case "/oauth2/device/code":
			fmt.Fprint(w, `{"device_code":"private","user_code":"ABCD-EFGH","verification_uri":"https://dash.lolia.link/oauth/device","expires_in":600,"interval":5}`)
		case "/oauth2/token":
			if r.Form.Get("grant_type") == "urn:ietf:params:oauth:grant-type:device_code" {
				calls++
				if r.Form.Get("device_code") != "private" {
					t.Error("missing device_code")
				}
				if calls == 1 {
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":"authorization_pending"}`)
					return
				}
			} else if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" {
				t.Error("bad refresh request")
			}
			fmt.Fprint(w, `{"access_token":"oauth2_access","refresh_token":"refresh","token_type":"Bearer","expires_in":86400,"scope":"user:read"}`)
		case "/oauth2/revoke":
			if r.Form.Get("token_type_hint") != "refresh_token" || r.Form.Get("token") != "refresh" {
				t.Error("bad revoke request")
			}
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	c := New(server.URL, "public_client")
	ctx := context.Background()
	d, err := c.Device(ctx)
	if err != nil || d.UserCode != "ABCD-EFGH" {
		t.Fatalf("device: %+v %v", d, err)
	}
	_, err = c.Poll(ctx, d.DeviceCode)
	var problem *Error
	if !errors.As(err, &problem) || problem.Code != "authorization_pending" {
		t.Fatalf("pending: %v", err)
	}
	token, err := c.Poll(ctx, d.DeviceCode)
	if err != nil || token.Access != "oauth2_access" {
		t.Fatal(err)
	}
	if _, err = c.Refresh(ctx, "refresh"); err != nil {
		t.Fatal(err)
	}
	if err = c.Revoke(ctx, "refresh", "refresh_token"); err != nil {
		t.Fatal(err)
	}
}
