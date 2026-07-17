package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/otel/internal/mcpserver"
	"github.com/otel/internal/query"
	"github.com/otel/internal/receiver"
	"github.com/otel/internal/store"
)

func main() {
	dbPath := flag.String("db-path", ":memory:", "Path to SQLite database file")
	ingestPort := flag.String("ingest-port", ":4318", "Port for OTLP ingest server")
	queryPort := flag.String("query-port", ":4319", "Port for query API server")
	mcpAddr := flag.String("mcp-addr", ":4320", "Port for MCP query server")
	flag.Parse()

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

	// Create servers with timeouts (prevent Slowloris attack)
	ingestServer := &http.Server{
		Addr:              *ingestPort,
		Handler:           ingestHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	queryServer := &http.Server{
		Addr:              *queryPort,
		Handler:           queryHandler,
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

	// Start servers in goroutines
	go func() {
		log.Printf("Starting OTLP receiver on %s", *ingestPort)
		if err := ingestServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Ingest server error: %v", err)
		}
	}()

	go func() {
		log.Printf("Starting query API on %s", *queryPort)
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

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

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
