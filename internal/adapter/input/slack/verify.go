package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// slackTimestampMaxSkew is the allowed window between the request's
// X-Slack-Request-Timestamp and the verifier's clock. Slack's own
// recommendation is ±5 minutes; outside this window the request is treated
// as a replay attempt. See ADR 0016.
const slackTimestampMaxSkew = 5 * time.Minute

// VerifySignature checks the X-Slack-Signature header against the signing secret
// and rejects requests whose timestamp is outside slackTimestampMaxSkew of now.
// Returns nil if valid, error otherwise.
func VerifySignature(header http.Header, body []byte, signingSecret string) error {
	return verifySignatureAt(time.Now(), header, body, signingSecret)
}

// verifySignatureAt is the testable form of VerifySignature: callers can
// inject the "current time" used for replay-window checks.
func verifySignatureAt(now time.Time, header http.Header, body []byte, signingSecret string) error {
	timestamp := header.Get("X-Slack-Request-Timestamp")
	signature := header.Get("X-Slack-Signature")
	if timestamp == "" || signature == "" {
		return fmt.Errorf("missing slack signature headers")
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid slack timestamp: %w", err)
	}
	skew := now.Sub(time.Unix(ts, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > slackTimestampMaxSkew {
		return fmt.Errorf("slack timestamp out of replay window: skew=%s", skew)
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
