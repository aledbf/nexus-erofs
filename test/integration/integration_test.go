// Package integration provides end-to-end tests for the nexus-erofs snapshotter.
//
// These tests verify the complete workflow of the snapshotter when used with
// containerd, including image pulling, snapshot operations, VMDK generation,
// and the commit lifecycle.
//
// Running tests:
//
//	go test -v ./test/integration/... -test.root
//
// These tests require:
//   - Root privileges (for mount operations)
//   - Linux kernel with EROFS support
//   - mkfs.erofs available in PATH
//   - containerd binary available in PATH
//
//go:build linux

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/opencontainers/go-digest"
)

// Test configuration constants.
const (
	// testNamespace is the containerd namespace for integration tests.
	testNamespace = "integration-test"

	// defaultTestImage is a small image for basic tests.
	defaultTestImage = "ghcr.io/containerd/alpine:3.14.0"

	// multiLayerImage is an image with multiple layers for VMDK tests.
	multiLayerImage = "docker.io/library/nginx:1.27-alpine"

	// snapshotterName is the name of the snapshotter under test.
	snapshotterName = "nexus-erofs"

	// serviceStartTimeout is the maximum time to wait for services to start.
	serviceStartTimeout = 30 * time.Second

	// imagePullTimeout is the maximum time to wait for image pulls.
	imagePullTimeout = 5 * time.Minute

	// mergedVMDKFile is the name of the VMDK descriptor file.
	mergedVMDKFile = "merged.vmdk"
)

// Environment manages the test environment including containerd and snapshotter.
type Environment struct {
	t *testing.T

	// Paths
	rootDir         string
	containerdRoot  string
	snapshotterRoot string
	logDir          string

	// Sockets
	containerdSocket  string
	snapshotterSocket string

	// Process management
	containerdPID  int
	snapshotterPID int

	// Client
	client *client.Client

	// Mutex for concurrent access
	mu sync.Mutex
}

// NewEnvironment creates a new test environment.
// It initializes directories but does not start services.
func NewEnvironment(t *testing.T) *Environment {
	t.Helper()
	testutil.RequiresRoot(t)

	rootDir := t.TempDir()

	env := &Environment{
		t:                 t,
		rootDir:           rootDir,
		containerdRoot:    filepath.Join(rootDir, "containerd"),
		snapshotterRoot:   filepath.Join(rootDir, "snapshotter"),
		logDir:            filepath.Join(rootDir, "logs"),
		containerdSocket:  filepath.Join(rootDir, "containerd.sock"),
		snapshotterSocket: filepath.Join(rootDir, "snapshotter.sock"),
	}

	// Create directories
	dirs := []string{env.containerdRoot, env.snapshotterRoot, env.logDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("create directory %s: %v", dir, err)
		}
	}

	return env
}

// Start starts containerd and the snapshotter.
func (e *Environment) Start() error {
	if err := e.writeContainerdConfig(); err != nil {
		return fmt.Errorf("write containerd config: %w", err)
	}

	if err := e.startSnapshotter(); err != nil {
		return fmt.Errorf("start snapshotter: %w", err)
	}

	if err := e.startContainerd(); err != nil {
		e.stopSnapshotter()
		return fmt.Errorf("start containerd: %w", err)
	}

	if err := e.connect(); err != nil {
		e.Stop()
		return fmt.Errorf("connect to containerd: %w", err)
	}

	return nil
}

// Stop stops all services and cleans up.
func (e *Environment) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.client != nil {
		e.client.Close()
		e.client = nil
	}

	e.stopContainerd()
	e.stopSnapshotter()
}

// Client returns the containerd client.
func (e *Environment) Client() *client.Client {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client
}

// Context returns a context with the test namespace.
func (e *Environment) Context() context.Context {
	return namespaces.WithNamespace(e.t.Context(), testNamespace)
}

// SnapshotService returns the snapshot service for the test snapshotter.
func (e *Environment) SnapshotService() snapshots.Snapshotter {
	return e.Client().SnapshotService(snapshotterName)
}

// SnapshotterRoot returns the root directory of the snapshotter.
func (e *Environment) SnapshotterRoot() string {
	return e.snapshotterRoot
}

// LogDir returns the directory containing service logs.
func (e *Environment) LogDir() string {
	return e.logDir
}

