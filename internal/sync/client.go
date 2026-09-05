package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type CredentialProvider func(context.Context) (string, error)

type Client struct {
	BaseURL    string
	DeviceID   string
	HTTP       *http.Client
	Credential CredentialProvider
}
type PushRequest struct {
	Scope    string              `json:"scope"`
	DeviceID string              `json:"device_id"`
	Objects  []domain.SyncObject `json:"objects"`
}
type PushResponse struct {
	AckedKeys []string              `json:"acked_keys"`
	Conflicts []domain.SyncConflict `json:"conflicts,omitempty"`
}
type PullResponse struct {
	Objects []domain.SyncObject `json:"objects"`
	Cursor  string              `json:"cursor"`
	HasMore bool                `json:"has_more"`
}
type RegisterResponse struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Token      string `json:"token"`
}

func (c *Client) Register(ctx context.Context, deviceName, code string) (RegisterResponse, error) {
	var response RegisterResponse
	err := c.do(ctx, http.MethodPost, "/v1/devices/register", map[string]string{"device_name": deviceName, "registration_code": code}, &response)
	return response, err
}

func (c *Client) RenameDevice(ctx context.Context, deviceName string) error {
	return c.do(ctx, http.MethodPatch, "/v1/devices/self", map[string]string{"device_name": deviceName}, nil)
}

func (c *Client) Push(ctx context.Context, request PushRequest) (PushResponse, error) {
	var response PushResponse
	err := c.do(ctx, http.MethodPost, "/v1/sync/push", request, &response)
	return response, err
}
func (c *Client) Pull(ctx context.Context, scope, cursor, deviceID string, limit int) (PullResponse, error) {
	query := url.Values{"scope": {scope}, "cursor": {cursor}, "device_id": {deviceID}, "limit": {fmt.Sprint(limit)}}
	var response PullResponse
	err := c.do(ctx, http.MethodGet, "/v1/sync/pull?"+query.Encode(), nil, &response)
	return response, err
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("parse sync endpoint: %w", err)
	}
	relative, err := url.Parse(path)
	if err != nil {
		return err
	}
	endpoint := base.ResolveReference(relative)
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.DeviceID != "" {
		req.Header.Set("X-Device-ID", c.DeviceID)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Credential != nil {
		credential, err := c.Credential(ctx)
		if err != nil {
			return fmt.Errorf("load sync credential: %w", err)
		}
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sync request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("sync server returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode sync response: %w", err)
	}
	return nil
}
