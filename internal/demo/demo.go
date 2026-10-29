// Package demo runs the disposable, shared public demonstration.
package demo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.uber.org/dig"
	"gorm.io/gorm"

	"gpt-load/internal/app"
	"gpt-load/internal/container"
	"gpt-load/internal/control"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/platform/memlimit"
	"gpt-load/internal/platform/securefile"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/webui"
)

const AuthKey = "gpt-load-demo"
const resetInterval = 30 * time.Minute

type instance struct {
	app     *app.App
	engine  *gin.Engine
	db      *gorm.DB
	dir     string
	resetAt time.Time
}

// Run starts the shared demonstration until the context is canceled.
func Run(ctx context.Context) error {
	if err := validateEnvironment(); err != nil {
		return err
	}
	port := 3001
	if value := os.Getenv("PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			return fmt.Errorf("PORT must be between 1 and 65535")
		}
		port = parsed
	}
	host := os.Getenv("HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	utils.SetupLogger(utils.LogConfig{Level: "info", Format: "text"})
	return run(ctx, host, port, resetInterval)
}

func validateEnvironment() error {
	if strings.TrimSpace(os.Getenv("DATABASE_DSN")) != "" {
		return fmt.Errorf("demo requires an empty DATABASE_DSN and always uses a new temporary SQLite database")
	}
	return nil
}

func run(ctx context.Context, host string, port int, interval time.Duration) error {
	for ctx.Err() == nil {
		current, err := newInstance(host, port, interval)
		if err != nil {
			return err
		}
		if err := current.app.Start(); err != nil {
			return errors.Join(err, current.close())
		}
		logrus.WithFields(logrus.Fields{"event": "demo.ready", "reset_at": current.resetAt}).Info("Shared demo ready; all data is temporary")
		timer := time.NewTimer(time.Until(current.resetAt))
		select {
		case <-ctx.Done():
		case err = <-current.app.ServeErrors():
		case <-timer.C:
			logrus.WithField("event", "demo.reset").Info("Restoring demo data")
		}
		timer.Stop()
		if stopErr := current.close(); err != nil || stopErr != nil {
			return errors.Join(err, stopErr)
		}
	}
	return nil
}

func newInstance(host string, port int, interval time.Duration) (*instance, error) {
	// 不读取 .env、DATA_DIR、AUTH_KEY 或 ENCRYPTION_KEY，避免演示启动接触用户数据。
	dir, err := os.MkdirTemp("", "gpt-load-demo-")
	if err != nil {
		return nil, err
	}
	if err := securefile.PrepareManagedDataDir(dir); err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	current := &instance{dir: dir, resetAt: time.Now().Add(interval)}
	autoSync := false
	cfg := &config.Config{
		Server:  config.ServerConfig{Host: host, Port: port, GracefulShutdownTimeout: 10, ReadTimeout: 60, IdleTimeout: 120},
		DataDir: dir, DatabaseDSN: filepath.Join(dir, "gpt-load.db"),
		DatabaseMetadata: config.DatabaseMetadata{Source: config.DatabaseSourceManaged, Driver: config.DatabaseDriverSQLite},
		DatabasePool:     config.DefaultDatabasePoolConfig(),
		AuthKey:          AuthKey, AuthKeyMetadata: config.SecretMetadata{Source: config.SecretSourceEnvironment},
		EncryptionKeyMetadata:     config.SecretMetadata{Source: config.SecretSourceKeyFile, Path: filepath.Join(dir, "encryption.key")},
		ModelsDevAutoSyncOverride: &autoSync,
		Log:                       config.LogConfig{Level: "info", Format: "text"},
	}
	d, err := container.BuildContainer(func(d *dig.Container) error {
		for _, decorator := range []any{
			func() *config.Config { return cfg },
			func() app.ControlRuntime { return idleRuntime{} },
			func(loader app.RuntimeStateLoader, service *control.Service, db *gorm.DB) app.RuntimeStateLoader {
				return seededLoader{loader: loader, service: service, db: db}
			},
			func(engine *gin.Engine, web *webui.Server) *gin.Engine {
				engine.Use(i18n.Middleware(), guard(current.resetAt, web.HTTPModule()))
				return engine
			},
		} {
			if err := d.Decorate(decorator); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		err = d.Invoke(func(application *app.App, engine *gin.Engine, db *gorm.DB, memory memlimit.Memory) {
			current.app, current.engine, current.db = application, engine, db
			memlimit.ApplyGoMemoryLimit(memory)
		})
	}
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	return current, nil
}

func (current *instance) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopErr := current.app.Stop(ctx)
	// dir 只来自本进程的 MkdirTemp，绝不使用用户传入的目录执行删除。
	removeErr := os.RemoveAll(current.dir)
	return errors.Join(stopErr, removeErr)
}

type idleRuntime struct{}

func (idleRuntime) Run(ctx context.Context) { <-ctx.Done() }
