//go:build !windows

package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql" // required by testcontainers Dolt module
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/dolt"
)

// DoltDockerImage is the Docker image used for Dolt test containers.
// DOLT_ROOT_HOST=% tells the entrypoint to create root@'%' (available
// since Dolt 1.46.0), which lets testcontainers connect via TCP.
const DoltDockerImage = "dolthub/dolt-sql-server:1.83.0"

var (
	doltCtr     *dolt.DoltContainer
	doltCtrOnce sync.Once
	doltCtrErr  error
	doltCtrPort string
	dockerOnce  sync.Once
	dockerAvail bool
)

func externalDoltPortFromEnv() string {
	if os.Getenv("GT_TEST_EXTERNAL_DOLT") == "" {
		return ""
	}
	for _, key := range []string{"GT_DOLT_PORT", "BEADS_DOLT_PORT"} {
		port := strings.TrimSpace(os.Getenv(key))
		if port == "" {
			continue
		}
		if _, err := strconv.Atoi(port); err == nil && externalDoltReachable(port) {
			return port
		}
	}
	return ""
}

func externalDoltReachable(port string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	dsn := fmt.Sprintf("root:@tcp(127.0.0.1:%s)/?timeout=2s&readTimeout=2s&writeTimeout=2s", port)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return false
	}
	defer db.Close()
	return db.PingContext(ctx) == nil
}

// isDockerAvailable returns true if the Docker daemon is reachable.
// The result is cached after the first call.
func isDockerAvailable() bool {
	dockerOnce.Do(func() {
		dockerAvail = exec.Command("docker", "info").Run() == nil
	})
	return dockerAvail
}

// isReaperRemovingErr returns true if the error is a transient "removing"
// status from the testcontainers Ryuk reaper. This happens when a previous
// test run's reaper container is still being cleaned up by Docker.
func isReaperRemovingErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unexpected container status") &&
		strings.Contains(err.Error(), "removing")
}

// runDoltContainerWithRetry calls dolt.Run, retrying on transient reaper
// "removing" errors up to 3 times with exponential backoff.
func runDoltContainerWithRetry(ctx context.Context) (*dolt.DoltContainer, error) {
	const maxRetries = 3
	delay := 2 * time.Second
	var lastErr error
	for attempt := range maxRetries {
		ctr, err := dolt.Run(ctx, DoltDockerImage,
			dolt.WithDatabase("gt_test"),
			testcontainers.WithEnv(map[string]string{"DOLT_ROOT_HOST": "%"}),
		)
		if err == nil {
			return ctr, nil
		}
		lastErr = err
		if !isReaperRemovingErr(err) {
			return nil, err
		}
		if attempt < maxRetries-1 {
			time.Sleep(delay)
			delay *= 2
		}
	}
	return nil, lastErr
}

// startSharedDoltContainer starts the shared Dolt container and sets
// GT_DOLT_PORT and BEADS_DOLT_PORT process-wide.
func startSharedDoltContainer() {
	if port := externalDoltPortFromEnv(); port != "" {
		doltCtrPort = port
		os.Setenv("GT_DOLT_PORT", port)         //nolint:tenv // intentional process-wide env
		os.Setenv("BEADS_DOLT_PORT", port)      //nolint:tenv // intentional process-wide env
		os.Setenv("GT_TEST_EXTERNAL_DOLT", "1") //nolint:tenv // borrowed server marker
		return
	}

	ctx := context.Background()
	ctr, err := runDoltContainerWithRetry(ctx)
	if err != nil {
		doltCtrErr = fmt.Errorf("starting Dolt container: %w", err)
		return
	}

	p, err := ctr.MappedPort(ctx, "3306/tcp")
	if err != nil {
		doltCtrErr = fmt.Errorf("getting mapped port: %w", err)
		_ = testcontainers.TerminateContainer(ctr)
		return
	}

	doltCtr = ctr
	doltCtrPort = p.Port()
	os.Setenv("GT_DOLT_PORT", doltCtrPort)    //nolint:tenv // intentional process-wide env
	os.Setenv("BEADS_DOLT_PORT", doltCtrPort) //nolint:tenv // intentional process-wide env
	os.Setenv("GT_TEST_EXTERNAL_DOLT", "1")   //nolint:tenv // integration tests reuse this container
}

// StartIsolatedDoltContainer starts a per-test Dolt container and returns the
// mapped host port. GT_DOLT_PORT is set via t.Setenv (scoped to the test).
// The container is terminated automatically when the test finishes.
func StartIsolatedDoltContainer(t *testing.T) string {
	t.Helper()
	if port := externalDoltPortFromEnv(); port != "" {
		t.Setenv("GT_DOLT_PORT", port)
		t.Setenv("BEADS_DOLT_PORT", port)
		t.Setenv("GT_TEST_EXTERNAL_DOLT", "1")
		return port
	}
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping test")
	}

	ctx := context.Background()
	ctr, err := runDoltContainerWithRetry(ctx)
	if err != nil {
		t.Fatalf("starting Dolt container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("terminating Dolt container: %v", err)
		}
	})

	port, err := ctr.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("getting mapped port: %v", err)
	}

	portStr := port.Port()
	t.Setenv("GT_DOLT_PORT", portStr)
	t.Setenv("BEADS_DOLT_PORT", portStr)
	t.Setenv("GT_TEST_EXTERNAL_DOLT", "1")
	return portStr
}

// EnsureDoltContainerForTestMain starts a shared Dolt container for use in
// TestMain functions. Call TerminateDoltContainer() after m.Run() to clean up.
// Sets both GT_DOLT_PORT and BEADS_DOLT_PORT process-wide.
func EnsureDoltContainerForTestMain() error {
	if externalDoltPortFromEnv() == "" && !isDockerAvailable() {
		return fmt.Errorf("Docker not available")
	}

	doltCtrOnce.Do(startSharedDoltContainer)
	return doltCtrErr
}

// RequireDoltContainer ensures a shared Dolt container is running. Skips the
// test if Docker is not available.
func RequireDoltContainer(t *testing.T) {
	t.Helper()
	if externalDoltPortFromEnv() == "" && !isDockerAvailable() {
		t.Skip("Docker not available, skipping test")
	}

	doltCtrOnce.Do(startSharedDoltContainer)
	if doltCtrErr != nil {
		t.Fatalf("Dolt container setup failed: %v", doltCtrErr)
	}
}

// DoltContainerAddr returns the address (host:port) of the Dolt container.
func DoltContainerAddr() string {
	return "127.0.0.1:" + doltCtrPort
}

// DoltContainerPort returns the mapped host port of the Dolt container.
func DoltContainerPort() string {
	return doltCtrPort
}

// TerminateDoltContainer stops and removes the shared Dolt container.
// Called from TestMain after m.Run().
func TerminateDoltContainer() {
	if doltCtr != nil {
		_ = testcontainers.TerminateContainer(doltCtr)
		doltCtr = nil
	}
}
