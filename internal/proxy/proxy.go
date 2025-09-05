package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
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

	"github.com/steelcityamir/lucy/internal/config"
)

type ProxyServer struct {
	config config.Config
	logger *slog.Logger
	client *http.Client
	server *http.Server
}

// NewProxyServer initializes the proxy
func NewProxyServer(cfg config.Config) *ProxyServer {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	client := &http.Client{
		Timeout: cfg.RequestTimeout,
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
		Addr:         ":" + strconv.Itoa(cfg.Port),
		ReadTimeout:  cfg.ServerTimeout,
		WriteTimeout: cfg.ServerTimeout,
		IdleTimeout:  time.Minute,
	}

	proxy := &ProxyServer{
		config: cfg,
		logger: logger,
		client: client,
		server: server,
	}
	server.Handler = http.HandlerFunc(proxy.handleRequest)
	return proxy
}

// Start runs the proxy
func (p *ProxyServer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

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

// --- Request/Response Handling ---

func (p *ProxyServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method == "CONNECT" {
		p.handleHTTPS(w, r, start)
	} else {
		p.handleHTTP(w, r, start)
	}
}

// handleHTTP is mostly the same, but uses PrettyLogRequest/Response
func (p *ProxyServer) handleHTTP(w http.ResponseWriter, r *http.Request, start time.Time) {
	ctx := r.Context()
	body, _ := io.ReadAll(io.LimitReader(r.Body, p.config.MaxBodySize))
	defer r.Body.Close()

	headers := extractInterestingHeaders(r.Header)
	PrettyLogRequest(r.Method, r.URL.String(), headers, string(body))

	targetURL := buildTargetURL(r)
	req, _ := http.NewRequestWithContext(ctx, r.Method, targetURL, bytes.NewReader(body))
	for name, values := range r.Header {
		if !isHopByHopHeader(name) {
			req.Header[name] = values
		}
	}

	resp, err := p.client.Do(req)
	if err != nil {
		fmt.Printf("[%s] ❌ ERROR %s: %v\n", timestamp(), targetURL, err)
		http.Error(w, "Failed to make request: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, p.config.MaxBodySize))
	decompressed := decompressIfNeeded(respBody, resp.Header)

	respHeaders := extractInterestingHeaders(resp.Header)
	PrettyLogResponse(resp.StatusCode, targetURL, respHeaders, string(decompressed), time.Since(start))

	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// handleHTTPS processes HTTPS CONNECT requests
func (p *ProxyServer) handleHTTPS(w http.ResponseWriter, r *http.Request, start time.Time) {
	p.logger.Info("HTTPS CONNECT", "host", r.Host)

	// Connect to target
	targetConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		p.logger.Error("CONNECT "+r.Host, err, time.Since(start))
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

// --- Helper functions ---

func extractInterestingHeaders(headers http.Header) map[string]string {
	interesting := map[string]string{}
	names := []string{"Authorization", "Content-Type", "Content-Length", "User-Agent", "Accept", "Cookie", "Set-Cookie"}
	for _, name := range names {
		if values := headers.Values(name); len(values) > 0 {
			interesting[name] = strings.Join(values, ", ")
		}
	}
	return interesting
}

func isHopByHopHeader(name string) bool {
	hopByHop := []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailers", "Transfer-Encoding", "Upgrade"}
	for _, h := range hopByHop {
		if strings.EqualFold(name, h) {
			return true
		}
	}
	return false
}

func decompressIfNeeded(body []byte, headers http.Header) []byte {
	if headers.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return body
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			return body
		}
		return data
	}
	return body
}

func buildTargetURL(r *http.Request) string {
	if r.URL.IsAbs() {
		return r.URL.String()
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return (&url.URL{
		Scheme:   scheme,
		Host:     r.Host,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}).String()
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