// writeContainerdConfig writes the containerd configuration file.
func (e *Environment) writeContainerdConfig() error {
	configPath := filepath.Join(e.rootDir, "containerd.toml")

	config := fmt.Sprintf(`version = 2
root = %q

[grpc]
  address = %q

[proxy_plugins]
  [proxy_plugins.nexus-erofs]
    type = "snapshot"
    address = %q

  [proxy_plugins.nexus-erofs-diff]
    type = "diff"
    address = %q

[plugins."io.containerd.service.v1.diff-service"]
  default = ["nexus-erofs-diff", "walking"]

[plugins."io.containerd.transfer.v1.local"]
  [[plugins."io.containerd.transfer.v1.local".unpack_config]]
    platform = "linux/amd64"
    snapshotter = "nexus-erofs"
    differ = "nexus-erofs-diff"

[plugins."io.containerd.cri.v1.images"]
  snapshotter = "nexus-erofs"
`, e.containerdRoot, e.containerdSocket, e.snapshotterSocket, e.snapshotterSocket)

	return os.WriteFile(configPath, []byte(config), 0644)
}

// startSnapshotter starts the nexus-erofs-snapshotter process.
func (e *Environment) startSnapshotter() error {
	binary, err := findBinary("nexus-erofs-snapshotter")
	if err != nil {
		return err
	}

	logFile, err := os.Create(filepath.Join(e.logDir, "snapshotter.log"))
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}

	cmd := exec.Command(binary,
		"--address", e.snapshotterSocket,
		"--root", e.snapshotterRoot,
		"--containerd-address", e.containerdSocket,
		"--log-level", "debug",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start snapshotter: %w", err)
	}

	e.snapshotterPID = cmd.Process.Pid
	e.t.Logf("snapshotter started (PID: %d)", e.snapshotterPID)

	// Wait for socket
	if err := waitForSocket(e.snapshotterSocket, serviceStartTimeout); err != nil {
		e.dumpLogs("snapshotter")
		return fmt.Errorf("wait for snapshotter socket: %w", err)
	}

	return nil
}

// startContainerd starts the containerd process.
func (e *Environment) startContainerd() error {
	binary, err := findBinary("containerd")
	if err != nil {
		return err
	}

	logFile, err := os.Create(filepath.Join(e.logDir, "containerd.log"))
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}

	configPath := filepath.Join(e.rootDir, "containerd.toml")
	cmd := exec.Command(binary, "--config", configPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start containerd: %w", err)
	}

	e.containerdPID = cmd.Process.Pid
	e.t.Logf("containerd started (PID: %d)", e.containerdPID)

	// Wait for socket
	if err := waitForSocket(e.containerdSocket, serviceStartTimeout); err != nil {
		e.dumpLogs("containerd")
		return fmt.Errorf("wait for containerd socket: %w", err)
	}

	return nil
}

// connect establishes a connection to containerd.
func (e *Environment) connect() error {
	ctx, cancel := context.WithTimeout(context.Background(), serviceStartTimeout)
	defer cancel()

	var c *client.Client
	var err error

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout connecting to containerd: %w", err)
		default:
			c, err = client.New(e.containerdSocket)
			if err == nil {
				// Verify connection
				if _, err = c.Version(ctx); err == nil {
					e.client = c
					return nil
				}
				c.Close()
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// stopContainerd stops the containerd process.
func (e *Environment) stopContainerd() {
	if e.containerdPID == 0 {
		return
	}

	proc, err := os.FindProcess(e.containerdPID)
	if err != nil {
		return
	}

	_ = proc.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		proc.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = proc.Kill()
	}

	e.containerdPID = 0
}

// stopSnapshotter stops the snapshotter process.
func (e *Environment) stopSnapshotter() {
	if e.snapshotterPID == 0 {
		return
	}

	proc, err := os.FindProcess(e.snapshotterPID)
	if err != nil {
		return
	}

	_ = proc.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		proc.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = proc.Kill()
	}

	e.snapshotterPID = 0
}

// dumpLogs prints the last N lines of a service log.
func (e *Environment) dumpLogs(service string) {
	logPath := filepath.Join(e.logDir, service+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		e.t.Logf("failed to read %s logs: %v", service, err)
		return
	}

	lines := strings.Split(string(data), "\n")
	start := 0
	if len(lines) > 50 {
		start = len(lines) - 50
	}

	e.t.Logf("=== %s logs (last %d lines) ===", service, len(lines)-start)
	for _, line := range lines[start:] {
		e.t.Log(line)
	}
}

// findBinary locates a binary in PATH or common locations.
func findBinary(name string) (string, error) {
	// Check PATH first
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}

	// Check common locations
	locations := []string{
		"/usr/local/bin/" + name,
		"/usr/bin/" + name,
		"./bin/" + name,
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc, nil
		}
	}

	return "", fmt.Errorf("binary not found: %s", name)
}

// waitForSocket waits for a Unix socket to become available.
func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket not available: %s", path)
}

