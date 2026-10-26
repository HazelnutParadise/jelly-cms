package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/safehttp"
	"github.com/HazelnutParadise/jelly-cms/internal/storage"
)

const (
	pluginStorageMaxBytes = int64(4 << 30)
	pluginStorageSmallIO  = 30 * time.Second
	pluginStorageLargeIO  = 5 * time.Minute
)

type pluginStorageDriver struct {
	id string
}

type pluginStorageDescriptor struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func newPluginStorageDriver(ctx context.Context, id string) (storage.Driver, error) {
	db := extensionDatabase(ctx)
	if !storagePolicyProviderAllowed(ctx, db, id) {
		return nil, errMediaDriverUnavailable
	}
	runtime, _, err := extensionPluginMeta(ctx, db, id, ext.PointMediaStorage)
	if err != nil || runtime == nil {
		return nil, errMediaDriverUnavailable
	}
	return &pluginStorageDriver{id: id}, nil
}

func (d *pluginStorageDriver) descriptor(ctx context.Context, op, p string, size int64, contentType string) (*url.URL, http.Header, plugin.PluginMetadata, error) {
	clean, err := storage.CleanPath(p)
	if err != nil || (op == "put" && (size < 0 || size > pluginStorageMaxBytes)) {
		return nil, nil, plugin.PluginMetadata{}, storage.ErrInvalidPath
	}
	db := extensionDatabase(ctx)
	if !storagePolicyProviderAllowed(ctx, db, d.id) {
		return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
	}
	runtime, meta, err := extensionPluginMeta(ctx, db, d.id, ext.PointMediaStorage)
	if err != nil {
		return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
	}
	if err := requirePluginHostPermission(ctx, db, meta, "http.fetch"); err != nil {
		return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
	}
	input, err := json.Marshal(map[string]any{"op": op, "path": clean, "size": size, "content_type": contentType})
	if err != nil {
		return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
	}
	output, err := runtime.CallExtension(ctx, d.id, ext.PointMediaStorage, input)
	if err != nil || len(output) == 0 || len(output) > pluginHostJSONMax {
		return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
	}
	var result pluginStorageDescriptor
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || result.URL == "" || len(result.Headers) > 32 {
		return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
	}
	parsed, err := url.ParseRequestURI(result.URL)
	permission, _ := pluginPermission(meta, "http.fetch")
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.Hostname() != strings.ToLower(parsed.Hostname()) || !exactHostAllowed(parsed.Hostname(), permission.Hosts) || hasControl(result.URL) {
		return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
		}
	}
	headers := make(http.Header, len(result.Headers))
	headerBytes := 0
	for key, value := range result.Headers {
		canonical := http.CanonicalHeaderKey(key)
		lower := strings.ToLower(canonical)
		if !validPluginHostHeaderName(key) || pluginHostSecretHeaders[lower] || pluginHostHopHeaders[lower] || strings.ContainsAny(value, "\r\n") || len(value) > 8192 {
			return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
		}
		headerBytes += len(canonical) + len(value)
		if headerBytes > pluginHostHTTPHeaderMax {
			return nil, nil, plugin.PluginMetadata{}, errMediaDriverUnavailable
		}
		headers.Set(canonical, value)
	}
	return parsed, headers, meta, nil
}

func (d *pluginStorageDriver) request(ctx context.Context, method, op, p string, body io.Reader, size int64, contentType string, exclusive bool) (*http.Response, *http.Client, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := pluginStorageSmallIO
	if size > 1<<20 || method == http.MethodGet {
		timeout = pluginStorageLargeIO
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	parsed, headers, meta, err := d.descriptor(ctx, op, p, size, contentType)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	permission, _ := pluginPermission(meta, "http.fetch")
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		cancel()
		return nil, nil, nil, errMediaDriverUnavailable
	}
	request.Close = true
	request.Header = headers
	if method == http.MethodPut {
		request.ContentLength = size
		request.Header.Set("Content-Length", strconv.FormatInt(size, 10))
		if exclusive {
			request.Header.Set("If-None-Match", "*")
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
	}
	client := safehttp.Client(safehttp.Options{Timeout: timeout, MaxBytes: pluginStorageMaxBytes, AllowHosts: permission.Hosts})
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("storage redirect rejected") }
	client.Transport = &pluginStorageHeaderLimit{base: client.Transport, maxBytes: pluginHostHTTPHeaderMax}
	response, err := client.Do(request)
	if err != nil {
		cancel()
		client.CloseIdleConnections()
		return nil, nil, nil, errors.New("plugin storage HTTP request failed")
	}
	return response, client, cancel, nil
}

func (d *pluginStorageDriver) Put(ctx context.Context, p string, r io.Reader, size int64, contentType string) error {
	return d.put(ctx, p, r, size, contentType, false)
}

