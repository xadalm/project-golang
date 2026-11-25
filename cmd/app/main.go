package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"example.com/linkup/internal/demo"
	"example.com/linkup/internal/httpapi"
	"example.com/linkup/internal/interaction"
	"example.com/linkup/internal/memory"
	"example.com/linkup/internal/profileapi"
)

func main() {
	if err := run(); err != nil {
		log.Printf("application failed: %v", err)
		os.Exit(1)
	}
}

func run() error {
	appAddr := env("APP_ADDR", ":8080")
	profileURL := env("PROFILE_API_URL", "http://127.0.0.1:8081")
	commandBudget, err := positiveMillis("COMMAND_BUDGET_MS", "900")
	if err != nil {
		return err
	}
	clientTimeout, err := positiveMillis("PROFILE_HTTP_TIMEOUT_MS", "700")
	if err != nil {
		return err
	}
	shutdownGrace, err := positiveMillis("SHUTDOWN_GRACE_MS", "5000")
	if err != nil {
		return err
	}
	maxResponseBytes, err := strconv.Atoi(env("PROFILE_RESPONSE_LIMIT_BYTES", "4096"))
	if err != nil || maxResponseBytes < 128 || maxResponseBytes > 1<<20 {
		return errors.New("PROFILE_RESPONSE_LIMIT_BYTES must be between 128 and 1048576")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	profiles := memory.NewProfiles(demo.Profiles())
	profileClient, err := profileapi.NewClient(profileURL, &http.Client{Timeout: clientTimeout, Transport: http.DefaultTransport}, maxResponseBytes)
	if err != nil {
		return err
	}
	interactions := interaction.NewService(memory.NewInteractions(), profileClient)
	api, err := httpapi.New(profiles, interactions, commandBudget, logger)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              appAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      commandBudget + 2*time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	listener, err := net.Listen("tcp", appAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", appAddr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("application listening", "address", appAddr, "profile_api", profileURL)
		serverErrors <- server.Serve(listener)
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		closeErr := server.Close()
		if closeErr != nil {
			return fmt.Errorf("shutdown server: %v; force close: %w", err, closeErr)
		}
		return fmt.Errorf("shutdown server: %w", err)
	}
	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

func positiveMillis(name, fallback string) (time.Duration, error) {
	value, err := strconv.Atoi(env(name, fallback))
	if err != nil || value <= 0 || value > 24*60*60*1000 {
		return 0, fmt.Errorf("%s must be between 1 and 86400000 milliseconds", name)
	}
	return time.Duration(value) * time.Millisecond, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