// pullImage pulls an image with retry logic.
func pullImage(ctx context.Context, c *client.Client, ref string) error {
	ctx, cancel := context.WithTimeout(ctx, imagePullTimeout)
	defer cancel()

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		_, err := c.Pull(ctx, ref,
			client.WithPlatform("linux/amd64"),
			client.WithPullUnpack,
			client.WithPullSnapshotter(snapshotterName),
		)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}
	return fmt.Errorf("pull image after 3 attempts: %w", lastErr)
}

// ============================================================================
// Tests
// ============================================================================

// TestIntegration runs the integration test suite.
func TestIntegration(t *testing.T) {
	testutil.RequiresRoot(t)

	// Check prerequisites
	if err := checkPrerequisites(); err != nil {
		t.Skipf("prerequisites not met: %v", err)
	}

	// Create environment
	env := NewEnvironment(t)
	t.Cleanup(func() {
		env.Stop()
		env.dumpLogs("snapshotter")
		env.dumpLogs("containerd")
	})

	// Start services
	if err := env.Start(); err != nil {
		t.Fatalf("start environment: %v", err)
	}

	// Run health checks
	t.Run("health_check", func(t *testing.T) {
		testHealthCheck(t, env)
	})

	// Run tests in order (some depend on previous tests)
	t.Run("pull_image", func(t *testing.T) {
		testPullImage(t, env)
	})

	t.Run("prepare_snapshot", func(t *testing.T) {
		testPrepareSnapshot(t, env)
	})

	t.Run("view_snapshot", func(t *testing.T) {
		testViewSnapshot(t, env)
	})

	t.Run("erofs_layers", func(t *testing.T) {
		testErofsLayers(t, env)
	})

	t.Run("rwlayer_creation", func(t *testing.T) {
		testRwlayerCreation(t, env)
	})

	t.Run("commit", func(t *testing.T) {
		testCommit(t, env)
	})

	t.Run("snapshot_cleanup", func(t *testing.T) {
		testSnapshotCleanup(t, env)
	})

	// Multi-layer tests
	t.Run("multi_layer", func(t *testing.T) {
		testMultiLayer(t, env)
	})

	t.Run("vmdk_format", func(t *testing.T) {
		testVMDKFormat(t, env)
	})

	t.Run("vmdk_layer_order", func(t *testing.T) {
		testVMDKLayerOrder(t, env)
	})

	// Lifecycle test
	t.Run("commit_lifecycle", func(t *testing.T) {
		testCommitLifecycle(t, env)
	})

	// Cleanup test (runs last)
	t.Run("full_cleanup", func(t *testing.T) {
		testFullCleanup(t, env)
	})
}

// checkPrerequisites verifies that required tools are available.
func checkPrerequisites() error {
	required := []string{"containerd", "mkfs.erofs"}
	for _, bin := range required {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found in PATH", bin)
		}
	}

	// Check for nexus-erofs-snapshotter
	if _, err := findBinary("nexus-erofs-snapshotter"); err != nil {
		return err
	}

	return nil
}

// testHealthCheck verifies the services are running and responsive.
func testHealthCheck(t *testing.T, env *Environment) {
	ctx := env.Context()
	c := env.Client()

	// Check containerd version
	v, err := c.Version(ctx)
	if err != nil {
		t.Fatalf("get containerd version: %v", err)
	}
	t.Logf("containerd version: %s", v.Version)

	// Check snapshotter is accessible
	ss := env.SnapshotService()
	if err := ss.Walk(ctx, func(_ context.Context, _ snapshots.Info) error {
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}
	t.Log("snapshotter is accessible")

	// Check disk space
	var stat syscall.Statfs_t
	if err := syscall.Statfs(env.SnapshotterRoot(), &stat); err != nil {
		t.Fatalf("statfs: %v", err)
	}
	availGB := (stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024 * 1024)
	t.Logf("available disk space: %d GB", availGB)
	if availGB < 1 {
		t.Log("WARNING: low disk space may cause issues")
	}
}

// testPullImage verifies that pulling an image creates snapshots.
func testPullImage(t *testing.T, env *Environment) {
	ctx := env.Context()
	c := env.Client()

	if err := pullImage(ctx, c, defaultTestImage); err != nil {
		t.Fatalf("pull image: %v", err)
	}

	// Verify image exists
	img, err := c.GetImage(ctx, defaultTestImage)
	if err != nil {
		t.Fatalf("get image: %v", err)
	}
	t.Logf("image pulled: %s", img.Name())

	// Verify snapshots were created
	ss := env.SnapshotService()
	var count int
	if err := ss.Walk(ctx, func(_ context.Context, _ snapshots.Info) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}

	if count == 0 {
		t.Fatal("expected at least one snapshot after pull")
	}
	t.Logf("snapshots created: %d", count)
}

// testPrepareSnapshot verifies snapshot preparation from a committed parent.
func testPrepareSnapshot(t *testing.T, env *Environment) {
	ctx := env.Context()
	ss := env.SnapshotService()

	// Find a committed snapshot
	var parentKey string
	if err := ss.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		if info.Kind == snapshots.KindCommitted && parentKey == "" {
			parentKey = info.Name
		}
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}

	if parentKey == "" {
		t.Fatal("no committed snapshot found")
	}

	// Prepare an active snapshot
	snapKey := fmt.Sprintf("test-prepare-%d", time.Now().UnixNano())
	mounts, err := ss.Prepare(ctx, snapKey, parentKey)
	if err != nil {
		t.Fatalf("prepare snapshot: %v", err)
	}
	t.Cleanup(func() {
		ss.Remove(ctx, snapKey)
	})

	if len(mounts) == 0 {
		t.Fatal("prepare returned no mounts")
	}
	t.Logf("prepared snapshot with %d mounts", len(mounts))

	// Verify snapshot info
	info, err := ss.Stat(ctx, snapKey)
	if err != nil {
		t.Fatalf("stat snapshot: %v", err)
	}

	if info.Kind != snapshots.KindActive {
		t.Errorf("expected KindActive, got %v", info.Kind)
	}
}

