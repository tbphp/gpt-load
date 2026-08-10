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

	"gpt-load/internal/httplifecycle"
	"gpt-load/internal/platform/config"
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
	engine            *gin.Engine
	config            *config.Config
	db                *gorm.DB
	runtimeState      RuntimeStateLoader
	runtimeCheckpoint RuntimeStateCheckpoint
	lifecycle         *httplifecycle.Coordinator
	controlRuntime    ControlRuntime
	startupBootstrap  StartupBootstrap
	startupRecovery   StartupRecovery
	requestLogs       RequestLogRuntime
	executionRuntime  ExecutionRuntime
	listen            func(network, address string) (net.Listener, error)

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

// StartupRecovery restores durable post-commit control-plane side effects.
type StartupRecovery interface {
	DrainCommittedOperations(context.Context) error
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

// ExecutionRuntime owns provider-execution resources and starts a non-blocking
// shutdown so SDK workers cannot consume the process shutdown deadline.
type ExecutionRuntime interface {
	Start(context.Context) error
	BeginShutdown() <-chan struct{}
}

// AppParams defines dependencies injected into App.
type AppParams struct {
	dig.In

	Engine            *gin.Engine
	Config            *config.Config
	DB                *gorm.DB
	StartupBootstrap  StartupBootstrap
	StartupRecovery   StartupRecovery `optional:"true"`
	RuntimeState      RuntimeStateLoader
	RuntimeCheckpoint RuntimeStateCheckpoint     `optional:"true"`
	Lifecycle         *httplifecycle.Coordinator `optional:"true"`
	ControlRuntime    ControlRuntime
	RequestLogs       RequestLogRuntime
	ExecutionRuntime  ExecutionRuntime `optional:"true"`
}

// NewEngine creates the process HTTP engine and global middleware.
func NewEngine() (*gin.Engine, error) {
	return newEngine(nil)
}

// NewEngineWithLifecycle adds process-wide handler tracking used by the
// production shutdown coordinator.
func NewEngineWithLifecycle(lifecycle *httplifecycle.Coordinator) (*gin.Engine, error) {
	return newEngine(lifecycle)
}

func newEngine(lifecycle *httplifecycle.Coordinator) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.RedirectTrailingSlash = false
	if err := engine.SetTrustedProxies(nil); err != nil {
		return nil, fmt.Errorf("disable trusted proxies: %w", err)
	}
	engine.Use(recoveryMiddleware())
	if lifecycle != nil {
		engine.Use(lifecycle.TrackAll())
	}
	return engine, nil
}

// NewApp creates the application lifecycle manager.
func NewApp(params AppParams) *App {
	return &App{
		engine:            params.Engine,
		config:            params.Config,
		db:                params.DB,
		runtimeState:      params.RuntimeState,
		runtimeCheckpoint: params.RuntimeCheckpoint,
		lifecycle:         params.Lifecycle,
		controlRuntime:    params.ControlRuntime,
		startupBootstrap:  params.StartupBootstrap,
		startupRecovery:   params.StartupRecovery,
		requestLogs:       params.RequestLogs,
		executionRuntime:  params.ExecutionRuntime,
		listen:            net.Listen,
		serveErrors:       make(chan error, 1),
	}
}

