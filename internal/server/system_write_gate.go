package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// A session advisory lock coordinates writes with queue cutover and restore
// across every process using the same PostgreSQL database.
const systemWriteGateLock int64 = 731984205

type writeGateKey struct{}
type writeGateLease struct {
	db        *gorm.DB
	conn      *sql.Conn
	exclusive bool
	once      sync.Once
}

func acquireSystemWriteGate(ctx context.Context, db *gorm.DB, exclusive bool) (context.Context, func(), error) {
	if db == nil {
		return ctx, func() {}, nil
	}
	if existing, _ := ctx.Value(writeGateKey{}).(*writeGateLease); existing != nil && existing.db == db {
		if exclusive && !existing.exclusive {
			return ctx, nil, errors.New("system write gate: cannot upgrade a shared lease")
		}
		return ctx, func() {}, nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return ctx, nil, err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return ctx, nil, err
	}
	lock, unlock := "pg_advisory_lock_shared", "pg_advisory_unlock_shared"
	if exclusive {
		lock, unlock = "pg_advisory_lock", "pg_advisory_unlock"
	}
	if _, err := conn.ExecContext(ctx, "SELECT "+lock+"($1)", systemWriteGateLock); err != nil {
		// The request may be cancelled after PostgreSQL acquired the lock but
		// before the result reached us. Discard this session in that ambiguous case.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
		return ctx, nil, err
	}
	lease := &writeGateLease{db: db, conn: conn, exclusive: exclusive}
	gateCtx := context.WithValue(ctx, writeGateKey{}, lease)
	release := func() {
		lease.once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var released bool
			if err := conn.QueryRowContext(unlockCtx, "SELECT "+unlock+"($1)", systemWriteGateLock).Scan(&released); err != nil || !released {
				// Discard the session so a leaked session lock cannot return to pool.
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			_ = conn.Close()
		})
	}
	return gateCtx, release, nil
}

func acquireSharedSystemWrite(ctx context.Context, db *gorm.DB) (context.Context, func(), error) {
	return acquireSystemWriteGate(ctx, db, false)
}

func acquireExclusiveSystemWrite(ctx context.Context, db *gorm.DB) (context.Context, func(), error) {
	return acquireSystemWriteGate(ctx, db, true)
}

// systemWriteGateMiddleware coordinates request writes with restore and queue
// migration. These two endpoints acquire the exclusive lease themselves.
func systemWriteGateMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		needsGate := false
		switch c.Request().Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			needsGate = true
		case http.MethodGet:
			needsGate = c.Path() == "/api/admin/infra" || c.Path() == "/api/admin/jobs"
		default:
			return next(c)
		}
		if !needsGate {
			return next(c)
		}
		if c.Path() == "/api/admin/backups/:id/restore" || c.Path() == "/api/admin/infra/queue/switch" {
			return next(c)
		}
		ctx, release, err := acquireSharedSystemWrite(c.Request().Context(), data.DB)
		if err != nil {
			return infraError(c, http.StatusServiceUnavailable, "Site write coordination is unavailable")
		}
		defer release()
		c.SetRequest(c.Request().WithContext(ctx))
		return next(c)
	}
}
