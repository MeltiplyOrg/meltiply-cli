package cli

import (
	"bytes"
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
)

// Error is the stable stderr envelope detail. IdempotencyKey lets callers retry
// an uncertain mutation with the same identity; no credentials belong here.
type Error struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Status         int    `json:"status,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	cause          error
}

// Error implements error without including headers or response bodies.
func (err *Error) Error() string { return err.Message }

// Unwrap preserves cancellation and transport identity without exposing the
// underlying error as an additional JSON field.
func (err *Error) Unwrap() error { return err.cause }

// installationOrigin rejects credentials, paths, and insecure remote hosts.
// HTTP is permitted only for localhost and literal loopback IPs in development.
func installationOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("server must be an absolute installation origin")
	}
	validPath := u.Path == "" || u.Path == "/"
	validURL := u.Hostname() != "" && u.User == nil && u.Opaque == ""
	hasSuffix := u.RawQuery != "" || u.ForceQuery || u.Fragment != ""
	if !validURL || !validPath || hasSuffix {
		return "", errors.New("server must be an origin without credentials, a path, query, or fragment")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		loopback := strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
		if u.Scheme != "http" || !loopback {
			return "", errors.New("server requires HTTPS; HTTP is allowed only for loopback development")
		}
	}
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	u.Path, u.RawPath = "", ""
	return u.String(), nil
}

// newHTTPClient bounds every request and refuses redirects, including same-host
// redirects, so session and API credentials never reach an unintended endpoint.
func newHTTPClient(source *http.Client) *http.Client {
	client := http.Client{Timeout: 30 * time.Second}
	if source != nil {
		client = *source
		if client.Timeout == 0 {
			client.Timeout = 30 * time.Second
		}
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

type httpRequest struct {
	method     string
	path       string
	credential string
	key        string
	body       any
}

type httpResult struct {
	body   json.RawMessage
	header http.Header
}

// request makes one bounded HTTP attempt. Mutations are never retried
// automatically; callers retain their idempotency key after an uncertain result.
func (app *application) request(ctx context.Context, input httpRequest) (httpResult, error) {
	var body io.Reader
	if input.body != nil {
		encoded, err := json.Marshal(input.body)
		if err != nil {
			return httpResult{}, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, input.method, app.server+input.path, body)
	if err != nil {
		return httpResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "melti-cli")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if input.credential != "" {
		req.Header.Set("Authorization", "Bearer "+input.credential)
	}
	if strings.HasPrefix(input.path, "/api/auth/") {
		req.Header.Set("Origin", app.server)
	}
	if input.key != "" {
		req.Header.Set("Idempotency-Key", input.key)
	}
	response, err := app.client.Do(req)
	if err != nil {
		return httpResult{}, fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()
	const maximumResponse = 8 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumResponse+1))
	if err != nil {
		return httpResult{}, fmt.Errorf("read response: %w", err)
	}
	if len(data) > maximumResponse {
		return httpResult{}, errors.New("server response exceeds 8 MiB; request a smaller page")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return httpResult{}, responseError(response, data)
	}
	if len(data) != 0 && !json.Valid(data) {
		return httpResult{}, errors.New("server returned invalid JSON")
	}
	return httpResult{body: data, header: response.Header}, nil
}

// responseError recognizes both core API envelopes and Better Auth's OAuth-style
// errors without ever printing arbitrary response text, such as an HTML proxy page.
func responseError(response *http.Response, data []byte) *Error {
	detail := &Error{
		Code: "http_error", Message: fmt.Sprintf("server returned HTTP %d", response.StatusCode),
		Status: response.StatusCode, RequestID: response.Header.Get("X-Request-ID"),
	}
	var envelope struct {
		Error            json.RawMessage `json:"error"`
		Code             string          `json:"code"`
		Message          string          `json:"message"`
		ErrorDescription string          `json:"error_description"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return detail
	}
	var nested struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	var code string
	switch {
	case json.Unmarshal(envelope.Error, &nested) == nil && nested.Code != "":
		detail.Code, detail.Message = nested.Code, nested.Message
		if nested.RequestID != "" {
			detail.RequestID = nested.RequestID
		}
	case json.Unmarshal(envelope.Error, &code) == nil && code != "":
		detail.Code = code
		if envelope.ErrorDescription != "" {
			detail.Message = envelope.ErrorDescription
		}
	case envelope.Code != "":
		detail.Code = envelope.Code
		if envelope.Message != "" {
			detail.Message = envelope.Message
		}
	}
	return detail
}
