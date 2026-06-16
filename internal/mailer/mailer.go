package mailer

import (
	"fmt"
	"net/smtp"
	"strings"
)

// Mailer sends emails via SMTP.
type Mailer struct {
	host string
	port string
	user string
	pass string
	from string
}

// New returns a Mailer. host and port are required; user/pass may be empty for
// unauthenticated relays.
func New(host, port, user, pass, from string) *Mailer {
	if from == "" {
		from = "no-reply@" + host
	}
	return &Mailer{host: host, port: port, user: user, pass: pass, from: from}
}

// Available returns true when SMTP is configured.
func (m *Mailer) Available() bool {
	return m != nil && m.host != "" && m.port != ""
}

// Send sends a plain-text email.
func (m *Mailer) Send(to, subject, body string) error {
	if !m.Available() {
		return fmt.Errorf("mailer: SMTP not configured")
	}
	msg := buildMessage(m.from, to, subject, body)
	addr := m.host + ":" + m.port
	var auth smtp.Auth
	if m.user != "" {
		auth = smtp.PlainAuth("", m.user, m.pass, m.host)
	}
	return smtp.SendMail(addr, auth, m.from, []string{to}, []byte(msg))
}

func buildMessage(from, to, subject, body string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}
