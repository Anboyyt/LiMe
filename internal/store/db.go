package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	*sql.DB
	// searchReady 为 true 时，长度 >= 3 的搜索词走 FTS5 trigram 索引；
	// 建索引失败时保持 false，查询退回 LIKE。
	searchReady bool
}

func Open(path string) (*DB, error) {
	p := filepath.ToSlash(path)
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)",
		p,
	)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// WAL 模式下读写不互斥：多留几个连接，扫描写入时首页查询仍能读，
	// 不会出现扫描期间所有 API 排队几十秒的情况。写事务仍由 SQLite 串行化。
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &DB{DB: sqlDB}, nil
}

func (d *DB) Migrate() error {
	if _, err := d.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`,
	); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}

		var applied int
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}

		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}

		for _, stmt := range splitStatements(string(raw)) {
			if _, err := d.Exec(stmt); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
		}

		if _, err := d.Exec(
			`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
			name, time.Now().Unix(),
		); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements 按 ; 切分 DDL。迁移文件里没有分号字符串和触发器，
// 因此不需要完整的 SQL 词法分析。
func splitStatements(text string) []string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	var out []string
	for _, part := range strings.Split(b.String(), ";") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}