// testViewSnapshot verifies view snapshot creation and mount info.
func testViewSnapshot(t *testing.T, env *Environment) {
	ctx := env.Context()
	ss := env.SnapshotService()

	// Find a committed snapshot
	var parentKey string
	if err := ss.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		if info.Kind == snapshots.KindCommitted && parentKey == "" {
			parentKey = info.Name
		}
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}

	if parentKey == "" {
		t.Fatal("no committed snapshot found")
	}

	// Create a view
	viewKey := fmt.Sprintf("test-view-%d", time.Now().UnixNano())
	mounts, err := ss.View(ctx, viewKey, parentKey)
	if err != nil {
		t.Fatalf("create view: %v", err)
	}
	t.Cleanup(func() {
		ss.Remove(ctx, viewKey)
	})

	if len(mounts) == 0 {
		t.Fatal("view returned no mounts")
	}

	// Verify mount contains EROFS reference
	hasErofs := false
	for _, m := range mounts {
		if strings.Contains(m.Type, "erofs") || strings.Contains(m.Source, ".erofs") {
			hasErofs = true
			break
		}
	}
	if !hasErofs {
		t.Errorf("expected EROFS mount, got: %+v", mounts)
	}

	// Verify snapshot info
	info, err := ss.Stat(ctx, viewKey)
	if err != nil {
		t.Fatalf("stat view: %v", err)
	}

	if info.Kind != snapshots.KindView {
		t.Errorf("expected KindView, got %v", info.Kind)
	}
}

// testErofsLayers verifies EROFS layer files are created correctly.
func testErofsLayers(t *testing.T, env *Environment) {
	snapshotsDir := filepath.Join(env.SnapshotterRoot(), "snapshots")

	// Find EROFS layer files (exclude fsmeta.erofs which has different structure)
	var erofsFiles []string
	if err := filepath.Walk(snapshotsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".erofs") && !info.IsDir() {
			// fsmeta.erofs files are multi-device metadata, not standard EROFS images
			if filepath.Base(path) != "fsmeta.erofs" {
				erofsFiles = append(erofsFiles, path)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots dir: %v", err)
	}

	if len(erofsFiles) == 0 {
		// If no layer files, check if fsmeta.erofs exists (multi-device setup)
		var hasFsmeta bool
		filepath.Walk(snapshotsDir, func(path string, info os.FileInfo, err error) error { //nolint:errcheck
			if err == nil && filepath.Base(path) == "fsmeta.erofs" {
				hasFsmeta = true
				t.Logf("found fsmeta.erofs: %s", path)
			}
			return nil
		})
		if hasFsmeta {
			t.Log("no individual layer files, but fsmeta.erofs present (multi-device mode)")
			return
		}
		t.Fatal("no EROFS files found")
	}
	t.Logf("found %d EROFS layer files", len(erofsFiles))

	// Verify at least one has valid EROFS magic
	for _, path := range erofsFiles {
		err := verifyErofsMagic(path)
		if err == nil {
			t.Logf("verified EROFS magic in: %s", filepath.Base(path))
			return
		}
		t.Logf("file %s: %v", filepath.Base(path), err)
	}
	t.Error("no EROFS layer file with valid magic found")
}

// verifyErofsMagic checks if a file has the EROFS magic bytes.
func verifyErofsMagic(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// EROFS magic is at offset 1024, value 0xE0F5E1E2 (little-endian)
	if _, err := f.Seek(1024, io.SeekStart); err != nil {
		return err
	}

	var magic uint32
	if err := binary.Read(f, binary.LittleEndian, &magic); err != nil {
		return err
	}

	if magic != 0xE2E1F5E0 {
		return fmt.Errorf("invalid magic: %#x", magic)
	}
	return nil
}

// testRwlayerCreation verifies rwlayer.img files are created for active snapshots.
func testRwlayerCreation(t *testing.T, env *Environment) {
	ctx := env.Context()
	ss := env.SnapshotService()

	// Find a committed snapshot
	var parentKey string
	if err := ss.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		if info.Kind == snapshots.KindCommitted && parentKey == "" {
			parentKey = info.Name
		}
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}

	// Create active snapshot
	snapKey := fmt.Sprintf("test-rwlayer-%d", time.Now().UnixNano())
	_, err := ss.Prepare(ctx, snapKey, parentKey)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	t.Cleanup(func() {
		ss.Remove(ctx, snapKey)
	})

	// Look for rwlayer.img
	snapshotsDir := filepath.Join(env.SnapshotterRoot(), "snapshots")
	var found bool
	filepath.Walk(snapshotsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && filepath.Base(path) == "rwlayer.img" {
			found = true
			t.Logf("found rwlayer.img: %s (%d bytes)", path, info.Size())
		}
		return nil
	})

	if !found {
		t.Error("no rwlayer.img found after prepare")
	}
}

