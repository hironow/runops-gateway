//go:build integration

// Package testutils provides self-contained integration test helpers.
//
// The absolute rule (2026-05-31): integration tests depend ONLY on
// testcontainers and run entirely inside the container it starts. They never
// rely on a locally running emulator (`just pubsub-up`), an external
// PUBSUB_EMULATOR_HOST, or a docker-compose-started service. TestMain in each
// integration package calls RunFirebaseEmulator, which builds the emulator
// image from the repo's docker/firebase-emulator/Dockerfile (no external
// registry dependency) and exposes Pub/Sub + Firestore on dynamic host ports.
package testutils

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// FirebaseProjectID is the project the emulator is started with. Tests use the
// same value for the Pub/Sub and Firestore clients.
const FirebaseProjectID = "runops-local"

// dockerfileContextDir resolves the build context (the repo's
// docker/firebase-emulator) from THIS file's own location via runtime.Caller,
// not from the test's working directory. testcontainers' FromDockerfile.Context
// is interpreted relative to the go test CWD (the calling package's dir), which
// differs per package (tests/integration vs internal/adapter/output/state), so a
// fixed relative path would resolve from one but not the other. The Dockerfile
// builds the firebase emulator suite (Pub/Sub + Firestore + hub).
func dockerfileContextDir() string {
	_, thisFile, _, _ := runtime.Caller(0) // tests/utils/firebase_emulator.go
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "docker", "firebase-emulator")
}

// FirebaseEmulator is a running firebase emulator container plus the dynamic
// host:port mappings tests inject into PUBSUB_EMULATOR_HOST /
// FIRESTORE_EMULATOR_HOST.
type FirebaseEmulator struct {
	Container     testcontainers.Container
	PubSubHost    string // host:port for PUBSUB_EMULATOR_HOST
	FirestoreHost string // host:port for FIRESTORE_EMULATOR_HOST
}

// RunFirebaseEmulator builds the emulator image from the repo Dockerfile and
// starts a container, returning the dynamic Pub/Sub + Firestore host:port.
//
// KeepImage avoids rebuilding the (slow) firebase image on every package's
// TestMain. The 4400 hub HTTP 200 only means the hub is up; the 9399/8080
// listeners can lag, so callers (InitPubSub / FirestoreReady) must retry the
// actual service connection before issuing real RPCs.
func RunFirebaseEmulator(ctx context.Context) (*FirebaseEmulator, error) {
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    dockerfileContextDir(),
				Dockerfile: "Dockerfile",
				KeepImage:  true,
			},
			ExposedPorts: []string{"9399/tcp", "8080/tcp", "4400/tcp"},
			Env: map[string]string{
				"FIREBASE_PROJECT_ID": FirebaseProjectID,
			},
			WaitingFor: wait.ForHTTP("/emulators").
				WithPort("4400/tcp").
				WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	}

	c, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("testutils: start firebase emulator: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("testutils: container host: %w", err)
	}
	psPort, err := c.MappedPort(ctx, "9399/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("testutils: map pubsub port: %w", err)
	}
	fsPort, err := c.MappedPort(ctx, "8080/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("testutils: map firestore port: %w", err)
	}

	return &FirebaseEmulator{
		Container:     c,
		PubSubHost:    net.JoinHostPort(host, psPort.Port()),
		FirestoreHost: net.JoinHostPort(host, fsPort.Port()),
	}, nil
}

// Terminate stops and removes the container.
func (e *FirebaseEmulator) Terminate(ctx context.Context) error {
	return e.Container.Terminate(ctx)
}