// PutExclusive asks the provider to create the object only if it does not
// already exist. The conditional is enforced by the object server itself.
func (d *pluginStorageDriver) PutExclusive(ctx context.Context, p string, r io.Reader, size int64, contentType string) error {
	return d.put(ctx, p, r, size, contentType, true)
}

func (d *pluginStorageDriver) put(ctx context.Context, p string, r io.Reader, size int64, contentType string, exclusive bool) error {
	if r == nil || size < 0 || size > pluginStorageMaxBytes {
		return storage.ErrInvalidPath
	}
	response, client, cancel, err := d.request(ctx, http.MethodPut, "put", p, r, size, contentType, exclusive)
	if err != nil {
		return err
	}
	defer cancel()
	defer client.CloseIdleConnections()
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusPreconditionFailed {
		return fs.ErrExist
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errMediaDriverUnavailable
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, pluginHostHTTPBodyMax))
	return nil
}

func (d *pluginStorageDriver) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	response, client, cancel, err := d.request(ctx, http.MethodGet, "open", p, nil, 0, "", false)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNotFound {
		_ = response.Body.Close()
		cancel()
		client.CloseIdleConnections()
		return nil, fs.ErrNotExist
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		cancel()
		client.CloseIdleConnections()
		return nil, fmt.Errorf("plugin storage GET returned status %d", response.StatusCode)
	}
	return &pluginStorageBody{ReadCloser: response.Body, cancel: cancel, closeIdle: client.CloseIdleConnections}, nil
}

func (d *pluginStorageDriver) Stat(ctx context.Context, p string) (storage.Info, error) {
	response, client, cancel, err := d.request(ctx, http.MethodHead, "stat", p, nil, 0, "", false)
	if err != nil {
		return storage.Info{}, err
	}
	defer cancel()
	defer client.CloseIdleConnections()
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return storage.Info{}, fs.ErrNotExist
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || response.ContentLength < 0 || response.ContentLength > pluginStorageMaxBytes {
		return storage.Info{}, errMediaDriverUnavailable
	}
	info := storage.Info{Path: p, Size: response.ContentLength}
	if modified := response.Header.Get("Last-Modified"); modified != "" {
		info.ModTime, _ = http.ParseTime(modified)
	}
	return info, nil
}

func (d *pluginStorageDriver) Delete(ctx context.Context, p string) error {
	response, client, cancel, err := d.request(ctx, http.MethodDelete, "delete", p, nil, 0, "", false)
	if err != nil {
		return err
	}
	defer cancel()
	defer client.CloseIdleConnections()
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusNoContent || (response.StatusCode >= 200 && response.StatusCode < 300) {
		return nil
	}
	return errMediaDriverUnavailable
}

func (d *pluginStorageDriver) URL(string) string { return "" }

func (d *pluginStorageDriver) Test(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	token, err := extensionRandomID()
	if err != nil {
		return errMediaDriverUnavailable
	}
	p := ".jelly-storage-test-" + token
	payload := []byte("jelly-media-storage-probe:" + token)
	deleted := false
	cleanup := func() {
		if deleted {
			return
		}
		cleanCtx, cancel := context.WithTimeout(context.Background(), pluginStorageSmallIO)
		defer cancel()
		_ = d.Delete(cleanCtx, p)
	}
	if err := d.Put(ctx, p, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"); err != nil {
		cleanup()
		return fmt.Errorf("plugin storage probe PUT failed: %w", err)
	}
	defer cleanup()
	reader, err := d.Open(ctx, p)
	if err != nil {
		return fmt.Errorf("plugin storage probe GET failed: %w", err)
	}
	readback, readErr := io.ReadAll(io.LimitReader(reader, int64(len(payload))+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(readback, payload) {
		return errors.New("plugin storage probe GET did not return the uploaded bytes")
	}
	info, err := d.Stat(ctx, p)
	if err != nil || info.Size != int64(len(payload)) {
		return errors.New("plugin storage probe HEAD did not report the uploaded size")
	}
	if err := d.Delete(ctx, p); err != nil {
		return fmt.Errorf("plugin storage probe DELETE failed: %w", err)
	}
	deleted = true
	return nil
}

type pluginStorageBody struct {
	io.ReadCloser
	cancel    context.CancelFunc
	closeIdle func()
}

func (b *pluginStorageBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	b.closeIdle()
	return err
}

type pluginStorageHeaderLimit struct {
	base     http.RoundTripper
	maxBytes int
}

func (t *pluginStorageHeaderLimit) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	total := 0
	for key, values := range response.Header {
		total += len(key)
		for _, value := range values {
			total += len(value)
		}
		if total > t.maxBytes {
			_ = response.Body.Close()
			return nil, errors.New("storage response headers are too large")
		}
	}
	return response, nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

var _ storage.Driver = (*pluginStorageDriver)(nil)

func (d *pluginStorageDriver) String() string { return fmt.Sprintf("plugin:%s", d.id) }