// testCommit verifies snapshot commit creates EROFS layers.
func testCommit(t *testing.T, env *Environment) {
	ctx := env.Context()
	ss := env.SnapshotService()

	// Find a committed snapshot
	var parentKey string
	if err := ss.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		if info.Kind == snapshots.KindCommitted && parentKey == "" {
			parentKey = info.Name
		}
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}

	// Create an extract snapshot (triggers host mounting)
	ts := time.Now().UnixNano()
	extractKey := fmt.Sprintf("extract-test-commit-%d", ts)
	_, err := ss.Prepare(ctx, extractKey, parentKey)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// Commit it
	commitKey := fmt.Sprintf("committed-test-%d", ts)
	if err := ss.Commit(ctx, commitKey, extractKey); err != nil {
		t.Fatalf("commit: %v", err)
	}
	t.Cleanup(func() {
		ss.Remove(ctx, commitKey)
	})

	// Verify committed snapshot exists
	info, err := ss.Stat(ctx, commitKey)
	if err != nil {
		t.Fatalf("stat committed: %v", err)
	}

	if info.Kind != snapshots.KindCommitted {
		t.Errorf("expected KindCommitted, got %v", info.Kind)
	}
	t.Logf("snapshot committed: %s", commitKey)
}

// testSnapshotCleanup verifies snapshots can be properly removed.
func testSnapshotCleanup(t *testing.T, env *Environment) {
	ctx := env.Context()
	ss := env.SnapshotService()

	// Find a committed parent
	var parentKey string
	if err := ss.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		if info.Kind == snapshots.KindCommitted && parentKey == "" {
			parentKey = info.Name
		}
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}

	if parentKey == "" {
		t.Skip("no committed snapshot found to use as parent")
	}
	t.Logf("using parent: %s", parentKey)

	// Create snapshot for cleanup test
	snapKey := fmt.Sprintf("test-cleanup-%d", time.Now().UnixNano())
	mounts, err := ss.Prepare(ctx, snapKey, parentKey)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	t.Logf("prepared snapshot %s with %d mounts", snapKey, len(mounts))

	// Verify it exists
	info, err := ss.Stat(ctx, snapKey)
	if err != nil {
		t.Fatalf("stat before remove: %v", err)
	}
	t.Logf("snapshot exists: kind=%v, parent=%s", info.Kind, info.Parent)

	// Remove it
	if err := ss.Remove(ctx, snapKey); err != nil {
		// Log all snapshots for debugging
		t.Logf("remove failed, listing all snapshots:")
		ss.Walk(ctx, func(_ context.Context, si snapshots.Info) error { //nolint:errcheck
			t.Logf("  - %s (kind=%v, parent=%s)", si.Name, si.Kind, si.Parent)
			return nil
		})
		t.Fatalf("remove: %v", err)
	}

	// Verify it's gone
	_, err = ss.Stat(ctx, snapKey)
	if err == nil {
		t.Fatal("snapshot still exists after remove")
	}
	t.Log("snapshot removed successfully")
}

