package alerts

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/smtp"
	"strings"

	"git.jas.pe/vrepsaj/quptime/internal/config"
)

// sendSMTP delivers msg through the alert's SMTP relay. STARTTLS is
// negotiated whenever the alert has SMTPStartTLS true; the smtp
// server is responsible for advertising the extension.
func sendSMTP(a *config.Alert, msg Message) error {
	if a.SMTPHost == "" || a.SMTPPort == 0 {
		return errors.New("smtp host/port not set")
	}
	if a.SMTPFrom == "" || len(a.SMTPTo) == 0 {
		return errors.New("smtp from/to not set")
	}

	addr := fmt.Sprintf("%s:%d", a.SMTPHost, a.SMTPPort)
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	defer client.Close()

	if a.SMTPStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: a.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	if a.SMTPUser != "" {
		auth := smtp.PlainAuth("", a.SMTPUser, a.SMTPPassword, a.SMTPHost)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(a.SMTPFrom); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, rcpt := range a.SMTPTo {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("rcpt %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(buildRFC822(a.SMTPFrom, a.SMTPTo, msg)); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}
	return client.Quit()
}

func buildRFC822(from string, to []string, msg Message) []byte {
	var sb strings.Builder
	fmt.Fprintf(&sb, "From: %s\r\n", from)
	fmt.Fprintf(&sb, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&sb, "Subject: %s\r\n", msg.Subject)
	fmt.Fprintf(&sb, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&sb, "Content-Type: text/plain; charset=UTF-8\r\n")
	fmt.Fprintf(&sb, "\r\n")
	sb.WriteString(msg.Body)
	return []byte(sb.String())
}
