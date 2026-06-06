// Package delivery sends rendered reports to recipients. Email (SMTP) is the only channel today;
// the Deliverer interface keeps file/webhook channels drop-in.
package delivery

import "context"

// Deliverer sends one tenant's rendered report to its recipients.
type Deliverer interface {
	Deliver(ctx context.Context, tenant string, to []string, subject string, html, pdf []byte) error
}