// testMultiLayer tests pulling and viewing a multi-layer image.
func testMultiLayer(t *testing.T, env *Environment) {
	ctx := env.Context()
	c := env.Client()
	ss := env.SnapshotService()

	// Pull multi-layer image
	if err := pullImage(ctx, c, multiLayerImage); err != nil {
		t.Fatalf("pull multi-layer image: %v", err)
	}

	// Count snapshots (should have multiple)
	var count int
	var topSnap string
	if err := ss.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		count++
		if info.Kind == snapshots.KindCommitted {
			topSnap = info.Name
		}
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}

	if count < 2 {
		t.Fatalf("expected multiple snapshots for multi-layer image, got %d", count)
	}
	t.Logf("multi-layer image created %d snapshots", count)

	// Create a view to trigger VMDK generation
	viewKey := fmt.Sprintf("test-multi-view-%d", time.Now().UnixNano())
	_, err := ss.View(ctx, viewKey, topSnap)
	if err != nil {
		t.Fatalf("create view: %v", err)
	}
	t.Cleanup(func() {
		ss.Remove(ctx, viewKey)
	})

	// Give time for VMDK generation
	time.Sleep(500 * time.Millisecond)

	// Look for VMDK files
	snapshotsDir := filepath.Join(env.SnapshotterRoot(), "snapshots")
	var vmdkCount int
	filepath.Walk(snapshotsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && filepath.Base(path) == mergedVMDKFile {
			vmdkCount++
			t.Logf("found VMDK: %s", path)
		}
		return nil
	})

	if vmdkCount == 0 {
		t.Error("no VMDK files generated for multi-layer image")
	}
}

// testVMDKFormat verifies VMDK descriptor format is valid.
func testVMDKFormat(t *testing.T, env *Environment) {
	snapshotsDir := filepath.Join(env.SnapshotterRoot(), "snapshots")

	// Find a VMDK file
	var vmdkPath string
	filepath.Walk(snapshotsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && filepath.Base(path) == mergedVMDKFile && vmdkPath == "" {
			vmdkPath = path
		}
		return nil
	})

	if vmdkPath == "" {
		t.Skip("no VMDK file found")
	}

	// Read and validate VMDK
	data, err := os.ReadFile(vmdkPath)
	if err != nil {
		t.Fatalf("read VMDK: %v", err)
	}

	content := string(data)

	// Check required fields
	requiredFields := []string{"version=", "CID=", "createType="}
	for _, field := range requiredFields {
		if !strings.Contains(content, field) {
			t.Errorf("VMDK missing required field: %s", field)
		}
	}

	// Check extent format
	extentPattern := regexp.MustCompile(`RW\s+\d+\s+FLAT\s+"[^"]+"\s+\d+`)
	if !extentPattern.MatchString(content) {
		t.Error("VMDK has no valid extent definitions")
	}

	t.Logf("VMDK format validated: %s", vmdkPath)
}

// testVMDKLayerOrder verifies VMDK layers are in correct order.
func testVMDKLayerOrder(t *testing.T, env *Environment) {
	snapshotsDir := filepath.Join(env.SnapshotterRoot(), "snapshots")

	// Find VMDK with multiple layers
	vmdkPath, maxLayers := findVMDKWithMostLayers(snapshotsDir)

	if vmdkPath == "" || maxLayers < 2 {
		t.Skip("no multi-layer VMDK found")
	}

	// Parse VMDK
	vmdkLayers, err := parseVMDKLayers(vmdkPath)
	if err != nil {
		t.Fatalf("parse VMDK: %v", err)
	}

	// Verify fsmeta is first
	if len(vmdkLayers) > 0 && !strings.Contains(vmdkLayers[0], "fsmeta.erofs") {
		t.Errorf("first layer should be fsmeta.erofs, got: %s", vmdkLayers[0])
	}

	// Read manifest file
	manifestPath := filepath.Join(filepath.Dir(vmdkPath), "layers.manifest")
	manifestDigests, err := readLayersManifest(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	// Extract VMDK digests
	var vmdkDigests []string
	for _, layer := range vmdkLayers {
		if d := extractDigest(layer); d != "" {
			vmdkDigests = append(vmdkDigests, d)
		}
	}

	// Compare order
	if len(vmdkDigests) != len(manifestDigests) {
		t.Errorf("layer count mismatch: VMDK=%d, manifest=%d", len(vmdkDigests), len(manifestDigests))
	}

	for i := 0; i < len(vmdkDigests) && i < len(manifestDigests); i++ {
		if vmdkDigests[i] != manifestDigests[i] {
			t.Errorf("layer order mismatch at position %d: VMDK=%s, manifest=%s",
				i, vmdkDigests[i][:12], manifestDigests[i][:12])
		}
	}

	t.Logf("VMDK layer order verified (%d layers)", len(vmdkDigests))
}

// parseVMDKLayers extracts layer paths from a VMDK descriptor.
func parseVMDKLayers(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var layers []string
	scanner := bufio.NewScanner(f)
	extentPattern := regexp.MustCompile(`RW\s+\d+\s+FLAT\s+"([^"]+)"`)

	for scanner.Scan() {
		if matches := extentPattern.FindStringSubmatch(scanner.Text()); len(matches) > 1 {
			layers = append(layers, matches[1])
		}
	}
	return layers, scanner.Err()
}

// readLayersManifest reads digests from a layers.manifest file.
func readLayersManifest(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var digests []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "sha256:") {
			digests = append(digests, strings.TrimPrefix(line, "sha256:"))
		}
	}
	return digests, scanner.Err()
}

