// Engineered by Dhanush C N (github.com/dhanush-cn)
package repository

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/dhanush-cn/fundkit/order-service/internal/config"
	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
)

// OpenPostgres dials Postgres, applies pool limits and runs migrations. The
// caller owns the returned handle and is responsible for closing it.
func OpenPostgres(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger) (*gorm.DB, error) {
	dialCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	db, err := gorm.Open(postgres.Open(cfg.URL), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("resolve sql handle: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	if err := sqlDB.PingContext(dialCtx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	if err := db.WithContext(dialCtx).AutoMigrate(&domain.Order{}, &domain.OutboxMessage{}); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	// GORM struct tags cannot express a partial index, and the partial index is
	// the difference between an outbox that stays fast and one that degrades as
	// history accumulates. Raw, idempotent DDL applied straight after
	// AutoMigrate has created the table.
	if err := createOutboxIndexes(dialCtx, db); err != nil {
		return nil, err
	}

	logger.InfoContext(ctx, "postgres connected",
		slog.Int("max_open_conns", cfg.MaxOpenConns),
		slog.Duration("conn_max_lifetime", cfg.ConnMaxLifetime),
	)
	return db, nil
}

// ClosePostgres releases the underlying connection pool.
func ClosePostgres(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// PingPostgres backs the /readyz probe.
func PingPostgres(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database not initialised")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return sqlDB.PingContext(pingCtx)
}

// createOutboxIndexes installs the relay's access paths and the status
// constraint. Mirrors migrations/0002_outbox.sql; both are idempotent, so a
// database created either way ends up identical.
func createOutboxIndexes(ctx context.Context, db *gorm.DB) error {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_outbox_pending
		     ON outbox (id) WHERE status = 'PENDING'`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_aggregate
		     ON outbox (aggregate_type, aggregate_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_processed_at
		     ON outbox (processed_at) WHERE status = 'PROCESSED'`,
		// AutoMigrate cannot express a CHECK constraint and Postgres has no
		// CREATE CONSTRAINT IF NOT EXISTS, so drop-then-add keeps this
		// idempotent across restarts.
		`ALTER TABLE outbox DROP CONSTRAINT IF EXISTS outbox_status_check`,
		`ALTER TABLE outbox ADD CONSTRAINT outbox_status_check
		     CHECK (status IN ('PENDING', 'PROCESSED', 'FAILED'))`,
	}
	for _, stmt := range statements {
		if err := db.WithContext(ctx).Exec(stmt).Error; err != nil {
			return fmt.Errorf("apply outbox schema: %w", err)
		}
	}
	return nil
}
