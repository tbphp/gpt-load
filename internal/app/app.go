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
	"gpt-load/internal/storage/store"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

// App owns the process lifecycle, infrastructure resources, and runtime state.
type App struct {
	engine       *gin.Engine
	config       *config.Config
	encryption   encryption.Service
	store        store.Store
	db           *gorm.DB
	runtimeState RuntimeStateLoader

	mu          sync.Mutex
	httpServer  *http.Server
	listener    net.Listener
	serveErrors chan error
}

// RuntimeStateLoader initializes the in-memory runtime state from persistence.
type RuntimeStateLoader interface {
	Load(context.Context) error
}

// AppParams defines dependencies injected into App.
type AppParams struct {
	dig.In

	Engine       *gin.Engine
	Config       *config.Config
	Encryption   encryption.Service
	Store        store.Store
	DB           *gorm.DB
	RuntimeState RuntimeStateLoader
}

// NewEngine creates the HTTP engine and health endpoint.
func NewEngine() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.RedirectTrailingSlash = false
	engine.Use(recoveryMiddleware())
	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": version.Version,
		})
	})
	return engine
}

// NewApp creates the application lifecycle manager.
func NewApp(params AppParams) *App {
	return &App{
		engine:       params.Engine,
		config:       params.Config,
		encryption:   params.Encryption,
		store:        params.Store,
		db:           params.DB,
		runtimeState: params.RuntimeState,
		serveErrors:  make(chan error, 1),
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
	if err := a.runtimeState.Load(context.Background()); err != nil {
		return fmt.Errorf("load runtime state: %w", err)
	}

	address := net.JoinHostPort(a.config.Server.Host, strconv.Itoa(a.config.Server.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
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

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.serveErrors <- fmt.Errorf("serve HTTP: %w", err)
		}
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
	a.mu.Lock()
	server := a.httpServer
	a.mu.Unlock()

	var errs []error
	if server != nil {
		if err := server.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shut down HTTP server: %w", err))
			if closeErr := server.Close(); closeErr != nil {
				errs = append(errs, fmt.Errorf("force close HTTP server: %w", closeErr))
			}
		}
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close runtime store: %w", err))
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
