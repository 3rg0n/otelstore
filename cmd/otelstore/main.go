package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/otel/internal/auth"
	"github.com/otel/internal/grpcreceiver"
	"github.com/otel/internal/mcpserver"
	"github.com/otel/internal/query"
	"github.com/otel/internal/receiver"
	"github.com/otel/internal/store"
)

func main() {
	dbPath := flag.String("db-path", ":memory:", "Path to SQLite database file")
	// Defaults bind to loopback (127.0.0.1) so a plain run is local-only — no
	// firewall prompt and not exposed to the LAN. To accept remote traffic,
	// pass an explicit host, e.g. -grpc-port 0.0.0.0:4317 (front with a proxy
	// for TLS/auth).
	grpcPort := flag.String("grpc-port", "127.0.0.1:4317", "Address for gRPC OTLP receiver")
	ingestPort := flag.String("ingest-port", "127.0.0.1:4318", "Address for HTTP OTLP ingest server")
	queryPort := flag.String("query-port", "127.0.0.1:4319", "Address for query API server")
	mcpAddr := flag.String("mcp-addr", "127.0.0.1:4320", "Address for MCP query server")
	authToken := flag.String("auth-token", "", "Bearer token for authentication (if empty, auth disabled)")
	retention := flag.Duration("retention", 0, "Data retention duration (e.g. 168h); 0 disables retention")
	flag.Parse()

	// Check for auth token from environment (flag takes precedence)
	if *authToken == "" {
		if envToken, ok := os.LookupEnv("OTELSTORE_AUTH_TOKEN"); ok {
			authToken = &envToken
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

	// Apply auth middleware to ingest and query handlers if token is set
	var ingestWithAuth, queryWithAuth http.Handler = ingestHandler, queryHandler
	if *authToken != "" {
		authMW := auth.Middleware(*authToken)
		ingestWithAuth = authMW(ingestHandler)
		queryWithAuth = authMW(queryHandler)
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

	mcpHandler := mcpserver.NewStreamableHTTPHandler(mcpSrv)
	mcpHTTPServer := &http.Server{
		Addr:              *mcpAddr,
		Handler:           mcpHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Start retention sweeper if enabled
	var stopRetention chan struct{}
	if *retention > 0 {
		stopRetention = make(chan struct{})
		go func() {
			ticker := time.NewTicker(minDuration(*retention/10, time.Hour))
			defer ticker.Stop()
			for {
				select {
				case <-stopRetention:
					return
				case <-ticker.C:
					cutoffNs := time.Now().UnixNano() - retention.Nanoseconds()
					deleted, err := s.DeleteBefore(ctx, cutoffNs)
					if err != nil {
						log.Printf("Retention cleanup error: %v", err)
					} else if deleted > 0 {
						log.Printf("Retention cleanup: deleted %d rows", deleted)
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
		log.Printf("Starting MCP query server on %s", *mcpAddr)
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
