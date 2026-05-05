package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func buildHeader(timestamp, signature string) http.Header {
	h := http.Header{}
	h.Set("X-Slack-Request-Timestamp", timestamp)
	h.Set("X-Slack-Signature", signature)
	return h
}

func validSignature(secret, timestamp string, body []byte) string {
	basestring := "v0:" + timestamp + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(basestring))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature_Valid(t *testing.T) {
	// given
	secret := "test-secret"
	// Use a current timestamp so the freshness check (ADR 0016) accepts the request.
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte("payload=test")
	sig := validSignature(secret, timestamp, body)
	header := buildHeader(timestamp, sig)

	// when
	err := VerifySignature(header, body, secret)

	// then
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestVerifySignature_InvalidMAC(t *testing.T) {
	// given
	secret := "test-secret"
	timestamp := "1234567890"
	body := []byte("payload=test")
	header := buildHeader(timestamp, "v0=invalidsignature")

	// when
	err := VerifySignature(header, body, secret)

	// then
	if err == nil {
		t.Error("expected error for invalid MAC, got nil")
	}
}

func TestVerifySignature_MissingHeaders(t *testing.T) {
	// given
	body := []byte("payload=test")
	header := http.Header{}
	header.Set("X-Slack-Signature", "v0=somesig")
	// timestamp is missing

	// when
	err := VerifySignature(header, body, "secret")

	// then
	if err == nil {
		t.Error("expected error for missing timestamp header, got nil")
	}
}

func TestVerifySignature_EmptyBody(t *testing.T) {
	// given — empty body is valid if signature matches
	secret := "test-secret"
	// Current timestamp so the freshness check (ADR 0016) accepts the request.
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte{}
	sig := validSignature(secret, timestamp, body)
	header := buildHeader(timestamp, sig)

	// when
	err := VerifySignature(header, body, secret)

	// then
	if err != nil {
		t.Errorf("expected no error for empty body with valid sig, got: %v", err)
	}
}

func TestVerifySignature_TamperedTimestamp(t *testing.T) {
	// given — signature was computed with one timestamp, request uses another
	secret := "test-secret"
	body := []byte("payload=test")
	sig := validSignature(secret, "1111111111", body) // signed with different timestamp
	header := buildHeader("9999999999", sig)          // but request claims different timestamp

	// when
	err := VerifySignature(header, body, secret)

	// then
	if err == nil {
		t.Error("expected error when timestamp was tampered, got nil")
	}
}

func TestVerifySignature_MissingSignature(t *testing.T) {
	// given
	body := []byte("payload=test")
	header := http.Header{}
	header.Set("X-Slack-Request-Timestamp", "1234567890")
	// signature is missing

	// when
	err := VerifySignature(header, body, "secret")

	// then
	if err == nil {
		t.Error("expected error for missing signature header, got nil")
	}
}

// --- replay protection tests (Issue 0019, ADR 0016) ---

func TestVerifySignatureAt_RejectsStaleTimestamp(t *testing.T) {
	// given — signature is correct for ts; verification clock is 6 minutes later.
	secret := "test-secret"
	body := []byte("payload=test")
	ts := int64(1700000000)
	tsStr := strconv.FormatInt(ts, 10)
	sig := validSignature(secret, tsStr, body)
	header := buildHeader(tsStr, sig)
	now := time.Unix(ts+6*60, 0)

	// when
	err := verifySignatureAt(now, header, body, secret)

	// then
	if err == nil {
		t.Fatal("expected stale-timestamp error, got nil")
	}
}

func TestVerifySignatureAt_RejectsFutureTimestamp(t *testing.T) {
	// given — request claims a timestamp 6 minutes ahead of verification clock.
	secret := "test-secret"
	body := []byte("payload=test")
	ts := int64(1700000000)
	tsStr := strconv.FormatInt(ts, 10)
	sig := validSignature(secret, tsStr, body)
	header := buildHeader(tsStr, sig)
	now := time.Unix(ts-6*60, 0)

	// when
	err := verifySignatureAt(now, header, body, secret)

	// then
	if err == nil {
		t.Fatal("expected future-timestamp error, got nil")
	}
}

func TestVerifySignatureAt_RejectsUnparseableTimestamp(t *testing.T) {
	// given — header value cannot be parsed as integer.
	secret := "test-secret"
	body := []byte("payload=test")
	header := buildHeader("not-a-number", "v0=anything")

	// when
	err := verifySignatureAt(time.Unix(1700000000, 0), header, body, secret)

	// then
	if err == nil {
		t.Fatal("expected parse error for non-numeric timestamp, got nil")
	}
}

func TestVerifySignatureAt_AcceptsWithinFiveMinuteWindow(t *testing.T) {
	cases := []struct {
		name      string
		skewSecs  int64 // negative = ts is in the past relative to now
	}{
		{"now", 0},
		{"ts 4 minutes old", 4 * 60},
		{"ts 4 minutes future", -4 * 60},
		{"ts 299 seconds old", 299},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secret := "test-secret"
			body := []byte("payload=test")
			ts := int64(1700000000)
			tsStr := strconv.FormatInt(ts, 10)
			sig := validSignature(secret, tsStr, body)
			header := buildHeader(tsStr, sig)
			now := time.Unix(ts+tc.skewSecs, 0)

			err := verifySignatureAt(now, header, body, secret)
			if err != nil {
				t.Errorf("expected accept within window, got: %v", err)
			}
		})
	}
}