// extractDigest extracts a sha256 digest from a layer path.
func extractDigest(path string) string {
	pattern := regexp.MustCompile(`sha256-([a-f0-9]{64})\.erofs`)
	if matches := pattern.FindStringSubmatch(path); len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// testCommitLifecycle tests the full container commit workflow.
func testCommitLifecycle(t *testing.T, env *Environment) {
	ctx := env.Context()
	ss := env.SnapshotService()

	// Find a committed parent
	var parentKey string
	if err := ss.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		if info.Kind == snapshots.KindCommitted && parentKey == "" {
			parentKey = info.Name
		}
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}

	// Create active snapshot
	ts := time.Now().UnixNano()
	activeKey := fmt.Sprintf("lifecycle-active-%d", ts)
	mounts, err := ss.Prepare(ctx, activeKey, parentKey)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	t.Cleanup(func() {
		ss.Remove(ctx, activeKey)
	})

	// Find ext4 rwlayer path
	var rwlayerPath string
	for _, m := range mounts {
		if m.Type == "ext4" {
			rwlayerPath = m.Source
			break
		}
	}

	if rwlayerPath == "" {
		t.Skip("no ext4 mount returned, skipping lifecycle test")
	}

	// Mount ext4, write data, unmount
	mountPoint := t.TempDir()
	if err := mountExt4(rwlayerPath, mountPoint); err != nil {
		t.Fatalf("mount ext4: %v", err)
	}

	// Write test data
	upperDir := filepath.Join(mountPoint, "upper")
	if err := os.MkdirAll(upperDir, 0755); err != nil {
		unmountExt4(mountPoint)
		t.Fatalf("create upper dir: %v", err)
	}

	testFile := filepath.Join(upperDir, "lifecycle-test.bin")
	testData := bytes.Repeat([]byte("x"), 1024*1024) // 1MB
	if err := os.WriteFile(testFile, testData, 0644); err != nil {
		unmountExt4(mountPoint)
		t.Fatalf("write test file: %v", err)
	}

	if err := unmountExt4(mountPoint); err != nil {
		t.Fatalf("unmount ext4: %v", err)
	}

	// Commit
	commitKey := fmt.Sprintf("lifecycle-commit-%d", ts)
	if err := ss.Commit(ctx, commitKey, activeKey); err != nil {
		t.Fatalf("commit: %v", err)
	}
	t.Cleanup(func() {
		ss.Remove(ctx, commitKey)
	})

	// Verify committed snapshot
	info, err := ss.Stat(ctx, commitKey)
	if err != nil {
		t.Fatalf("stat committed: %v", err)
	}

	if info.Kind != snapshots.KindCommitted {
		t.Errorf("expected KindCommitted, got %v", info.Kind)
	}

	t.Log("commit lifecycle test passed")
}

// mountExt4 mounts an ext4 image at the given path.
func mountExt4(image, target string) error {
	cmd := exec.Command("mount", "-o", "loop", image, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount: %s: %w", string(out), err)
	}
	return nil
}

// unmountExt4 unmounts an ext4 filesystem.
func unmountExt4(target string) error {
	_ = exec.Command("sync").Run()
	cmd := exec.Command("umount", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("umount: %s: %w", string(out), err)
	}
	return nil
}

