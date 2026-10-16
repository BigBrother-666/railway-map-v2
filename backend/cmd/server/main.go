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
	"railway-map-backend/internal/pluginconfig"
	"railway-map-backend/internal/pluginlink"
	"railway-map-backend/internal/purchase"
	"railway-map-backend/internal/rbac"
	"railway-map-backend/internal/realtime"
	"railway-map-backend/internal/routing"
	"railway-map-backend/internal/settings"
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

	settingsSvc, err := settings.New(st, cfg.Legacy, logger)
	if err != nil {
		logger.Error("Failed to load server settings", "err", err)
		os.Exit(1)
	}
	rbacSvc, err := rbac.New(st, cfg.Auth.SuperAdminUUID, logger)
	if err != nil {
		logger.Error("Failed to initialize RBAC", "err", err)
		os.Exit(1)
	}

	cache := geo.NewCache(st)
	st.SetGeometryProvider(cache)
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
	configRequester := pluginconfig.New(pluginServer, time.Duration(cfg.Plugin.ConfigTimeoutSeconds)*time.Second)
	pluginServer.SetConfigRouter(configRequester)
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

	routingSvc := routing.NewService(cache, settingsSvc.Route, func() float64 { return settingsSvc.Frontend().DefaultPricePerKm })

	api := httpapi.New(httpapi.Options{
		Cache: cache, Agg: agg, Plugin: pluginServer,
		Purchase: orchestrator, Auth: authSvc, Store: st, Logger: logger,
		Frontend: settingsSvc.Frontend, FrontendBaseURL: cfg.Server.FrontendBaseURL,
		TestAuthEnabled: cfg.Auth.TestAuthEnabled, TestAuthUUIDs: cfg.Auth.TestAuthUUIDs,
		PurchaseMinInterval:          time.Duration(cfg.Plugin.PurchaseMinIntervalSeconds) * time.Second,
		Routing:                      routingSvc,
		RouteQueryRateLimitPerSecond: func() float64 { return settingsSvc.Route().QueryRateLimitPerSecond },
	})
	center := httpapi.NewCenter(httpapi.CenterOptions{
		API: api, Store: st, Cache: cache, RBAC: rbacSvc, Settings: settingsSvc,
		Plugin: configRequester, PluginOnline: pluginServer.Online,
		OnSettingsChanged: routingSvc.InvalidateCache, Logger: logger,
	})

	r := buildRouter(cfg, api, center, pluginServer, hub, logger)

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

func buildRouter(cfg *config.Config, api *httpapi.API, center *httpapi.Center, plugin *pluginlink.Server, hub *ws.Hub, logger *slog.Logger) http.Handler {
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
		r.Get("/stations", api.Stations)
		r.Get("/trains", api.Trains)

		r.Get("/auth/login", api.Login)
		r.Get("/auth/callback", api.Callback)
		r.Post("/auth/test-login", api.TestLogin)
		r.Get("/auth/me", api.Me)
		r.Get("/me/history", api.MyRideHistory)
		r.Post("/auth/logout", api.Logout)

		r.Post("/route/query", api.RouteQuery)
		r.Post("/purchase", api.Purchase)
		r.Get("/realtime", hub.HandleWS)

		// 个人中心：每个接口都在后端独立校验登录与权限
		r.Group(func(r chi.Router) {
			r.Use(center.RequireLogin)
			r.Get("/me/profile", center.Profile)
			r.Get("/me/overview", center.Overview)
			r.Get("/me/players", center.SearchPlayers)

			// 铁路系统 / 线路：成员关系在 handler 内按私有快照校验
			r.Get("/me/systems/{id}", center.GetSystem)
			r.Put("/me/systems/{id}", center.UpdateSystem)
			r.Delete("/me/systems/{id}", center.DeleteSystem)
			r.Put("/me/systems/{id}/logo/{kind}", center.UploadSystemLogo)
			r.Delete("/me/systems/{id}/logo/{kind}", center.DeleteSystemLogo)
			r.Get("/me/systems/{id}/lines", center.ListSystemLines)
			r.Put("/me/lines/{lineId}", center.UpdateLine)
			r.Delete("/me/lines/{lineId}", center.DeleteLine)

			r.With(center.RequirePerm(rbac.PermStationAlias)).Route("/me/station-aliases", func(r chi.Router) {
				r.Get("/", center.ListStationAliases)
				r.Put("/{station}", center.UpsertStationAlias)
				r.Delete("/{station}", center.DeleteStationAlias)
			})
			r.With(center.RequirePerm(rbac.PermStationGroup)).Route("/me/station-groups", func(r chi.Router) {
				r.Get("/", center.ListStationGroups)
				r.Put("/{group}", center.UpsertStationGroup)
				r.Delete("/{group}", center.DeleteStationGroup)
			})

			r.With(center.RequirePerm(rbac.PermRBAC)).Route("/admin/rbac", func(r chi.Router) {
				r.Get("/", center.RBACOverview)
				r.Post("/roles", center.CreateRole)
				r.Put("/roles/{roleId}", center.UpdateRole)
				r.Delete("/roles/{roleId}", center.DeleteRole)
				r.Post("/users", center.GrantRole)
				r.Delete("/users/{uuid}/roles/{roleId}", center.RevokeRole)
			})
			r.With(center.RequirePerm(rbac.PermSettings)).Route("/admin/settings", func(r chi.Router) {
				r.Get("/", center.GetSettings)
				r.Put("/{section}", center.UpdateSettings)
				r.Post("/{section}/reset", center.ResetSettings)
			})
			r.With(center.RequirePerm(rbac.PermAudit)).Get("/admin/audit", center.ListAudit)
		})
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
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
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
