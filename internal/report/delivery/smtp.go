package delivery

import (
	"bytes"
	"context"
	"fmt"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/wneessen/go-mail"
)

// SMTP delivers reports as email (HTML body + PDF attachment) via go-mail.
type SMTP struct {
	client *mail.Client
	from   string
}

// NewSMTP builds an SMTP deliverer from config. STARTTLS is mandatory when cfg.StartTLS is set
// (port 587 style); otherwise TLS is disabled (e.g. a local demo sink). Auth is enabled only when
// a username is configured.
func NewSMTP(cfg config.SMTP) (*SMTP, error) {
	opts := []mail.Option{mail.WithPort(cfg.Port)}
	if cfg.StartTLS {
		opts = append(opts, mail.WithTLSPortPolicy(mail.TLSMandatory))
	} else {
		opts = append(opts, mail.WithTLSPortPolicy(mail.NoTLS))
	}
	if cfg.Username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(cfg.Username),
			mail.WithPassword(cfg.Password),
		)
	}
	client, err := mail.NewClient(cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("smtp client: %w", err)
	}
	return &SMTP{client: client, from: cfg.From}, nil
}

// Deliver composes and sends the email.
func (s *SMTP) Deliver(ctx context.Context, tenant string, to []string, subject string, html, pdf []byte) error {
	msg, err := buildMessage(s.from, subject, to, html, fmt.Sprintf("%s-report.pdf", tenant), pdf)
	if err != nil {
		return err
	}
	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// buildMessage assembles a multipart email: a short plain-text body, an HTML alternative (the
// rendered report), and the PDF as an attachment. Factored out so it can be asserted without a
// live SMTP server.
func buildMessage(from, subject string, to []string, html []byte, pdfName string, pdf []byte) (*mail.Msg, error) {
	m := mail.NewMsg()
	if err := m.From(from); err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	if err := m.To(to...); err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	m.Subject(subject)
	m.SetDate()
	m.SetMessageID()
	m.SetBodyString(mail.TypeTextPlain, "Your backup assurance report is attached; an HTML version is included in this message.")
	m.AddAlternativeString(mail.TypeTextHTML, string(html))
	if len(pdf) > 0 {
		if err := m.AttachReader(pdfName, bytes.NewReader(pdf)); err != nil {
			return nil, fmt.Errorf("attach pdf: %w", err)
		}
	}
	return m, nil
}
