package testdb

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"sort"

	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Setup sets up a temporary PostgreSQL container and applies embedded SQL migrations.
// `migrations` is the embed.FS and `dir` is the subdirectory in that FS (e.g. "migrations").
func Setup(migrations embed.FS, dir string) (*sql.DB, func(), error) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16.9",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "testuser",
			"POSTGRES_PASSWORD": "testpass",
			"POSTGRES_DB":       "testdb",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed starting container: %w", err)
	}

	cleanup := func() {
		_ = container.Terminate(ctx)
	}

	host, _ := container.Host(ctx)
	port, _ := container.MappedPort(ctx, "5432")

	dsn := fmt.Sprintf("postgres://testuser:testpass@%s:%s/testdb?sslmode=disable", host, port.Port())
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed connecting to db: %w", err)
	}

	if err := db.Ping(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed pinging db: %w", err)
	}

	goose.SetDialect("postgres")

	// Write embedded migrations to a temp dir
	tempDir, err := os.MkdirTemp("", "migrations-*")
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed creating temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	files, err := fs.ReadDir(migrations, dir)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed reading embedded migrations: %w", err)
	}

	var filenames []string
	for _, f := range files {
		if !f.IsDir() {
			filenames = append(filenames, f.Name())
		}
	}
	sort.Strings(filenames)

	for _, name := range filenames {
		// Use forward slash to build embed FS path (not filepath.Join)
		fpath := dir + "/" + name

		contents, err := migrations.ReadFile(fpath)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("failed reading embedded file %s: %w", fpath, err)
		}

		destPath := tempDir + "/" + name
		if err := os.WriteFile(destPath, contents, 0644); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("failed writing temp file %s: %w", destPath, err)
		}
	}

	if err := goose.Up(db, tempDir); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed running goose.Up: %w", err)
	}

	return db, cleanup, nil
}
