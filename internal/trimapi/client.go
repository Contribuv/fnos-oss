// Package trimapi talks to the fnOS open API over its Unix socket. It is only
// used for diagnostics: the folder picker returns the granted path directly, so
// the console keeps working even when this socket is unavailable (for example
// in local development on Windows).
package trimapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// SocketPath is where fnOS exposes the scoped open API.
const SocketPath = "/var/run/trim_open_gateway_apiscope.socket"

type response struct {
	ReqID string          `json:"reqId"`
	Code  int             `json:"code"`
	Msg   string          `json:"msg"`
	Data  json.RawMessage `json:"data"`
}

// SharedFolders lists the folders an administrator has authorised for the app.
// It is the server-side counterpart of pickSharedFile and is the quickest way
// to tell whether the declared api-scope actually took effect.
func SharedFolders(ctx context.Context, appName string) ([]string, error) {
	token := os.Getenv("TRIM_API_TOKEN")
	if token == "" {
		return nil, errors.New("TRIM_API_TOKEN 未注入，无法调用开放 API")
	}
	payload, err := json.Marshal(map[string]any{
		"reqId":   fmt.Sprintf("%d", time.Now().UnixNano()),
		"req":     "trim.file.getSharedAccessibleFolders",
		"appName": appName,
		"data":    map[string]any{},
	})
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", SocketPath)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/api/v1/trimapp", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析开放 API 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK || out.Code != 0 {
		if out.Msg == "" {
			out.Msg = resp.Status
		}
		return nil, fmt.Errorf("开放 API 返回 %d: %s", out.Code, out.Msg)
	}

	var data struct {
		Paths []string `json:"paths"`
	}
	if len(out.Data) > 0 {
		if err := json.Unmarshal(out.Data, &data); err != nil {
			return nil, fmt.Errorf("解析授权目录失败: %w", err)
		}
	}
	return data.Paths, nil
}
