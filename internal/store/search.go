package store

import (
	"fmt"
	"strings"
)

// bump 这个版本号即可在下一次启动时强制重建全文索引。
const searchIndexVersion = "2"

// EnsureSearchIndex 建立/校验 FTS5 全文索引。trigram 分词器支持任意
// 3 个字符以上的子串匹配，中文和英文都能用；2 个字符的短词仍走 LIKE。
//
// 返回值 rebuilt 表示这次执行了全量重建（首次部署或版本变化）。
func (d *DB) EnsureSearchIndex() (bool, error) {
	// 触发器必须用单独的 Exec 执行：迁移文件切割器按分号切分，
	// BEGIN...END 里的分号会被切坏。
	stmts := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS items_fts USING fts5(
			name, original_title, sort_name,
			content='items', content_rowid='rowid', tokenize='trigram')`,
		`CREATE TRIGGER IF NOT EXISTS items_fts_ai AFTER INSERT ON items BEGIN
			INSERT INTO items_fts(rowid, name, original_title, sort_name)
			VALUES (new.rowid, new.name, new.original_title, new.sort_name);
		END`,
		`CREATE TRIGGER IF NOT EXISTS items_fts_ad AFTER DELETE ON items BEGIN
			INSERT INTO items_fts(items_fts, rowid, name, original_title, sort_name)
			VALUES ('delete', old.rowid, old.name, old.original_title, old.sort_name);
		END`,
		// WHEN 条件让重扫时（名字没变）不产生额外的索引写入。
		`CREATE TRIGGER IF NOT EXISTS items_fts_au AFTER UPDATE OF name, original_title, sort_name ON items
		WHEN old.name IS NOT new.name
		  OR old.original_title IS NOT new.original_title
		  OR old.sort_name IS NOT new.sort_name
		BEGIN
			INSERT INTO items_fts(items_fts, rowid, name, original_title, sort_name)
			VALUES ('delete', old.rowid, old.name, old.original_title, old.sort_name);
			INSERT INTO items_fts(rowid, name, original_title, sort_name)
			VALUES (new.rowid, new.name, new.original_title, new.sort_name);
		END`,
	}
	// 2 字查询用的 bigram 索引：contentless FTS5，触发器直接把相邻两字写进去。
	bigramTriggers := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS items_bigram USING fts5(
			bigrams, content='', tokenize='unicode61')`,
		`CREATE TRIGGER IF NOT EXISTS items_bigram_ai AFTER INSERT ON items BEGIN
			INSERT INTO items_bigram(rowid, bigrams) VALUES (new.rowid, ` + bigramExpr("new.") + `);
		END`,
		`CREATE TRIGGER IF NOT EXISTS items_bigram_au AFTER UPDATE OF name, original_title, sort_name ON items
		WHEN old.name IS NOT new.name
		  OR old.original_title IS NOT new.original_title
		  OR old.sort_name IS NOT new.sort_name
		BEGIN
			INSERT INTO items_bigram(items_bigram, rowid, bigrams)
			VALUES ('delete', old.rowid, ` + bigramExpr("old.") + `);
			INSERT INTO items_bigram(rowid, bigrams) VALUES (new.rowid, ` + bigramExpr("new.") + `);
		END`,
		`CREATE TRIGGER IF NOT EXISTS items_bigram_ad AFTER DELETE ON items BEGIN
			INSERT INTO items_bigram(items_bigram, rowid, bigrams)
			VALUES ('delete', old.rowid, ` + bigramExpr("old.") + `);
		END`,
	}
	stmts = append(stmts, bigramTriggers...)

	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return false, fmt.Errorf("search index: %w", err)
		}
	}

	// 外部内容 FTS 表的 SELECT 实际读的是 items，COUNT 无法判断索引是否已建，
	// 因此用 settings 里的版本标记决定是否需要全量重建。
	version, err := d.GetSetting("search_index_version")
	if err != nil {
		return false, fmt.Errorf("search index version: %w", err)
	}

	rebuilt := false
	if version != searchIndexVersion {
		if _, err := d.Exec(`INSERT INTO items_fts(items_fts) VALUES('rebuild')`); err != nil {
			return false, fmt.Errorf("search index rebuild: %w", err)
		}
		// 重建 bigram 索引：先清空（contentless 表用 delete-all），再从 items 灌入。
		if _, err := d.Exec(`INSERT INTO items_bigram(items_bigram) VALUES('delete-all')`); err != nil {
			return false, fmt.Errorf("bigram clear: %w", err)
		}
		if _, err := d.Exec(`INSERT INTO items_bigram(rowid, bigrams) SELECT rowid, ` + bigramExpr("") + ` FROM items`); err != nil {
			return false, fmt.Errorf("bigram rebuild: %w", err)
		}
		if err := d.SetSetting("search_index_version", searchIndexVersion); err != nil {
			return false, fmt.Errorf("search index version save: %w", err)
		}
		rebuilt = true
	}
	d.searchReady = true
	return rebuilt, nil
}

// ftsPhrase 把用户输入包成 FTS5 短语字符串，内部双引号翻倍转义。
func ftsPhrase(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// bigramExpr 生成把名字切成相邻两字的 SQL 表达式，最多 24 个位置。
// prefix 用 "new." 表示触发器里的新行，空串表示更新整表。
func bigramExpr(prefix string) string {
	fields := []string{"name", "original_title", "sort_name"}
	var parts []string
	for _, f := range fields {
		col := prefix + f
		for i := 1; i <= 24; i++ {
			parts = append(parts, fmt.Sprintf("substr(%s,%d,2)", col, i))
		}
	}
	return strings.Join(parts, "||' '||")
}
