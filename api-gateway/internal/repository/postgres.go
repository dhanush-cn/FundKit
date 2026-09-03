// Package repository is the gateway's persistence adapter for user accounts.
//
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

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/domain"
)

// OpenPostgres dials Postgres, applies pool limits and runs the identity
// migration. The caller owns the returned handle and must close it.
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

	if err := db.WithContext(dialCtx).AutoMigrate(&domain.User{}); err != nil {
		return nil, fmt.Errorf("migrate identity schema: %w", err)
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
