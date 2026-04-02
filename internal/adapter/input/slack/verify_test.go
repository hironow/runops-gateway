package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
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
	timestamp := "1234567890"
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
	timestamp := "1234567890"
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
