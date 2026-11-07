package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultScopes = "user:read tunnel:read tunnel:write traffic:read domain:read domain:write node:read announcement:read release:read"

type Client struct {
	Base, ID string
	HTTP     *http.Client
}
type Device struct {
	DeviceCode           string `json:"device_code"`
	UserCode             string `json:"user_code"`
	VerificationURI      string `json:"verification_uri"`
	VerificationComplete string `json:"verification_uri_complete"`
	ExpiresIn            int    `json:"expires_in"`
	Interval             int    `json:"interval"`
}
type Token struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	Type      string `json:"token_type"`
	Scope     string `json:"scope"`
	ExpiresIn int    `json:"expires_in"`
}
type Error struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *Error) Error() string {
	if e.Description != "" {
		return e.Description
	}
	return e.Code
}
func New(base, id string) *Client {
	return &Client{Base: base, ID: id, HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) post(ctx context.Context, path string, form url.Values, out any) error {
	if c.ID == "" {
		return errors.New("客户端缺少内置 OAuth 应用标识，请更新客户端")
	}
	form.Set("client_id", c.ID)
	req, err := http.NewRequestWithContext(ctx, "POST", c.Base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return errors.New("OAuth 响应过大")
	}
	var problem Error
	_ = json.Unmarshal(b, &problem)
	if problem.Code != "" {
		return &problem
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("OAuth 服务返回 HTTP %d", res.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}
func (c *Client) Device(ctx context.Context) (Device, error) {
	var d Device
	err := c.post(ctx, "/oauth2/device/code", url.Values{"scope": {DefaultScopes}}, &d)
	if err == nil && (d.DeviceCode == "" || d.UserCode == "" || d.ExpiresIn <= 0) {
		err = errors.New("设备授权响应缺少必要字段")
	}
	if d.Interval < 5 {
		d.Interval = 5
	}
	return d, err
}
func (c *Client) token(ctx context.Context, v url.Values) (Token, error) {
	var t Token
	err := c.post(ctx, "/oauth2/token", v, &t)
	if err == nil && (t.Access == "" || t.ExpiresIn <= 0 || !strings.EqualFold(t.Type, "Bearer")) {
		err = errors.New("无效 OAuth 令牌响应")
	}
	return t, err
}
func (c *Client) Poll(ctx context.Context, code string) (Token, error) {
	return c.token(ctx, url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {code}})
}
func (c *Client) Refresh(ctx context.Context, token string) (Token, error) {
	return c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}})
}
func (c *Client) Revoke(ctx context.Context, token, hint string) error {
	return c.post(ctx, "/oauth2/revoke", url.Values{"token": {token}, "token_type_hint": {hint}}, nil)
}
