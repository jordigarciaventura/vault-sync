package r2

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

// requestError uses only HTTP status codes; response bodies may contain secrets.
func requestError(status int) error {
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%w: R2 object or bucket does not exist", backend.ErrNotFound)
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: run `wrangler login` or set a valid CLOUDFLARE_API_TOKEN", backend.ErrAuthentication)
	case http.StatusForbidden:
		return errors.New("R2 access denied; check Cloudflare account ID and R2 token permissions")
	case http.StatusConflict:
		return fmt.Errorf("%w: R2 bucket already exists; retry init or choose another name", backend.ErrContainerUnavailable)
	case http.StatusRequestEntityTooLarge:
		return errors.New("R2 upload exceeds the Cloudflare REST API limit; use a smaller file")
	default:
		return fmt.Errorf("R2 request failed (HTTP %d); retry or check Cloudflare service status", status)
	}
}
