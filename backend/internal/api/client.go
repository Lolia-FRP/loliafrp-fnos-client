package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.lolia.link/api/v1"

type Client struct {
	Base string
	HTTP *http.Client
}
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }
func New(base string, development bool) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(development && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return nil, errors.New("API 地址须使用 HTTPS；开发模式只允许本机 HTTP")
	}
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Do(ctx context.Context, token, method, path string, body, out any) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return errors.New("invalid API path")
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 8<<20 {
		return errors.New("API 响应过大")
	}
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(b, &envelope); err != nil {
		return fmt.Errorf("API 返回了无效 JSON（HTTP %d）", res.StatusCode)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || envelope.Code < 200 || envelope.Code >= 300 {
		status := res.StatusCode
		if status >= 200 && status < 300 {
			status = envelope.Code
		}
		if envelope.Msg == "" {
			envelope.Msg = http.StatusText(status)
		}
		return &Error{Status: status, Message: envelope.Msg}
	}
	if out != nil && len(envelope.Data) > 0 {
		return json.Unmarshal(envelope.Data, out)
	}
	return nil
}

type User struct {
	ID           int    `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	Avatar       string `json:"avatar"`
	Role         string `json:"role"`
	MaxTunnels   int    `json:"max_tunnel_count"`
	TrafficLimit int64  `json:"traffic_limit"`
	TrafficUsed  int64  `json:"traffic_used"`
	HasKYC       bool   `json:"has_kyc"`
	HasQQ        bool   `json:"has_qq"`
	IsBanned     bool   `json:"is_banned"`
}
type Tunnel struct {
	ID           int            `json:"id"`
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	NodeID       int            `json:"node_id"`
	LocalIP      string         `json:"local_ip"`
	LocalPort    int            `json:"local_port"`
	RemotePort   int            `json:"remote_port"`
	CustomDomain string         `json:"custom_domain"`
	Remark       string         `json:"remark"`
	Status       string         `json:"status"`
	NodeName     string         `json:"node_name"`
	NodeAddress  string         `json:"node_address"`
	Config       map[string]any `json:"config"`
}

func (c *Client) User(ctx context.Context, token string) (User, error) {
	var u User
	err := c.Do(ctx, token, "GET", "/user/info", nil, &u)
	return u, err
}
func (c *Client) Tunnels(ctx context.Context, token string) ([]Tunnel, error) {
	result := []Tunnel{}
	for page := 1; page <= 10000; page++ {
		var data struct {
			List      []Tunnel `json:"list"`
			Total     int      `json:"total"`
			TotalPage int      `json:"total_page"`
		}
		err := c.Do(ctx, token, "GET", fmt.Sprintf("/user/tunnel?page=%d&limit=100", page), nil, &data)
		if err != nil {
			return nil, err
		}
		result = append(result, data.List...)
		if len(data.List) == 0 || (data.TotalPage > 0 && page >= data.TotalPage) || (data.TotalPage == 0 && len(result) >= data.Total) {
			return result, nil
		}
	}
	return nil, errors.New("隧道列表分页超过限制")
}
func (c *Client) Config(ctx context.Context, token, name string) ([]byte, error) {
	var data struct {
		Config string `json:"config"`
		Lolia  string `json:"lolia_config"`
	}
	if err := c.Do(ctx, token, "GET", "/user/frpc/config?tunnel="+url.QueryEscape(name), nil, &data); err != nil {
		return nil, err
	}
	if data.Lolia == "" {
		return nil, errors.New("服务端未生成 LoliaFRP 专用配置，请稍后重试")
	}
	b, err := base64.StdEncoding.DecodeString(data.Lolia)
	if err != nil || len(b) == 0 {
		return nil, errors.New("无效的 Base64 frpc 配置")
	}
	return b, nil
}