// Start initializes platform services, migrates the schema, and starts HTTP.
func (a *App) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.httpServer != nil {
		return fmt.Errorf("application is already started")
	}
	logrus.WithField("event", "startup.begin").Info("application startup started")
	if err := i18n.Init(); err != nil {
		return a.startupFailure("i18n", fmt.Errorf("initialize i18n: %w", err))
	}
	if err := storage.AutoMigrate(a.db); err != nil {
		return a.startupFailure("migration", err)
	}
	logrus.WithField("event", "startup.database_migrate").Info("database migration completed")
	if err := a.startupBootstrap.EnsureInitialState(context.Background()); err != nil {
		return a.startupFailure("bootstrap", fmt.Errorf("bootstrap initial state: %w", err))
	}
	logrus.WithField("event", "startup.bootstrap_complete").Info("initial control state ensured")
	if err := a.runtimeState.Load(context.Background()); err != nil {
		return a.startupFailure("runtime_state", fmt.Errorf("load runtime state: %w", err))
	}
	logrus.WithField("event", "startup.runtime_state_loaded").Info("runtime state loaded")
	if a.startupRecovery != nil {
		if err := a.startupRecovery.DrainCommittedOperations(context.Background()); err != nil {
			return a.startupFailure("recovery", fmt.Errorf("recover committed control operations: %w", err))
		}
		logrus.WithField("event", "startup.operation_recovery").Info("committed control operations recovered")
	}
	if a.runtimeCheckpoint != nil {
		if err := a.runtimeCheckpoint.Restore(context.Background()); err != nil {
			logrus.WithError(err).WithField("event", "startup.checkpoint_restore").Warn(
				"runtime state checkpoint restore failed; continuing with database-backed state",
			)
		} else {
			logrus.WithField("event", "startup.checkpoint_restore").Info("runtime state checkpoint checked")
		}
	}
	if a.executionRuntime != nil {
		if err := a.executionRuntime.Start(context.Background()); err != nil {
			return a.startupFailure("execution_runtime", fmt.Errorf("start execution runtime: %w", err))
		}
		logrus.WithField("event", "startup.execution_runtime_start").Info("execution runtime started")
	}

	address := net.JoinHostPort(a.config.Server.Host, strconv.Itoa(a.config.Server.Port))
	listener, err := a.listen("tcp", address)
	if err != nil {
		return a.startupFailure("listen", fmt.Errorf("listen on %s: %w", address, err))
	}
	logrus.WithFields(logrus.Fields{
		"event":   "startup.server_listen",
		"address": listener.Addr().String(),
	}).Info("HTTP listener bound")
	if a.requestLogs == nil {
		closeErr := listener.Close()
		return a.startupFailure("request_logs", errors.Join(
			fmt.Errorf("start request logs: request log runtime is nil"),
			wrapListenerCloseError(closeErr),
		))
	}
	if err := a.requestLogs.Start(); err != nil {
		closeErr := listener.Close()
		return a.startupFailure("request_logs", errors.Join(
			fmt.Errorf("start request logs: %w", err),
			wrapListenerCloseError(closeErr),
		))
	}
	logrus.WithField("event", "startup.request_log_start").Info("request log runtime started")

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
		logrus.WithField("event", "startup.control_runtime_start").Info("control runtime started")
		a.controlRuntime.Run(runtimeContext)
	}()

	logrus.WithFields(logrus.Fields{
		"event":   "startup.ready",
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

// Stop cancels active work, drains local state, and closes infrastructure.
func (a *App) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	server := a.httpServer
	listener := a.listener
	cancelRuntime := a.runtimeCancel
	runtimeDone := a.runtimeDone
	requestLogs := a.requestLogs
	executionRuntime := a.executionRuntime
	runtimeCheckpoint := a.runtimeCheckpoint
	lifecycle := a.lifecycle
	a.mu.Unlock()

	var errs []error
	if lifecycle != nil {
		lifecycle.BeginShutdown()
		logrus.WithField("event", "shutdown.http_requests_cancel").Info("active HTTP requests canceled")
	}
	if cancelRuntime != nil {
		cancelRuntime()
	}
	var executionShutdownDone <-chan struct{}
	if executionRuntime != nil {
		executionShutdownDone = executionRuntime.BeginShutdown()
	}
	if server != nil {
		if err := server.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shut down HTTP server: %w", err))
			logrus.WithError(err).WithFields(logrus.Fields{
				"event":   "shutdown.http_drain",
				"outcome": "forced",
			}).Warn("HTTP server graceful drain timed out")
			if closeErr := server.Close(); closeErr != nil {
				errs = append(errs, fmt.Errorf("force close HTTP server: %w", closeErr))
			}
		} else {
			logrus.WithFields(logrus.Fields{
				"event":   "shutdown.http_drain",
				"outcome": "graceful",
			}).Info("HTTP server drain completed")
		}
	}
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, fmt.Errorf("close HTTP listener: %w", err))
		}
	}
	if runtimeDone != nil {
		select {
		case <-runtimeDone:
			logrus.WithField("event", "shutdown.control_runtime_stop").Info("control runtime drain completed")
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("wait for control runtime: %w", ctx.Err()))
			logrus.WithError(ctx.Err()).WithFields(logrus.Fields{
				"event":   "shutdown.control_runtime_stop",
				"outcome": "forced",
			}).Warn("control runtime did not drain before shutdown deadline")
		}
	}
	if lifecycle != nil {
		waitErr := lifecycle.Wait(ctx)
		if waitErr != nil {
			errs = append(errs, fmt.Errorf("wait for HTTP handlers: %w", waitErr))
			logrus.WithError(waitErr).WithFields(logrus.Fields{
				"event":   "shutdown.http_handlers_drained",
				"outcome": "forced",
			}).Warn("HTTP handlers did not drain before shutdown deadline")
		}
		if waitErr == nil {
			logrus.WithField("event", "shutdown.http_handlers_drained").Info("HTTP handlers drained")
		}
	}
	if runtimeCheckpoint != nil {
		if err := runtimeCheckpoint.Save(context.Background()); err != nil {
			logrus.WithError(err).WithField("event", "shutdown.checkpoint_save").Warn(
				"runtime state checkpoint could not be saved; continuing shutdown",
			)
		} else {
			logrus.WithField("event", "shutdown.checkpoint_save").Info("runtime state checkpoint saved")
		}
	}
	if requestLogs != nil {
		if err := requestLogs.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop request logs: %w", err))
			logrus.WithError(err).WithFields(logrus.Fields{
				"event":   "shutdown.request_log_drain",
				"outcome": "forced",
			}).Warn("request log runtime did not drain cleanly")
		} else {
			logrus.WithField("event", "shutdown.request_log_drain").Info("request log runtime drained")
		}
	}
	if executionShutdownDone != nil {
		select {
		case <-executionShutdownDone:
			logrus.WithField("event", "shutdown.execution_runtime").Info("execution runtime stopped")
		default:
			logrus.WithFields(logrus.Fields{
				"event":   "shutdown.execution_runtime",
				"outcome": "detached",
			}).Warn("execution runtime is still stopping; process exit will release remaining connections")
		}
	}
	if a.db != nil {
		sqlDB, err := a.db.DB()
		if err != nil {
			errs = append(errs, fmt.Errorf("get database connection pool: %w", err))
			logrus.WithError(err).WithField("event", "shutdown.database_close").Warn("database connection pool unavailable during shutdown")
		} else if err := sqlDB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close database: %w", err))
			logrus.WithError(err).WithField("event", "shutdown.database_close").Warn("database close failed")
		} else {
			logrus.WithField("event", "shutdown.database_close").Info("database closed")
		}
	}

	err := errors.Join(errs...)
	if err != nil {
		logrus.WithError(err).WithField("event", "shutdown.failed").Error("application shutdown finished with errors")
	} else {
		logrus.WithField("event", "shutdown.complete").Info("application shutdown completed")
	}
	return err
}

func (a *App) startupFailure(stage string, err error) error {
	logrus.WithFields(logrus.Fields{
		"event": "startup.failed",
		"stage": stage,
	}).WithError(err).Error("application startup failed")
	return err
}

func wrapListenerCloseError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close listener after request log startup failure: %w", err)
}
