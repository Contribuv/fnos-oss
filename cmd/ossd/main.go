package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"fnososs/internal/admin"
	"fnososs/internal/config"
	"fnososs/internal/s3"
	"fnososs/internal/store"
	"fnososs/internal/version"
)

func main() {
	var (
		configPath = flag.String("config", config.ConfigPath(), "path to oss.json")
		adminAddr  = flag.String("admin-addr", os.Getenv("OSS_ADMIN_ADDR"), "optional TCP address for the admin console (development only)")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println(version.Version)
		return
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		logger.Error("open data store", "error", err, "dataDir", cfg.DataDir)
		os.Exit(1)
	}
	defer st.Close()

	if key, created, err := st.EnsureDefaultKey(); err != nil {
		logger.Error("ensure default access key", "error", err)
	} else if created {
		logger.Info("created default access key; open the admin console to view the secret", "accessKey", key.AccessKey)
	}

	started := time.Now()
	activity := admin.NewActivity(500)
	s3Server := &s3.Server{
		Store: st, Region: cfg.Region, Logger: logger,
		OnRequest: activity.Add,
	}
	adminServer := admin.New(st, cfg, started, activity)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errs := make(chan error, 4)
	servers := []*http.Server{}

	tcpAddr := net.JoinHostPort(cfg.ListenHost, fmt.Sprintf("%d", cfg.Port))
	ln, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		logger.Error("listen for S3 traffic", "addr", tcpAddr, "error", err)
		os.Exit(1)
	}
	s3HTTP := &http.Server{
		Handler:           s3Server,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	servers = append(servers, s3HTTP)
	go func() {
		logger.Info("S3 endpoint listening", "addr", tcpAddr)
		if err := s3HTTP.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	if cfg.SocketPath != "" {
		if runtime.GOOS == "windows" {
			logger.Warn("unix sockets are not supported on windows; skipping gateway socket", "path", cfg.SocketPath)
		} else {
			_ = os.Remove(cfg.SocketPath)
			if err := os.MkdirAll(dirOf(cfg.SocketPath), 0o755); err != nil {
				logger.Error("create socket directory", "error", err)
				os.Exit(1)
			}
			uln, err := net.Listen("unix", cfg.SocketPath)
			if err != nil {
				logger.Error("listen on gateway socket", "path", cfg.SocketPath, "error", err)
				os.Exit(1)
			}
			_ = os.Chmod(cfg.SocketPath, 0o666)
			adminHTTP := &http.Server{Handler: adminServer, ReadHeaderTimeout: 30 * time.Second}
			servers = append(servers, adminHTTP)
			go func() {
				logger.Info("admin console listening on gateway socket", "path", cfg.SocketPath, "prefix", cfg.GatewayPrefix)
				if err := adminHTTP.Serve(uln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errs <- err
				}
			}()
		}
	}

	if *adminAddr != "" {
		aln, err := net.Listen("tcp", *adminAddr)
		if err != nil {
			logger.Error("listen for admin console", "addr", *adminAddr, "error", err)
			os.Exit(1)
		}
		adminHTTP := &http.Server{Handler: adminServer, ReadHeaderTimeout: 30 * time.Second}
		servers = append(servers, adminHTTP)
		go func() {
			logger.Warn("admin console exposed on TCP (development only)", "addr", *adminAddr)
			if err := adminHTTP.Serve(aln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}()
	}

	logger.Info("fnOS object storage started", "version", version.Version, "data", cfg.DataDir)
	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-errs:
		logger.Error("server stopped", "error", err)
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	for _, srv := range servers {
		_ = srv.Shutdown(shutdownCtx)
	}
	if cfg.SocketPath != "" {
		_ = os.Remove(cfg.SocketPath)
	}
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv}))
}

func dirOf(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i > 0 {
		return p[:i]
	}
	return "."
}
