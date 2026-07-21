package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/3rg0n/otelstore/internal/auth"
	"github.com/3rg0n/otelstore/internal/grpcreceiver"
	"github.com/3rg0n/otelstore/internal/mcpserver"
	"github.com/3rg0n/otelstore/internal/query"
	"github.com/3rg0n/otelstore/internal/receiver"
	"github.com/3rg0n/otelstore/internal/store"
)

// version is the build version, overridden at release time via
// -ldflags "-X main.version=vX.Y.Z". Defaults to "dev" for local builds.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	dbPath := flag.String("db-path", ":memory:", "Path to SQLite database file")
	// Defaults bind to loopback (127.0.0.1) so a plain run is local-only — no
	// firewall prompt and not exposed to the LAN. To accept remote traffic,
	// pass an explicit host, e.g. -grpc-port 0.0.0.0:4317 (front with a proxy
	// for TLS/auth).
	grpcPort := flag.String("grpc-port", "127.0.0.1:4317", "Address for gRPC OTLP receiver")
	ingestPort := flag.String("ingest-port", "127.0.0.1:4318", "Address for HTTP OTLP ingest server")
	queryPort := flag.String("query-port", "127.0.0.1:4319", "Address for query API server")
	mcpAddr := flag.String("mcp-addr", "127.0.0.1:4320", "Address for MCP query server")
	authToken := flag.String("auth-token", "", "Bearer token for authentication (if empty, auth disabled). Prefer -auth-token-file or OTELSTORE_AUTH_TOKEN_FILE to avoid exposing the token in process args.")
	authTokenFile := flag.String("auth-token-file", "", "Path to a file containing the bearer token (avoids exposing it via argv/env). Also OTELSTORE_AUTH_TOKEN_FILE.")
	retention := flag.Duration("retention", 0, "Age-based retention: delete data older than this (e.g. 4320h = 180 days); 0 disables")
	maxSize := flag.Int64("max-size", 0, "Size cap in bytes: evict oldest rows (FIFO) until the DB is under this; 0 disables")
	flag.Parse()

	if *showVersion {
		fmt.Println("otelstore", version)
		return
	}

	log.Printf("otelstore %s", version)

	// Resolve the auth token. Precedence: -auth-token flag > OTELSTORE_AUTH_TOKEN
	// env > token file (-auth-token-file / OTELSTORE_AUTH_TOKEN_FILE). A file is
	// the least-exposed option (not visible in argv or the process environment).
	if *authToken == "" {
		if envToken, ok := os.LookupEnv("OTELSTORE_AUTH_TOKEN"); ok {
			authToken = &envToken
		}
	}
	if *authToken == "" {
		tokenFile := *authTokenFile
		if tokenFile == "" {
			tokenFile = os.Getenv("OTELSTORE_AUTH_TOKEN_FILE")
		}
		if tokenFile != "" {
			// Path is an operator-supplied config flag (same trust level as
			// -db-path), not attacker-controlled input; clean it and read.
			cleaned := filepath.Clean(tokenFile)
			data, err := os.ReadFile(cleaned) // #nosec G304 G703 -- operator-provided token file path
			if err != nil {
				log.Fatalf("failed to read auth token file: %v", err)
			}
			tok := strings.TrimSpace(string(data))
			authToken = &tok
		}
	}

	// Warn if any listener binds a non-loopback address without auth — the
	// telemetry would then be readable by anyone on the network.
	if *authToken == "" {
		for _, addr := range []string{*grpcPort, *ingestPort, *queryPort, *mcpAddr} {
			if isNonLoopback(addr) {
				log.Printf("WARNING: listening on non-loopback %q with no auth token; "+
					"all telemetry is unauthenticated. Set -auth-token (and front with a TLS proxy).", addr)
				break
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create store
	s, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("failed to open store: %v", err)
	}
	if err := s.InitSchema(ctx); err != nil {
		log.Fatalf("failed to init schema: %v", err)
	}
	defer s.Close()

	// Create handlers
	ingestHandler := receiver.NewHandler(s)
	queryHandler := query.NewHandler(s)
	mcpSrv := mcpserver.NewServer(s)

	// Create the MCP handler up front so it too can be auth-wrapped.
	mcpHandler := mcpserver.NewStreamableHTTPHandler(mcpSrv)

	// Apply auth middleware to ALL HTTP handlers when a token is set — ingest,
	// query, AND the MCP query endpoint. Missing MCP here previously let anyone
	// reaching :4320 read all stored telemetry despite -auth-token.
	var ingestWithAuth, queryWithAuth, mcpWithAuth http.Handler = ingestHandler, queryHandler, mcpHandler
	if *authToken != "" {
		authMW := auth.Middleware(*authToken)
		ingestWithAuth = authMW(ingestHandler)
		queryWithAuth = authMW(queryHandler)
		mcpWithAuth = authMW(mcpHandler)
	}

	// Create servers with timeouts (prevent Slowloris attack)
	grpcServer := grpcreceiver.NewGRPCServer(s, *authToken)

	ingestServer := &http.Server{
		Addr:              *ingestPort,
		Handler:           ingestWithAuth,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	queryServer := &http.Server{
		Addr:              *queryPort,
		Handler:           queryWithAuth,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	mcpHTTPServer := &http.Server{
		Addr:              *mcpAddr,
		Handler:           mcpWithAuth,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Start retention sweeper if either age-based or size-based limit is set.
	var stopRetention chan struct{}
	if *retention > 0 || *maxSize > 0 {
		stopRetention = make(chan struct{})
		// Sweep cadence: for age retention, a fraction of the window (capped at
		// 1h); default to 1m when only size is enforced.
		interval := time.Minute
		if *retention > 0 {
			interval = minDuration(*retention/10, time.Hour)
		}
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-stopRetention:
					return
				case <-ticker.C:
					if *retention > 0 {
						cutoffNs := time.Now().UnixNano() - retention.Nanoseconds()
						if deleted, err := s.DeleteBefore(ctx, cutoffNs); err != nil {
							log.Printf("Retention (age) error: %v", err)
						} else if deleted > 0 {
							log.Printf("Retention (age): deleted %d rows", deleted)
						}
					}
					if *maxSize > 0 {
						if deleted, err := s.EnforceMaxSize(ctx, *maxSize); err != nil {
							log.Printf("Retention (size) error: %v", err)
						} else if deleted > 0 {
							log.Printf("Retention (size): evicted %d rows", deleted)
						}
					}
				}
			}
		}()
	}

	// Start gRPC server
	gListener, err := net.Listen("tcp", *grpcPort)
	if err != nil {
		log.Fatalf("failed to listen on gRPC port: %v", err)
	}

	go func() {
		authStr := "auth disabled"
		if *authToken != "" {
			authStr = "auth enabled"
		}
		log.Printf("Starting gRPC OTLP receiver on %s (%s)", *grpcPort, authStr)
		if err := grpcServer.Serve(gListener); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()

	// Start servers in goroutines
	go func() {
		authStr := "auth disabled"
		if *authToken != "" {
			authStr = "auth enabled"
		}
		log.Printf("Starting HTTP OTLP ingest on %s (%s)", *ingestPort, authStr)
		if err := ingestServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Ingest server error: %v", err)
		}
	}()

	go func() {
		authStr := "auth disabled"
		if *authToken != "" {
			authStr = "auth enabled"
		}
		log.Printf("Starting query API on %s (%s)", *queryPort, authStr)
		if err := queryServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Query server error: %v", err)
		}
	}()

	go func() {
		authStr := "auth disabled"
		if *authToken != "" {
			authStr = "auth enabled"
		}
		log.Printf("Starting MCP query server on %s (%s)", *mcpAddr, authStr)
		if err := mcpHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("MCP server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")

	// Stop retention sweeper
	if stopRetention != nil {
		close(stopRetention)
	}

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	grpcServer.GracefulStop()
	if err := ingestServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Ingest server shutdown error: %v", err)
	}
	if err := queryServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Query server shutdown error: %v", err)
	}
	if err := mcpHTTPServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("MCP server shutdown error: %v", err)
	}

	log.Println("Shutdown complete")
}

// minDuration returns the minimum of two durations.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// isNonLoopback reports whether a listen address binds something other than
// localhost. Empty host (":4317") and 0.0.0.0/:: are treated as non-loopback
// (exposed); 127.0.0.1 and [::1] are loopback.
func isNonLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false // unparseable — don't emit a misleading warning
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return false
	}
	if host == "" {
		return true // ":4317" binds all interfaces
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true // hostname — assume routable
	}
	return !ip.IsLoopback()
}
