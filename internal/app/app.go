// Package app provides the 2.0 application lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

// App owns the process lifecycle, infrastructure resources, and runtime state.
type App struct {
	engine           *gin.Engine
	config           *config.Config
	encryption       encryption.Service
	db               *gorm.DB
	runtimeState     RuntimeStateLoader
	controlRuntime   ControlRuntime
	startupBootstrap StartupBootstrap
	requestLogs      RequestLogRuntime
	listen           func(network, address string) (net.Listener, error)

	mu            sync.Mutex
	httpServer    *http.Server
	listener      net.Listener
	serveErrors   chan error
	runtimeCancel context.CancelFunc
	runtimeDone   chan struct{}
}

// RuntimeStateLoader initializes the in-memory runtime state from persistence.
type RuntimeStateLoader interface {
	Load(context.Context) error
}

// StartupBootstrap ensures required persisted state exists before runtime loading.
type StartupBootstrap interface {
	EnsureInitialState(context.Context) error
}

// ControlRuntime runs background control-plane maintenance.
type ControlRuntime interface {
	Run(context.Context)
}

// RequestLogRuntime owns the asynchronous request log worker lifecycle.
type RequestLogRuntime interface {
	Start() error
	Stop(context.Context) error
}

// AppParams defines dependencies injected into App.
type AppParams struct {
	dig.In

	Engine           *gin.Engine
	Config           *config.Config
	Encryption       encryption.Service
	DB               *gorm.DB
	StartupBootstrap StartupBootstrap
	RuntimeState     RuntimeStateLoader
	ControlRuntime   ControlRuntime
	RequestLogs      RequestLogRuntime
}

// NewEngine creates the HTTP engine and health endpoint.
func NewEngine() (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.RedirectTrailingSlash = false
	if err := engine.SetTrustedProxies(nil); err != nil {
		return nil, fmt.Errorf("disable trusted proxies: %w", err)
	}
	engine.Use(recoveryMiddleware())
	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": version.Version,
		})
	})
	return engine, nil
}

// NewApp creates the application lifecycle manager.
func NewApp(params AppParams) *App {
	return &App{
		engine:           params.Engine,
		config:           params.Config,
		encryption:       params.Encryption,
		db:               params.DB,
		runtimeState:     params.RuntimeState,
		controlRuntime:   params.ControlRuntime,
		startupBootstrap: params.StartupBootstrap,
		requestLogs:      params.RequestLogs,
		listen:           net.Listen,
		serveErrors:      make(chan error, 1),
	}
}

// Start initializes platform services, migrates the schema, and starts HTTP.
func (a *App) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.httpServer != nil {
		return fmt.Errorf("application is already started")
	}
	if err := i18n.Init(); err != nil {
		return fmt.Errorf("initialize i18n: %w", err)
	}
	if err := storage.AutoMigrate(a.db); err != nil {
		return err
	}
	if err := a.startupBootstrap.EnsureInitialState(context.Background()); err != nil {
		return fmt.Errorf("bootstrap initial state: %w", err)
	}
	if err := a.runtimeState.Load(context.Background()); err != nil {
		return fmt.Errorf("load runtime state: %w", err)
	}

	address := net.JoinHostPort(a.config.Server.Host, strconv.Itoa(a.config.Server.Port))
	listener, err := a.listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	if a.requestLogs == nil {
		closeErr := listener.Close()
		return errors.Join(
			fmt.Errorf("start request logs: request log runtime is nil"),
			wrapListenerCloseError(closeErr),
		)
	}
	if err := a.requestLogs.Start(); err != nil {
		closeErr := listener.Close()
		return errors.Join(
			fmt.Errorf("start request logs: %w", err),
			wrapListenerCloseError(closeErr),
		)
	}

	server := &http.Server{
		Addr:              address,
		Handler:           a.engine,
		ReadTimeout:       time.Duration(a.config.Server.ReadTimeout) * time.Second,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       time.Duration(a.config.Server.IdleTimeout) * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	a.httpServer = server
	a.listener = listener
	runtimeContext, cancelRuntime := context.WithCancel(context.Background())
	runtimeDone := make(chan struct{})
	a.runtimeCancel = cancelRuntime
	a.runtimeDone = runtimeDone

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.serveErrors <- fmt.Errorf("serve HTTP: %w", err)
		}
	}()
	go func() {
		defer close(runtimeDone)
		a.controlRuntime.Run(runtimeContext)
	}()

	logrus.WithFields(logrus.Fields{
		"address": listener.Addr().String(),
		"version": version.Version,
	}).Info("GPT-Load 2.0 server started")
	return nil
}

// ServeErrors reports an unexpected terminal error from the HTTP accept loop.
func (a *App) ServeErrors() <-chan error {
	return a.serveErrors
}

// Address returns the bound listener address after Start succeeds.
func (a *App) Address() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.listener == nil {
		return ""
	}
	return a.listener.Addr().String()
}

// Stop gracefully shuts down HTTP and closes infrastructure resources.
func (a *App) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	server := a.httpServer
	cancelRuntime := a.runtimeCancel
	runtimeDone := a.runtimeDone
	requestLogs := a.requestLogs
	a.mu.Unlock()

	var errs []error
	if cancelRuntime != nil {
		cancelRuntime()
	}
	if server != nil {
		if err := server.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shut down HTTP server: %w", err))
			if closeErr := server.Close(); closeErr != nil {
				errs = append(errs, fmt.Errorf("force close HTTP server: %w", closeErr))
			}
		}
	}
	if runtimeDone != nil {
		select {
		case <-runtimeDone:
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("wait for control runtime: %w", ctx.Err()))
		}
	}
	if requestLogs != nil {
		if err := requestLogs.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop request logs: %w", err))
		}
	}
	if a.db != nil {
		sqlDB, err := a.db.DB()
		if err != nil {
			errs = append(errs, fmt.Errorf("get database connection pool: %w", err))
		} else if err := sqlDB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close database: %w", err))
		}
	}

	return errors.Join(errs...)
}

func wrapListenerCloseError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close listener after request log startup failure: %w", err)
}
