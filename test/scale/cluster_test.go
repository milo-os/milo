//go:build scale

package scale

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
)

type endpoint struct {
	url   string
	token string
}

func (e *endpoint) get(ctx context.Context, path string) (string, error) {
	return httpGet(ctx, e.url, e.token, path)
}

func (e *endpoint) post(ctx context.Context, path string, body []byte) (string, error) {
	return httpPost(ctx, e.url, e.token, path, body)
}

func (e *endpoint) delete(ctx context.Context, path string) error {
	return httpDelete(ctx, e.url, e.token, path)
}

// connect reads .milo/kubeconfig and returns a handle to the Milo API server.
// Requires 'task dev:setup' to have been run first.
func connect(t *testing.T) *endpoint {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("", filepath.Join(repoRoot(t), ".milo", "kubeconfig"))
	if err != nil {
		t.Fatalf("load .milo/kubeconfig: %v", err)
	}
	s := &endpoint{url: cfg.Host, token: cfg.BearerToken}
	s.waitReady(t)
	t.Logf("connected to milo apiserver at %s", s.url)
	return s
}

func (s *endpoint) waitReady(t *testing.T) {
	t.Helper()
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, s.url+"/readyz", nil)
		req.Header.Set("Authorization", "Bearer "+s.token)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			err = fmt.Errorf("readyz status=%d", resp.StatusCode)
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("milo apiserver not ready at %s: %v", s.url, lastErr)
}

// portForward starts a kubectl port-forward to a cluster service, returning
// an endpoint at the assigned local port. Killed in t.Cleanup.
func portForward(t *testing.T, namespace, service string, remotePort int, scheme, token string) *endpoint {
	t.Helper()
	kubeconfig := filepath.Join(repoRoot(t), ".test-infra", "kubeconfig")
	if _, err := os.Stat(kubeconfig); err != nil {
		t.Fatalf("port-forward %s/%s: .test-infra/kubeconfig not found — run 'task test-infra-cluster' first", namespace, service)
	}
	cmd := exec.CommandContext(context.Background(), "kubectl",
		"--kubeconfig", kubeconfig,
		"-n", namespace,
		"port-forward", service,
		fmt.Sprintf(":%d", remotePort),
	)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe for port-forward: %v", err)
	}
	cmd.Stdout = pw
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		t.Fatalf("port-forward %s/%s:%d: %v", namespace, service, remotePort, err)
	}
	_ = pw.Close()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = pr.Close()
	})
	localPort := parseForwardPort(t, pr, remotePort)
	t.Logf("port-forward %s/%s: localhost:%d -> %d", namespace, service, localPort, remotePort)
	return &endpoint{url: fmt.Sprintf("%s://localhost:%d", scheme, localPort), token: token}
}

// parseForwardPort scans kubectl stdout for "Forwarding from 127.0.0.1:PORT -> ..."
func parseForwardPort(t *testing.T, r *os.File, remotePort int) int {
	t.Helper()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Forwarding from 127.0.0.1:") {
			continue
		}
		portStr, _, _ := strings.Cut(strings.TrimPrefix(line, "Forwarding from 127.0.0.1:"), " ")
		if port, err := strconv.Atoi(portStr); err == nil {
			return port
		}
	}
	t.Fatalf("port-forward: did not see forwarding line for remote port %d", remotePort)
	return 0
}

var insecureClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	},
}

func httpPost(ctx context.Context, baseURL, token, path string, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := insecureClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &httpStatusError{code: resp.StatusCode, body: string(b)}
	}
	return string(b), nil
}

func httpDelete(ctx context.Context, baseURL, token, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := insecureClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func httpGet(ctx context.Context, baseURL, token, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := insecureClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &httpStatusError{code: resp.StatusCode, body: string(b)}
	}
	return string(b), nil
}

type httpStatusError struct {
	code int
	body string
}

func (e *httpStatusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("could not find go.mod from %s", wd)
		}
	}
}
