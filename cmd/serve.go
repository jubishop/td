package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/marcus/td/internal/db"
	"github.com/marcus/td/internal/serve"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the td HTTP API server",
	Long: `Start an HTTP API server that exposes td's issue tracker over REST.

The server provides JSON endpoints for creating, reading, updating,
and managing issues, boards, sessions, and more. It supports optional
bearer token authentication and CORS for browser-based clients.

If --port is 0 (the default), a random available port is assigned.
The actual port is written to .todos/serve-port for discovery.`,
	GroupID: "system",
	RunE:    runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().IntP("port", "p", 0, "Port to listen on (0 = auto-assign)")
	serveCmd.Flags().StringP("addr", "a", "localhost", "Address to bind to")
	serveCmd.Flags().String("token", "", "Bearer token for authentication (optional)")
	serveCmd.Flags().String("cors", "", "Allowed CORS origin (optional, e.g. http://localhost:3000)")
	serveCmd.Flags().Duration("interval", 2*time.Second, "Poll interval for SSE events")
}

func runServe(cmd *cobra.Command, args []string) error {
	return runHTTPServer(cmd, false)
}

func runHTTPServer(cmd *cobra.Command, browser bool) error {
	dir := getBaseDir()
	port, _ := cmd.Flags().GetInt("port")
	interval, _ := cmd.Flags().GetDuration("interval")
	if port < 0 || port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}
	if interval <= 0 {
		return fmt.Errorf("interval must be greater than zero")
	}

	// Open database
	database, err := db.Open(dir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = database.Close() }()

	// Limit connections for long-running server process
	database.SetMaxOpenConns(1)

	// Get or create web session
	session, err := serve.GetOrCreateWebSession(database)
	if err != nil {
		return fmt.Errorf("bootstrap web session: %w", err)
	}

	// Start session heartbeat
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	serve.StartSessionHeartbeat(ctx, database, session.ID)

	// Read flags
	addr, _ := cmd.Flags().GetString("addr")
	if browser {
		addr = "127.0.0.1"
	}
	token, _ := cmd.Flags().GetString("token")
	cors, _ := cmd.Flags().GetString("cors")

	config := serve.ServeConfig{
		Port:         port,
		Addr:         addr,
		Token:        token,
		CORSOrigin:   cors,
		PollInterval: interval,
		Browser:      browser,
	}

	// Create server
	srv := serve.NewServer(database, dir, session.ID, config)

	// Start listener (use net.Listen for auto-port support)
	listenAddr := fmt.Sprintf("%s:%d", addr, port)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	// Get actual port (may differ from requested if port was 0)
	actualPort := ln.Addr().(*net.TCPAddr).Port

	// Generate instance ID for port file
	instanceID, err := serve.GenerateInstanceID()
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("generate instance id: %w", err)
	}

	// Write port file
	portInfo := &serve.PortInfo{
		Port:       actualPort,
		PID:        os.Getpid(),
		StartedAt:  time.Now(),
		InstanceID: instanceID,
	}
	if err := serve.WritePortFile(dir, portInfo); err != nil {
		_ = ln.Close()
		return fmt.Errorf("write port file: %w", err)
	}
	defer func() {
		if current, err := serve.ReadPortFile(dir); err == nil && current.InstanceID == instanceID {
			_ = serve.DeletePortFile(dir)
		}
	}()

	// Print startup banner to stderr
	dbPath := filepath.Join(dir, ".todos", "issues.db")
	portFilePath := filepath.Join(dir, ".todos", "serve-port")
	url := fmt.Sprintf("http://%s:%d", addr, actualPort)
	fmt.Fprintf(os.Stderr, "td %s listening on %s\n", cmd.Name(), url)
	fmt.Fprintf(os.Stderr, "  base dir:   %s\n", dir)
	fmt.Fprintf(os.Stderr, "  database:   %s\n", dbPath)
	fmt.Fprintf(os.Stderr, "  session:    %s (web)\n", session.ID)
	fmt.Fprintf(os.Stderr, "  port file:  %s\n", portFilePath)

	// Start HTTP server in background
	srv.StartBackground(ctx)
	defer srv.StopBackground()

	httpServer := &http.Server{
		Handler:      srv.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()
	if browser {
		fmt.Fprintln(os.Stderr, "  Press Ctrl+C to stop.")
		noOpen, _ := cmd.Flags().GetBool("no-open")
		if !noOpen {
			go func() {
				if err := openBrowserURL(url); err != nil {
					fmt.Fprintf(os.Stderr, "  Could not open browser: %v. Open %s manually.\n", err, url)
				}
			}()
		}
	}

	// Wait for signal or server error
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-ctx.Done():
	case sig := <-sigCh:
		slog.Info("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
	}
	srv.StopBackground()

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}

	// Cleanup
	cancel() // stop heartbeat

	fmt.Fprintf(os.Stderr, "td %s stopped\n", cmd.Name())
	return nil
}
