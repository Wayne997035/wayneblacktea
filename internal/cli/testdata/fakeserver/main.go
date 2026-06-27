// fakeserver is a tiny HTTP test fixture used by internal/cli setup_test.go
// to stand in for wayneblacktea-server. It binds 127.0.0.1:$PORT and
// returns 200 OK on /health.
//
// It honours SIGTERM so RunStop can shut it down via the lifecycle
// supervisor.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8420"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})
	srv := &http.Server{
		Addr:              "127.0.0.1:" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	idle := make(chan struct{})
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		close(idle)
	}()

	fmt.Fprintf(os.Stderr, "fakeserver: listening on %s\n", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "fakeserver: %v\n", err)
		os.Exit(1)
	}
	<-idle
}
