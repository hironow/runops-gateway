package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
)

// VerifySignature checks the X-Slack-Signature header against the signing secret.
// Returns nil if valid, error otherwise.
func VerifySignature(header http.Header, body []byte, signingSecret string) error {
	timestamp := header.Get("X-Slack-Request-Timestamp")
	signature := header.Get("X-Slack-Signature")
	if timestamp == "" || signature == "" {
		return fmt.Errorf("missing slack signature headers")
	}
	basestring := "v0:" + timestamp + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(basestring))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("invalid slack signature")
	}
	return nil
}
