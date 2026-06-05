package ppdmclient

import (
	"context"
	"fmt"
	"strings"
)

// page is the PPDM pagination envelope.
type page[T any] struct {
	Content []T `json:"content"`
	Page    struct {
		Number     int `json:"number"`
		TotalPages int `json:"totalPages"`
	} `json:"page"`
}

// GetAll fetches every page of a PPDM list endpoint and returns the concatenated
// content. pageSize caps items per request; a 200-page safety bound prevents runaways.
func GetAll[T any](ctx context.Context, c Client, path string, pageSize int) ([]T, error) {
	// Append pagination params with the correct separator: collectors may pass a path
	// that already carries a query string (e.g. activities' ?filter=...).
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var all []T
	for n := 0; n < 200; n++ {
		var p page[T]
		url := fmt.Sprintf("%s%spage=%d&pageSize=%d", path, sep, n, pageSize)
		if err := c.Get(ctx, url, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Content...)
		if p.Page.TotalPages <= 0 || n >= p.Page.TotalPages-1 {
			break
		}
	}
	return all, nil
}
