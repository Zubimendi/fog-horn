package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPStorageClient implements StorageClient over HTTP (e.g. MinIO or S3 proxy).
type HTTPStorageClient struct {
	baseURL    string
	bucket     string
	httpClient *http.Client
}

// NewHTTPStorageClient creates a storage client that communicates with MinIO/S3 via HTTP.
func NewHTTPStorageClient(endpoint, bucket string, useSSL bool) *HTTPStorageClient {
	scheme := "http"
	if useSSL {
		scheme = "https"
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = fmt.Sprintf("%s://%s", scheme, endpoint)
	}

	return &HTTPStorageClient{
		baseURL:    endpoint,
		bucket:     bucket,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *HTTPStorageClient) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	url := fmt.Sprintf("%s/%s/%s", c.baseURL, c.bucket, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("s3: create put request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("s3: put object: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3: put object returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *HTTPStorageClient) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	url := fmt.Sprintf("%s/%s/%s", c.baseURL, c.bucket, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("s3: create get request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("s3: get object: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("object %q not found", key)
	}
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("s3: get object returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("s3: read body: %w", err)
	}

	etag := resp.Header.Get("ETag")
	if etag == "" {
		hash := sha256.Sum256(data)
		etag = fmt.Sprintf("\"%s\"", hex.EncodeToString(hash[:16]))
	}

	return data, etag, nil
}
