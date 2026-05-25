package secret

import (
	"context"
	"errors"
	"testing"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"
)

// fakeSecretAccessor stands in for the *secretmanager.Client. The
// test exercises the fetcher's orchestration (name validation,
// payload extraction, error propagation) without depending on
// network or auth.
type fakeSecretAccessor struct {
	gotReq *secretmanagerpb.AccessSecretVersionRequest
	resp   *secretmanagerpb.AccessSecretVersionResponse
	err    error
}

func (f *fakeSecretAccessor) AccessSecretVersion(_ context.Context, req *secretmanagerpb.AccessSecretVersionRequest, _ ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	f.gotReq = req
	return f.resp, f.err
}

// Each malformed secret name is rejected at ctor time so a
// production misconfiguration surfaces at startup, not at first
// inbound broker request.
func TestNewSecretManagerPrivateKeyFetcherWithAccessor_RejectsInvalidName(t *testing.T) {
	cases := []string{
		"",
		"not-a-resource-name",
		"projects/p/secrets/s",
		"projects//secrets/s/versions/latest",
		"projects/p/secrets//versions/latest",
		"projects/p/secrets/s/versions/",
		"projects/p/secrets/s/versions/abc", // non-numeric, non-latest
		"PROJECTS/p/secrets/s/versions/latest",
	}
	for _, name := range cases {
		_, err := newSecretManagerPrivateKeyFetcherWithAccessor(&fakeSecretAccessor{}, name)
		if !errors.Is(err, ErrSecretNameInvalid) {
			t.Errorf("name=%q: want ErrSecretNameInvalid, got %v", name, err)
		}
	}
}

// Canonical resource names are accepted: "latest" and numeric versions.
func TestNewSecretManagerPrivateKeyFetcherWithAccessor_AcceptsValidName(t *testing.T) {
	for _, name := range []string{
		"projects/proj/secrets/github-app-key/versions/latest",
		"projects/proj/secrets/github-app-key/versions/1",
		"projects/proj/secrets/github-app-key/versions/12345",
	} {
		f, err := newSecretManagerPrivateKeyFetcherWithAccessor(&fakeSecretAccessor{}, name)
		if err != nil {
			t.Errorf("name=%q: %v", name, err)
		}
		if f == nil {
			t.Errorf("name=%q: nil fetcher", name)
		}
	}
}

// Happy path: AccessSecretVersion returns a non-empty payload →
// Fetch returns the bytes unchanged + the request carries the
// configured secret name.
func TestSecretManagerPrivateKeyFetcher_Fetch_HappyPath(t *testing.T) {
	want := []byte("-----BEGIN RSA PRIVATE KEY-----\nplaceholder\n-----END RSA PRIVATE KEY-----\n")
	accessor := &fakeSecretAccessor{
		resp: &secretmanagerpb.AccessSecretVersionResponse{
			Payload: &secretmanagerpb.SecretPayload{Data: want},
		},
	}
	f, err := newSecretManagerPrivateKeyFetcherWithAccessor(accessor, "projects/proj/secrets/key/versions/latest")
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	got, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if accessor.gotReq.Name != "projects/proj/secrets/key/versions/latest" {
		t.Errorf("AccessSecretVersion received name = %q", accessor.gotReq.Name)
	}
}

// Upstream gRPC error propagates so the composition root can
// distinguish "Secret Manager unreachable" from "secret missing"
// at the deployment-failure log.
func TestSecretManagerPrivateKeyFetcher_Fetch_UpstreamErrorPropagates(t *testing.T) {
	wantErr := errors.New("synthetic gRPC unavailable")
	f, _ := newSecretManagerPrivateKeyFetcherWithAccessor(&fakeSecretAccessor{err: wantErr}, "projects/proj/secrets/key/versions/latest")
	_, err := f.Fetch(context.Background())
	if !errors.Is(err, wantErr) {
		t.Errorf("want %v, got %v", wantErr, err)
	}
}

// Empty payload (= upstream returned but Data is nil) is rejected
// so the composition root never hands an empty PEM to ghinstallation.
func TestSecretManagerPrivateKeyFetcher_Fetch_EmptyPayloadRejected(t *testing.T) {
	cases := map[string]*secretmanagerpb.AccessSecretVersionResponse{
		"nil response":     nil,
		"nil payload":      {Payload: nil},
		"empty data slice": {Payload: &secretmanagerpb.SecretPayload{Data: nil}},
		"zero-byte data":   {Payload: &secretmanagerpb.SecretPayload{Data: []byte{}}},
	}
	for name, resp := range cases {
		f, _ := newSecretManagerPrivateKeyFetcherWithAccessor(&fakeSecretAccessor{resp: resp}, "projects/proj/secrets/key/versions/latest")
		_, err := f.Fetch(context.Background())
		if err == nil {
			t.Errorf("[%s] empty payload must be rejected, got nil", name)
		}
	}
}
