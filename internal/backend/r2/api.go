package r2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func (b *Backend) request(ctx context.Context, method, path string, body []byte, contentType string) (*http.Response, error) {
	if err := b.Authenticate(ctx); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, b.apiBase+path, bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("construct R2 request failed")
		}
		req.Header.Set("Authorization", "Bearer "+b.token)
		req.Header.Set("Accept-Encoding", "identity")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		response, err := b.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("R2 request failed; check your network connection")
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return response, nil
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusUnauthorized && attempt < 2 && b.getenv("CLOUDFLARE_API_TOKEN") == "" {
			b.token = ""
			if attempt == 1 {
				if !b.interactive() {
					return nil, requestError(response.StatusCode)
				}
				if _, err := b.run(ctx, true, "login"); err != nil {
					return nil, err
				}
			}
			if err := b.Authenticate(ctx); err != nil {
				return nil, err
			}
			continue
		}
		return nil, requestError(response.StatusCode)
	}
	return nil, fmt.Errorf("%w: run `wrangler login` to refresh Cloudflare credentials", backend.ErrAuthentication)
}

func decodeResponse(response *http.Response, result any) error {
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if json.NewDecoder(response.Body).Decode(&envelope) != nil || !envelope.Success {
		return errors.New("invalid R2 API response; retry or check Cloudflare permissions")
	}
	if result != nil && json.Unmarshal(envelope.Result, result) != nil {
		return errors.New("invalid R2 API result")
	}
	return nil
}
