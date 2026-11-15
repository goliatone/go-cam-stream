// Package cli is an HTTPS client of the single running controller. It never
// constructs a streaming service, command bus or backend.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type Client struct {
	origin string
	http   *http.Client
}

func NewClient(origin string, client *http.Client) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	if client == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, cs.NewError(cs.CodeInternal, nil)
		}
		transport := base.Clone()
		transport.Proxy = nil
		client = &http.Client{Transport: transport, Timeout: 30 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copyClient.Timeout <= 0 {
		copyClient.Timeout = 30 * time.Second
	}
	return &Client{origin: origin, http: &copyClient}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) List(ctx context.Context) (api.CameraList, error) {
	var out api.CameraList
	err := c.request(ctx, http.MethodGet, "/api/cameras", nil, "", &out)
	return out, err
}
func (c *Client) Status(ctx context.Context, camera, stream string) (any, error) {
	path := "/api/cameras/" + url.PathEscape(camera)
	if stream != "" {
		var out api.StreamStatus
		err := c.request(ctx, http.MethodGet, path+"/streams/"+url.PathEscape(stream)+"/status", nil, "", &out)
		return out, err
	}
	var out api.CameraStatus
	err := c.request(ctx, http.MethodGet, path+"/status", nil, "", &out)
	return out, err
}
func (c *Client) Diagnose(ctx context.Context, camera, stream string) (api.DiagnoseResponse, error) {
	var out api.DiagnoseResponse
	err := c.write(ctx, "/api/cameras/"+url.PathEscape(camera)+"/diagnose", api.DiagnoseRequest{StreamID: cs.StreamID(stream)}, &out)
	if err != nil {
		return out, err
	}
	for _, s := range out.Streams {
		for _, check := range s.Checks {
			if check.State == api.CheckFailed {
				code := cs.CodeSourceUnavailable
				if check.Error != nil {
					code = check.Error.Code
				}
				return out, cs.NewError(code, nil)
			}
		}
	}
	return out, nil
}
func (c *Client) Reconnect(ctx context.Context, camera, stream string) (api.ReconnectResponse, error) {
	var out api.ReconnectResponse
	err := c.write(ctx, "/api/cameras/"+url.PathEscape(camera)+"/streams/"+url.PathEscape(stream)+"/reconnect", nil, &out)
	return out, err
}
func (c *Client) write(ctx context.Context, path string, payload, out any) error {
	var token api.CSRFResponse
	if err := c.request(ctx, http.MethodGet, api.CSRFPath, nil, "", &token); err != nil {
		return err
	}
	if token.Token == "" || token.HeaderName != api.CSRFHeader {
		return cs.NewError(cs.CodeCSRFInvalid, nil)
	}
	return c.request(ctx, http.MethodPost, path, payload, token.Token, out)
}
func (c *Client) request(ctx context.Context, method, path string, payload any, token string, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return cs.NewError(cs.CodeInvalidArgument, nil)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, body)
	if err != nil {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(api.CSRFHeader, token)
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return cs.NewError(cs.CodeServerUnavailable, nil)
	}
	return decodeResponse(response, out)
}
func decodeResponse(response *http.Response, out any) error {
	defer response.Body.Close() //nolint:errcheck // Read/decode failures are returned; response close cannot alter server outcome.
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return cs.NewError(cs.CodeServerUnavailable, nil)
	}
	content, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		return cs.NewError(cs.CodeServerUnavailable, nil)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure api.ErrorResponse
		if json.Unmarshal(data, &failure) != nil {
			return cs.NewError(cs.CodeServerUnavailable, nil)
		}
		return cs.NewError(failure.Error.Code, nil)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return cs.NewError(cs.CodeServerUnavailable, nil)
	}
	return nil
}
