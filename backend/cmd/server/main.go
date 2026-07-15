package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"railway-map-backend/internal/auth"
	"railway-map-backend/internal/config"
	"railway-map-backend/internal/geo"
	"railway-map-backend/internal/httpapi"
	"railway-map-backend/internal/logging"
	"railway-map-backend/internal/pluginlink"
	"railway-map-backend/internal/purchase"
	"railway-map-backend/internal/realtime"
	"railway-map-backend/internal/store"
	"railway-map-backend/internal/ws"
)

func main() {
	configPath := flag.String("config", "config.yml", "config file path")
	envPath := flag.String("env", ".env", ".env file path; missing files are ignored")
	flag.Parse()

	if err := config.LoadDotEnv(*envPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to load .env: %v\n", err)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger, logCloser, err := logging.New(cfg.Log.Level, cfg.Log.Dir, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logCloser.Close()
	slog.SetDefault(logger)
	logger.Info("Logger initialized", "level", cfg.Log.Level, "dir", cfg.Log.Dir)

	st, err := store.Open(cfg.DB.Driver, cfg.DB.DSN, cfg.DB.Path)
	if err != nil {
		logger.Error("Failed to open database", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	logger.Info("Database opened", "driver", cfg.DB.Driver)

	cache := geo.NewCache(st)
	hub := ws.NewHub(cfg.Realtime.ClientSendBuffer, nil, logger)
	agg := realtime.NewAggregator(time.Duration(cfg.Realtime.TrainTimeoutSeconds)*time.Second, hub)
	agg.SetRideFinalizer(st, logger)
	hub.SetSnapshotter(agg)
	stopSweeper := agg.StartSweeper()
	defer stopSweeper()
	logger.Info("Realtime aggregator started", "trainTimeoutSeconds", cfg.Realtime.TrainTimeoutSeconds)

	pluginServer := pluginlink.NewServer(pluginlink.Options{
		SharedToken: cfg.Plugin.SharedToken,
		Heartbeat:   time.Duration(cfg.Plugin.HeartbeatSeconds) * time.Second,
		Cache:       cache,
		Aggregator:  agg,
		Store:       st,
		Logger:      logger,
	})
	orchestrator := purchase.New(pluginServer, time.Duration(cfg.Plugin.PurchaseTimeoutSeconds)*time.Second)
	pluginServer.SetPurchaseRouter(orchestrator)
	logger.Info("Plugin link server initialized", "heartbeatSeconds", cfg.Plugin.HeartbeatSeconds, "purchaseTimeoutSeconds", cfg.Plugin.PurchaseTimeoutSeconds)

	var authSvc *auth.Service
	if cfg.Auth.Microsoft.ClientID != "" {
		redirectURL := cfg.Server.PublicBaseURL + cfg.Auth.Microsoft.RedirectPath
		authSvc = auth.NewService(
			cfg.Auth.Microsoft.ClientID, cfg.Auth.Microsoft.ClientSecret,
			redirectURL, cfg.Auth.JWTSecret, st,
		)
		logger.Info("Microsoft OAuth enabled", "redirectUrl", redirectURL)
	} else if cfg.Auth.TestAuthEnabled {
		authSvc = auth.NewSessionService(cfg.Auth.JWTSecret, st)
		logger.Warn("Test authentication enabled")
	} else {
		logger.Warn("Authentication is not configured; auth routes and purchases are disabled")
	}

	api := httpapi.New(httpapi.Options{
		Cache: cache, Agg: agg, Plugin: pluginServer,
		Purchase: orchestrator, Auth: authSvc, Store: st, Logger: logger,
		Frontend: cfg.Frontend, TestAuthEnabled: cfg.Auth.TestAuthEnabled, TestAuthUUIDs: cfg.Auth.TestAuthUUIDs,
	})

	r := buildRouter(cfg, api, pluginServer, hub, logger)

	srv := &http.Server{Addr: cfg.Server.Addr, Handler: r}
	go func() {
		logger.Info("Backend server started", "addr", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server stopped unexpectedly", "err", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	received := <-sig
	logger.Info("Shutdown signal received", "signal", received.String())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Graceful shutdown failed", "err", err)
		return
	}
	logger.Info("Backend server stopped")
}

func buildRouter(cfg *config.Config, api *httpapi.API, plugin *pluginlink.Server, hub *ws.Hub, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(requestLogger(logger))
	r.Use(corsMiddleware(cfg.Server.PublicBaseURL))

	r.Get("/health", api.Health)
	r.Get("/internal/plugin", plugin.HandlePlugin)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/config", api.Config)
		r.Get("/meta", api.Meta)
		r.Get("/geojson", api.Geojson)
		r.Get("/lines", api.Lines)
		r.Get("/systems", api.Systems)
		r.Get("/trains", api.Trains)

		r.Get("/auth/login", api.Login)
		r.Get("/auth/callback", api.Callback)
		r.Post("/auth/test-login", api.TestLogin)
		r.Get("/auth/me", api.Me)
		r.Get("/me/history", api.MyRideHistory)
		r.Post("/auth/logout", api.Logout)

		r.Post("/purchase", api.Purchase)
		r.Get("/realtime", hub.HandleWS)
	})

	return r
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("wrapped response writer does not implement http.Hijacker")
	}
	return hijacker.Hijack()
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			message := "REST request completed"
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				message = "WebSocket request completed"
			}
			logger.Info(message,
				"method", r.Method,
				"path", r.URL.RequestURI(),
				"status", status,
				"bytes", rec.bytes,
				"durationMs", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
				"userAgent", r.UserAgent(),
			)
		})
	}
}

func corsMiddleware(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-None-Match")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
