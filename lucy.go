package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Config holds all configuration for the proxy server
type Config struct {
	Port           int
	RequestTimeout time.Duration
	ServerTimeout  time.Duration
	MaxBodySize    int64
}

// ProxyServer handles HTTP proxy requests
type ProxyServer struct {
	config Config
	logger *slog.Logger
	client *http.Client
	server *http.Server
}

// RequestLog represents a logged HTTP request
type RequestLog struct {
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Duration time.Duration     `json:"duration"`
}

// ResponseLog represents a logged HTTP response
type ResponseLog struct {
	Status   string            `json:"status"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Duration time.Duration     `json:"duration"`
}

// NewProxyServer creates a new proxy server with the given config
func NewProxyServer(config Config) *ProxyServer {
	// Create structured logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Create HTTP client with reasonable defaults
	client := &http.Client{
		Timeout: config.RequestTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	server := &http.Server{
		Addr:         ":" + strconv.Itoa(config.Port),
		ReadTimeout:  config.ServerTimeout,
		WriteTimeout: config.ServerTimeout,
		IdleTimeout:  time.Minute,
	}

	proxy := &ProxyServer{
		config: config,
		logger: logger,
		client: client,
		server: server,
	}

	server.Handler = http.HandlerFunc(proxy.handleRequest)
	return proxy
}

// Start begins listening for HTTP requests and handles graceful shutdown
func (p *ProxyServer) Start(ctx context.Context) error {
	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		p.logger.Info("Starting HTTP debug proxy",
			"port", p.config.Port,
			"timeout", p.config.RequestTimeout)

		fmt.Printf("🚀 Lucy started on port %d\n", p.config.Port)
		fmt.Printf("📝 Watching for requests...\n\n")

		if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- fmt.Errorf("server failed to start: %w", err)
		}
	}()

	// Wait for shutdown signal or server error
	select {
	case err := <-serverErr:
		return err
	case sig := <-sigChan:
		p.logger.Info("Received shutdown signal", "signal", sig)
		return p.shutdown(ctx)
	case <-ctx.Done():
		return p.shutdown(ctx)
	}
}

// shutdown gracefully shuts down the server
func (p *ProxyServer) shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	p.logger.Info("Shutting down proxy server...")

	if err := p.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	p.logger.Info("Proxy server stopped")
	return nil
}

// handleRequest routes requests to appropriate handlers
func (p *ProxyServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method == "CONNECT" {
		p.handleHTTPS(w, r, start)
	} else {
		p.handleHTTP(w, r, start)
	}
}

// handleHTTP processes regular HTTP requests
func (p *ProxyServer) handleHTTP(w http.ResponseWriter, r *http.Request, start time.Time) {
	ctx := r.Context()

	// Read and limit request body
	body, err := p.readLimitedBody(r.Body, p.config.MaxBodySize)
	if err != nil {
		p.logger.Error("Failed to read request body", "error", err)
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	defer r.Body.Close()

	// Log the request
	p.logHTTPRequest(r, body)

	// Build target URL
	targetURL, err := p.buildTargetURL(r)
	if err != nil {
		p.logger.Error("Invalid target URL", "error", err)
		http.Error(w, "Invalid request URL", http.StatusBadRequest)
		return
	}

	// Create forwarded request
	req, err := p.createForwardedRequest(ctx, r, targetURL, body)
	if err != nil {
		p.logger.Error("Failed to create forwarded request", "error", err)
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	// Make the request
	resp, err := p.client.Do(req)
	if err != nil {
		p.logError(targetURL, err, time.Since(start))
		http.Error(w, "Failed to make request: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := p.readLimitedBody(resp.Body, p.config.MaxBodySize)
	if err != nil {
		p.logger.Error("Failed to read response body", "error", err)
		http.Error(w, "Response body too large", http.StatusBadGateway)
		return
	}

	// Decompress for logging if needed (but forward original compressed data)
	decompressedBody := p.decompressIfNeeded(respBody, resp.Header)

	// Log the response (using decompressed version for readability)
	p.logHTTPResponse(targetURL, resp, decompressedBody, time.Since(start))

	// Forward response (send original compressed data to maintain proper encoding)
	p.forwardResponse(w, resp, respBody)
}

// handleHTTPS processes HTTPS CONNECT requests
func (p *ProxyServer) handleHTTPS(w http.ResponseWriter, r *http.Request, start time.Time) {
	p.logger.Info("HTTPS CONNECT", "host", r.Host)

	// Connect to target
	targetConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		p.logError("CONNECT "+r.Host, err, time.Since(start))
		http.Error(w, "Failed to connect to target", http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	// Send 200 OK
	w.WriteHeader(http.StatusOK)

	// Hijack connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		p.logger.Error("Failed to hijack connection", "error", err)
		return
	}
	defer clientConn.Close()

	// Tunnel traffic
	p.tunnelTraffic(clientConn, targetConn, r.Host, start)
}

// Helper methods

func (p *ProxyServer) readLimitedBody(reader io.ReadCloser, maxSize int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(reader, maxSize))
}

func (p *ProxyServer) buildTargetURL(r *http.Request) (string, error) {
	if r.URL.IsAbs() {
		return r.URL.String(), nil
	}

	// Construct absolute URL
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	targetURL := &url.URL{
		Scheme:   scheme,
		Host:     r.Host,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	return targetURL.String(), nil
}

func (p *ProxyServer) createForwardedRequest(ctx context.Context, original *http.Request, targetURL string, body []byte) (*http.Request, error) {
	bodyReader := strings.NewReader(string(body))
	req, err := http.NewRequestWithContext(ctx, original.Method, targetURL, bodyReader)
	if err != nil {
		return nil, err
	}

	// Copy headers (excluding hop-by-hop headers)
	for name, values := range original.Header {
		if !isHopByHopHeader(name) {
			req.Header[name] = values
		}
	}

	return req, nil
}

func (p *ProxyServer) forwardResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	// Copy headers
	for name, values := range resp.Header {
		w.Header()[name] = values
	}

	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func (p *ProxyServer) tunnelTraffic(clientConn, targetConn net.Conn, host string, start time.Time) {
	done := make(chan struct{}, 2)

	// Copy data in both directions
	go func() {
		io.Copy(targetConn, clientConn)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(clientConn, targetConn)
		done <- struct{}{}
	}()

	// Wait for either direction to close
	<-done

	p.logger.Info("HTTPS tunnel closed",
		"host", host,
		"duration", time.Since(start))
}

// Logging methods

func (p *ProxyServer) logHTTPRequest(r *http.Request, body []byte) {
	fmt.Printf("➡️ %s %s\n", r.Method, r.URL.String())

	// Log interesting headers
	headers := p.extractInterestingHeaders(r.Header)
	for name, value := range headers {
		fmt.Printf("   %s: %s\n", name, value)
	}

	// Log body if present
	if len(body) > 0 {
		bodyStr := p.formatBody(string(body))
		fmt.Printf("   Body: %s\n", bodyStr)
	}

	// Structured log
	p.logger.Info("HTTP request",
		"method", r.Method,
		"url", r.URL.String(),
		"headers", headers,
		"body_size", len(body))
}
func (p *ProxyServer) logHTTPResponse(url string, resp *http.Response, body []byte, duration time.Duration) {
	fmt.Printf("\n⬅️ %s %s (%v)\n", resp.Status, url, duration)

	// Log interesting headers
	headers := p.extractInterestingHeaders(resp.Header)
	for name, value := range headers {
		fmt.Printf("   %s: %s\n", name, value)
	}

	// Log response body
	if len(body) > 0 {
		// Check if content might be binary/compressed
		if p.isBinaryContent(body) {
			fmt.Printf("   Response: [Binary/Compressed content, %d bytes]\n", len(body))
		} else {
			bodyStr := p.formatBody(string(body))
			fmt.Printf("   Response: %s\n", bodyStr)
		}
	}
	fmt.Println("---")

	// Structured log
	p.logger.Info("HTTP response",
		"status", resp.StatusCode,
		"url", url,
		"headers", headers,
		"body_size", len(body),
		"duration", duration)
}

func (p *ProxyServer) logError(url string, err error, duration time.Duration) {
	fmt.Printf("❌ ERROR %s: %v (%v)\n---\n", url, err, duration)

	p.logger.Error("Request failed",
		"url", url,
		"error", err,
		"duration", duration)
}

func (p *ProxyServer) extractInterestingHeaders(headers http.Header) map[string]string {
	interesting := map[string]string{}
	interestingNames := []string{
		"Authorization", "Content-Type", "Content-Length",
		"User-Agent", "Accept", "Cookie", "Set-Cookie",
	}

	for _, name := range interestingNames {
		if values := headers.Values(name); len(values) > 0 {
			interesting[name] = strings.Join(values, ", ")
		}
	}

	return interesting
}

func (p *ProxyServer) formatBody(body string) string {
	const maxLength = 500

	// Try to pretty-print JSON
	if p.isJSON(body) {
		if pretty := p.prettyJSON(body); pretty != body {
			if len(pretty) > maxLength {
				return pretty[:maxLength] + "..."
			}
			return pretty
		}
	}

	// Truncate if too long
	if len(body) > maxLength {
		return body[:maxLength] + "..."
	}

	return body
}

func (p *ProxyServer) isJSON(s string) bool {
	s = strings.TrimSpace(s)
	return (strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"))
}
func (p *ProxyServer) decompressIfNeeded(body []byte, headers http.Header) []byte {
	encoding := headers.Get("Content-Encoding")
	if encoding == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			p.logger.Debug("Failed to create gzip reader", "error", err)
			return body // Return original if decompression fails
		}
		defer reader.Close()

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			p.logger.Debug("Failed to decompress gzip content", "error", err)
			return body
		}
		return decompressed
	}
	return body
}

func (p *ProxyServer) isBinaryContent(data []byte) bool {
	// Check for null bytes or high ratio of non-printable characters
	if len(data) == 0 {
		return false
	}

	nonPrintable := 0
	for _, b := range data {
		if b == 0 || (b < 32 && b != '\t' && b != '\n' && b != '\r') || b > 126 {
			nonPrintable++
		}
	}

	// If more than 20% is non-printable, consider it binary
	return float64(nonPrintable)/float64(len(data)) > 0.2
}

func (p *ProxyServer) prettyJSON(s string) string {
	var obj interface{}
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return s
	}

	pretty, err := json.MarshalIndent(obj, "   ", "  ")
	if err != nil {
		return s
	}

	return string(pretty)
}

// Utility functions

func isHopByHopHeader(name string) bool {
	hopByHop := []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailers", "Transfer-Encoding", "Upgrade",
	}

	for _, h := range hopByHop {
		if strings.EqualFold(name, h) {
			return true
		}
	}
	return false
}

func main() {
	var (
		port           = flag.Int("port", 8080, "Port to listen on")
		requestTimeout = flag.Duration("timeout", 30*time.Second, "Request timeout")
		serverTimeout  = flag.Duration("server-timeout", 30*time.Second, "Server timeout")
		maxBodySize    = flag.Int64("max-body-size", 10*1024*1024, "Maximum body size in bytes (10MB)")
	)
	flag.Parse()

	config := Config{
		Port:           *port,
		RequestTimeout: *requestTimeout,
		ServerTimeout:  *serverTimeout,
		MaxBodySize:    *maxBodySize,
	}

	proxy := NewProxyServer(config)

	ctx := context.Background()
	if err := proxy.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
