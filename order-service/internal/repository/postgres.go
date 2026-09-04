// Package repository owns every line of SQL in order-service.
//
// Schema management is deliberately NOT in this package any more. The service
// used to call AutoMigrate on boot, which meant the schema was whatever the Go
// structs implied at the moment the process started. That is convenient exactly
// until it is not:
//
//   - it is invisible to review — a column change hides inside a struct diff;
//   - it is not ordered, and two replicas booting together race to apply it;
//   - it never drops or narrows anything, so the schema silently accumulates;
//   - it cannot express a CHECK constraint or a partial index, which is why
//     this file used to carry hand-written DDL immediately after the call;
//   - it has no inverse, so there is nothing to roll back to.
//
// Schema changes are now versioned .sql files under migrations/, applied by
// golang-migrate before the process starts (see the Makefile and the
// docker-compose "migrate" service). What is left here is a boot-time
// assertion that the migrations actually ran — a service that starts against a
// schema it does not understand fails immediately and loudly, rather than at
// the first request that touches the missing column.
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

	"github.com/dhanush-cn/fundkit/order-service/internal/config"
)

// RequiredSchemaVersion is the migration version this build of the code needs.
//
// Bump it in the same commit as the migration that satisfies it. That pairing
// is what turns "someone forgot to run migrations" from a 500 at 3am into a
// container that refuses to start with a message saying exactly what to run.
const RequiredSchemaVersion = 2

// MigrationsTable is the bookkeeping table golang-migrate writes its version
// into. It is namespaced per service rather than left as the default
// "schema_migrations" because api-gateway shares this database; two services
// tracking independent migration histories in one table would each see the
// other's version and conclude they were behind.
const MigrationsTable = "order_service_schema_migrations"

// OpenPostgres dials Postgres, applies pool limits and verifies that the schema
// has been migrated to the version this build requires. The caller owns the
// returned handle and is responsible for closing it.
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

	version, err := VerifySchema(dialCtx, db)
	if err != nil {
		return nil, err
	}

	logger.InfoContext(ctx, "postgres connected",
		slog.Int("max_open_conns", cfg.MaxOpenConns),
		slog.Duration("conn_max_lifetime", cfg.ConnMaxLifetime),
		slog.Int64("schema_version", version),
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

// VerifySchema asserts that golang-migrate has brought this database up to at
// least RequiredSchemaVersion, and returns the version it found.
//
// Three distinct failures are reported separately, because the fix differs:
//
//   - no bookkeeping table: migrations have never been run here at all;
//   - dirty: a migration failed part-way and the database is in an unknown
//     state. This one must never be "fixed" by running up again — the operator
//     has to look at what half-applied, repair it, and then `migrate force` the
//     version. Starting the service against a dirty schema would be writing to
//     a database nobody can describe;
//   - behind: the binary is newer than the schema, which is the ordinary
//     "deployed without migrating" mistake.
//
// A version *ahead* of the requirement is fine and is not an error: during a
// rolling deploy the migration runs first and old pods keep serving. That
// asymmetry is the reason this checks >= rather than ==, and the reason
// migrations have to stay backward compatible for one release.
func VerifySchema(ctx context.Context, db *gorm.DB) (int64, error) {
	type migrationState struct {
		Version int64
		Dirty   bool
	}

	var state migrationState
	result := db.WithContext(ctx).
		Raw("SELECT version, dirty FROM " + MigrationsTable + " LIMIT 1").
		Scan(&state)

	if result.Error != nil {
		return 0, fmt.Errorf(
			"schema check failed: cannot read %s (has golang-migrate been run against this database?): %w",
			MigrationsTable, result.Error)
	}
	if result.RowsAffected == 0 {
		return 0, fmt.Errorf(
			"schema check failed: %s is empty; run `make migrate-up` to apply migrations before starting the service",
			MigrationsTable)
	}
	if state.Dirty {
		return state.Version, fmt.Errorf(
			"schema check failed: migration %d is marked dirty; a previous run failed part-way. "+
				"Inspect the database, repair it by hand, then `migrate force <version>` — do not simply re-run up",
			state.Version)
	}
	if state.Version < RequiredSchemaVersion {
		return state.Version, fmt.Errorf(
			"schema check failed: database is at migration %d but this build requires %d; run `make migrate-up`",
			state.Version, RequiredSchemaVersion)
	}

	return state.Version, nil
}