// testFullCleanup verifies all resources are cleaned up properly.
func testFullCleanup(t *testing.T, env *Environment) {
	ctx := env.Context()
	c := env.Client()
	ss := env.SnapshotService()

	// Remove all images
	imgService := c.ImageService()
	imgs, err := imgService.List(ctx)
	if err != nil {
		t.Fatalf("list images: %v", err)
	}

	for _, img := range imgs {
		if err := imgService.Delete(ctx, img.Name); err != nil {
			t.Logf("delete image %s: %v", img.Name, err)
		}
	}

	// Remove all snapshots (in reverse order to handle dependencies)
	var keys []string
	if err := ss.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		keys = append(keys, info.Name)
		return nil
	}); err != nil {
		t.Fatalf("walk snapshots: %v", err)
	}

	for i := len(keys) - 1; i >= 0; i-- {
		if err := ss.Remove(ctx, keys[i]); err != nil {
			t.Logf("remove snapshot %s: %v", keys[i], err)
		}
	}

	// Wait for cleanup
	time.Sleep(time.Second)

	// Verify no snapshots remain
	var remaining int
	ss.Walk(ctx, func(_ context.Context, _ snapshots.Info) error {
		remaining++
		return nil
	})

	if remaining > 0 {
		t.Errorf("%d snapshots still registered after cleanup", remaining)
	}

	// Check for leaked files
	snapshotsDir := filepath.Join(env.SnapshotterRoot(), "snapshots")
	if info, err := os.Stat(snapshotsDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(snapshotsDir)
		if len(entries) > 0 {
			t.Errorf("%d leaked files in snapshots directory", len(entries))
			for _, e := range entries {
				t.Logf("  leaked: %s", e.Name())
			}
		}
	}

	t.Log("cleanup verification complete")
}

// ============================================================================
// Benchmark Tests
// ============================================================================

// BenchmarkSnapshotPrepare benchmarks snapshot preparation.
func BenchmarkSnapshotPrepare(b *testing.B) {
	if err := checkPrerequisites(); err != nil {
		b.Skipf("prerequisites not met: %v", err)
	}

	t := &testing.T{}
	env := NewEnvironment(t)
	defer env.Stop()

	if err := env.Start(); err != nil {
		b.Fatalf("start environment: %v", err)
	}

	ctx := env.Context()
	ss := env.SnapshotService()

	// Pull test image
	if err := pullImage(ctx, env.Client(), defaultTestImage); err != nil {
		b.Fatalf("pull image: %v", err)
	}

	// Find committed parent
	var parentKey string
	ss.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		if info.Kind == snapshots.KindCommitted && parentKey == "" {
			parentKey = info.Name
		}
		return nil
	})

	b.ResetTimer()

	for i := range b.N {
		key := fmt.Sprintf("bench-%d", i)
		_, err := ss.Prepare(ctx, key, parentKey)
		if err != nil {
			b.Fatalf("prepare: %v", err)
		}
		ss.Remove(ctx, key)
	}
}

// ============================================================================
// Error Types
// ============================================================================

// ErrServiceNotReady indicates a service is not ready.
var ErrServiceNotReady = errors.New("service not ready")

// ErrSnapshotNotFound indicates a snapshot was not found.
var ErrSnapshotNotFound = errors.New("snapshot not found")

// ============================================================================
// Additional Helpers
// ============================================================================

// VerifyVMDK provides detailed VMDK verification for debugging.
type VerifyVMDK struct {
	Path       string
	Layers     []VMDKLayer
	FsmetaPath string
	TotalSize  int64
}

// VMDKLayer represents a layer in a VMDK descriptor.
type VMDKLayer struct {
	Path    string
	Sectors int64
	Digest  digest.Digest
}

// ParseVMDK parses a VMDK descriptor file for verification.
func ParseVMDK(path string) (*VerifyVMDK, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	v := &VerifyVMDK{Path: path}
	scanner := bufio.NewScanner(f)
	extentPattern := regexp.MustCompile(`RW\s+(\d+)\s+FLAT\s+"([^"]+)"`)

	for scanner.Scan() {
		if matches := extentPattern.FindStringSubmatch(scanner.Text()); len(matches) > 2 {
			var sectors int64
			fmt.Sscanf(matches[1], "%d", &sectors)

			layer := VMDKLayer{
				Path:    matches[2],
				Sectors: sectors,
			}

			// Extract digest if present
			if d := extractDigest(matches[2]); d != "" {
				layer.Digest = digest.Digest("sha256:" + d)
			}

			v.Layers = append(v.Layers, layer)
			v.TotalSize += sectors * 512

			if strings.Contains(matches[2], "fsmeta.erofs") {
				v.FsmetaPath = matches[2]
			}
		}
	}

	return v, scanner.Err()
}

// findVMDKWithMostLayers finds the VMDK file with the most layers.
// Returns the path and layer count, or empty string and 0 if not found.
func findVMDKWithMostLayers(snapshotsDir string) (string, int) {
	var vmdkPath string
	var maxLayers int

	_ = filepath.Walk(snapshotsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip files with errors
		}
		if filepath.Base(path) != mergedVMDKFile {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil //nolint:nilerr // intentionally skip files we can't read
		}
		layers := strings.Count(string(data), "sha256-")
		if layers > maxLayers {
			maxLayers = layers
			vmdkPath = path
		}
		return nil
	})

	return vmdkPath, maxLayers
}
