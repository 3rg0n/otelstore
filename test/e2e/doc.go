// Package e2e contains black-box end-to-end tests that run the compiled
// otelstore binary as a subprocess and drive it over real sockets.
//
// These tests are guarded by the "e2e" build tag so they are excluded from the
// normal unit-test gate (which must not spawn processes or bind ports). Run
// them explicitly:
//
//	go test -tags e2e ./test/e2e/ -v
//
// This file carries no build tag so that `go test ./...` sees a non-empty
// package (reported as "no test files") instead of failing with
// "build constraints exclude all Go files".
package e2e
