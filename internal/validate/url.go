// Package validate contains small, transport-independent input validators.
package validate

import (
	"errors"
	"net/url"
	"strings"
)

// WebhookURL accepts absolute HTTP(S) callback addresses. It deliberately does
// not reject private or local hosts: Taskboard is also used with self-hosted
// services in a private network. Credentials and fragments are not meaningful
// for callbacks and are rejected to avoid accidentally persisting secrets.
func WebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("webhook URL must be an absolute HTTP(S) address")
	}
	if u.User != nil {
		return errors.New("webhook URL must not contain credentials")
	}
	if u.Fragment != "" {
		return errors.New("webhook URL must not contain a fragment")
	}
	return nil
}
