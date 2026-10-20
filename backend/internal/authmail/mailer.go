package authmail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/textproto"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"
	"vpn/backend/internal/config"
)

// Message is one outbound email. MessageID stays fixed across retries.
type Message struct {
	To, MessageID string
	Rendered
}

// Mailer delivers one message. Implementations must honour ctx deadlines.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// DeliveryError carries a redacted failure class and whether a retry may help.
// It never includes server responses, addresses or credentials.
type DeliveryError struct {
	Class     string
	Temporary bool
}

func (e *DeliveryError) Error() string { return "mail delivery failed: " + e.Class }

// SMTPMailer sends through one authenticated SMTP account. Implicit TLS (465) or
// mandatory STARTTLS; certificates and host names are always verified, TLS >= 1.2,
// and there is no plaintext fallback.
type SMTPMailer struct {
	Host, Username, Password, FromName string
	Port                               int
	ImplicitTLS                        bool
	Timeout                            time.Duration
	// RootCAs overrides the system pool. Only tests set it.
	RootCAs *x509.CertPool
}

// NewSMTPMailer builds the production mailer from validated configuration.
func NewSMTPMailer(c config.Config) *SMTPMailer {
	return &SMTPMailer{Host: c.SMTPHost, Port: c.SMTPPort, ImplicitTLS: c.SMTPSecure, Username: c.SMTPUser, Password: c.SMTPPass, FromName: c.SMTPFromName, Timeout: SendTimeout}
}

func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = SendTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tlsConfig := &tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12, RootCAs: m.RootCAs}
	// The connection carries the context deadline as a hard I/O deadline, so a server
	// that accepts TCP and then stalls (greeting, TLS, DATA) cannot outlive the timeout.
	// A custom dialer replaces go-mail's own, so implicit TLS is performed here.
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: timeout}
		var conn net.Conn
		var err error
		if m.ImplicitTLS {
			conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, network, address)
		} else {
			conn, err = dialer.DialContext(ctx, network, address)
		}
		if err != nil {
			return nil, err
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		return conn, nil
	}
	options := []gomail.Option{
		gomail.WithPort(m.Port), gomail.WithTimeout(timeout), gomail.WithDialContextFunc(dial),
		// AUTH PLAIN (supported by QQ, 163, Gmail, Outlook) refuses to run unless the
		// connection is TLS-protected, whichever TLS mode established it.
		gomail.WithSMTPAuth(gomail.SMTPAuthPlain), gomail.WithUsername(m.Username), gomail.WithPassword(m.Password),
		gomail.WithTLSConfig(tlsConfig),
	}
	if m.ImplicitTLS {
		options = append(options, gomail.WithSSL())
	} else {
		options = append(options, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	client, err := gomail.NewClient(m.Host, options...)
	if err != nil {
		return &DeliveryError{Class: "client_config"}
	}
	message := gomail.NewMsg(gomail.WithNoDefaultUserAgent())
	// FromFormat encodes the display name (RFC 2047) and refuses header injection.
	if err = message.FromFormat(m.FromName, m.Username); err != nil {
		return &DeliveryError{Class: "sender_invalid"}
	}
	if err = message.To(msg.To); err != nil {
		return &DeliveryError{Class: "recipient_invalid"}
	}
	message.Subject(msg.Subject)
	message.SetMessageIDWithValue(msg.MessageID)
	message.SetDate()
	message.SetBodyString(gomail.TypeTextPlain, msg.Text)
	message.AddAlternativeString(gomail.TypeTextHTML, msg.HTML)
	if err = client.DialAndSendWithContext(ctx, message); err != nil {
		return classify(ctx, err)
	}
	return nil
}

// classify maps an SMTP/network error to a redacted class. 5xx replies are permanent;
// 4xx, timeouts and connection failures are temporary. TLS verification failures are
// configuration errors: retrying cannot help and nothing ever downgrades to plaintext.
func classify(ctx context.Context, err error) error {
	var proto *textproto.Error
	var sendErr *gomail.SendError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	var netErr net.Error
	text := strings.ToLower(err.Error())
	switch {
	case errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &invalid), errors.As(err, &verify):
		return &DeliveryError{Class: "tls_verification"}
	case strings.Contains(text, "starttls"):
		return &DeliveryError{Class: "starttls_unavailable"}
	case errors.As(err, &proto):
		class := "smtp_rejected"
		if strings.Contains(text, "auth") {
			class = "smtp_auth"
		}
		return &DeliveryError{Class: class, Temporary: proto.Code >= 400 && proto.Code < 500}
	case errors.As(err, &sendErr):
		return &DeliveryError{Class: "smtp_send", Temporary: sendErr.IsTemp()}
	case strings.Contains(text, "authenticat"):
		return &DeliveryError{Class: "smtp_auth"}
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout():
		return &DeliveryError{Class: "timeout", Temporary: true}
	case strings.Contains(text, "tls") || strings.Contains(text, "certificate") || strings.Contains(text, "handshake"):
		return &DeliveryError{Class: "tls_error"}
	default:
		return &DeliveryError{Class: "connection", Temporary: true}
	}
}
