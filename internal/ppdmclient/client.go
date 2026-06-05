// Package ppdmclient is the per-server Dell PowerProtect Data Manager REST API client.
package ppdmclient

import "context"

// Client is the per-server PPDM API client abstraction, satisfied by the live
// ServerClient and by Mock (tests). Get authenticates lazily and decodes JSON.
type Client interface {
	// Name returns the configured server name (used as the `server` label).
	Name() string
	// Get fetches an absolute API path (e.g. "/api/v2/activities") and JSON-decodes
	// the body into out. It (re-)authenticates as needed.
	Get(ctx context.Context, path string, out any) error
	// Close releases the session and HTTP resources.
	Close() error
}
