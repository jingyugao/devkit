package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"keeprun/internal/ipc"
	"keeprun/internal/paths"
	"keeprun/internal/task"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
}

func New() *Client {
	socketPath := paths.SocketPath()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		baseURL: "http://unix",
	}
}

func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeMaybeError(resp, nil)
}

func (c *Client) Status(ctx context.Context) (ipc.DaemonStatusResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/status", nil)
	if err != nil {
		return ipc.DaemonStatusResponse{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ipc.DaemonStatusResponse{}, err
	}
	defer resp.Body.Close()
	var out ipc.DaemonStatusResponse
	return out, decodeMaybeError(resp, &out)
}

func (c *Client) CreateTask(ctx context.Context, payload ipc.CreateTaskRequest) (task.Record, error) {
	return doJSON[ipc.CreateTaskRequest, ipc.StartStopResponse](ctx, c.httpClient, c.baseURL+"/tasks", http.MethodPost, payload)
}

func (c *Client) ListTasks(ctx context.Context, runningOnly bool) ([]task.Record, error) {
	u := c.baseURL + "/tasks"
	if runningOnly {
		u += "?state=running"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out ipc.ListTasksResponse
	if err := decodeMaybeError(resp, &out); err != nil {
		return nil, err
	}
	return out.Tasks, nil
}

func (c *Client) StartTask(ctx context.Context, ref string) (task.Record, error) {
	return doJSON[struct{}, ipc.StartStopResponse](ctx, c.httpClient, c.baseURL+"/tasks/"+url.PathEscape(ref)+"/start", http.MethodPost, struct{}{})
}

func (c *Client) StopTask(ctx context.Context, ref string) (task.Record, error) {
	return doJSON[struct{}, ipc.StartStopResponse](ctx, c.httpClient, c.baseURL+"/tasks/"+url.PathEscape(ref)+"/stop", http.MethodPost, struct{}{})
}

func (c *Client) RemoveTask(ctx context.Context, ref string, force bool) error {
	u := c.baseURL + "/tasks/" + url.PathEscape(ref)
	if force {
		u += "?force=1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeMaybeError(resp, nil)
}

func (c *Client) Logs(ctx context.Context, ref string, follow bool, lines int) (io.ReadCloser, error) {
	u := fmt.Sprintf("%s/tasks/%s/logs?lines=%d", c.baseURL, url.PathEscape(ref), lines)
	if follow {
		u += "&follow=1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var out ipc.ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err == nil && out.Error != "" {
			return nil, errors.New(out.Error)
		}
		return nil, fmt.Errorf("daemon returned %s", resp.Status)
	}
	return resp.Body, nil
}

func doJSON[Req any, Resp any](ctx context.Context, httpClient *http.Client, url string, method string, payload Req) (task.Record, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return task.Record{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(string(body)))
	if err != nil {
		return task.Record{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return task.Record{}, err
	}
	defer resp.Body.Close()
	var out Resp
	if err := decodeMaybeError(resp, &out); err != nil {
		return task.Record{}, err
	}
	switch converted := any(out).(type) {
	case ipc.StartStopResponse:
		return converted.Task, nil
	default:
		return task.Record{}, fmt.Errorf("unexpected response type")
	}
}

func decodeMaybeError(resp *http.Response, out any) error {
	if resp.StatusCode >= 400 {
		var apiErr ipc.ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err == nil && apiErr.Error != "" {
			return errors.New(apiErr.Error)
		}
		return fmt.Errorf("daemon returned %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
