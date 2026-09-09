package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// SQLite 重建被外键引用的表前，必须在事务外关闭外键执行，避免 DROP 级联删除子表数据。
// 整条迁移链仍在 BEGIN IMMEDIATE 中串行执行，提交前统一校验全部外键。
func applySQLiteMigrationRegistry(db *gorm.DB, entries []migration) error {
	return db.Connection(func(connection *gorm.DB) (resultErr error) {
		conn, ok := connection.Statement.ConnPool.(*sql.Conn)
		if !ok {
			return fmt.Errorf("SQLite migration connection is not pinned")
		}
		ctx := context.Background()
		var foreignKeys int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			return err
		}
		active := false
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var cleanupErr error
			if active {
				_, cleanupErr = conn.ExecContext(cleanup, "ROLLBACK")
			}
			restore := "PRAGMA foreign_keys = OFF"
			if foreignKeys != 0 {
				restore = "PRAGMA foreign_keys = ON"
			}
			_, err := conn.ExecContext(cleanup, restore)
			cleanupErr = errors.Join(cleanupErr, err)
			var restored int
			if err := conn.QueryRowContext(cleanup, "PRAGMA foreign_keys").Scan(&restored); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
			} else if restored != foreignKeys {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("restore SQLite foreign key enforcement failed"))
			}
			if cleanupErr != nil {
				// 丢弃清理失败的连接，不能将未知事务或外键状态交还连接池。
				if err := conn.Raw(func(any) error { return driver.ErrBadConn }); err != nil && !errors.Is(err, driver.ErrBadConn) {
					cleanupErr = errors.Join(cleanupErr, err)
				}
			}
			resultErr = errors.Join(resultErr, cleanupErr)
		}()
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		active = true
		tx := connection.Session(&gorm.Session{NewDB: true, SkipDefaultTransaction: true, Context: ctx})
		if err := applyMigrationsLocked(tx, entries, false); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		active = false
		return nil
	})
}
