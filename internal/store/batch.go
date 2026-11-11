package store

import (
	"database/sql"
	"errors"
)

// Batch 把大量写入合并进一个事务。
// 46 万个 STRM 逐个自动提交会产生上百万次 fsync，必须批量化。
type Batch struct {
	tx *sql.Tx
}

func (d *DB) BeginBatch() (*Batch, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	return &Batch{tx: tx}, nil
}

func (b *Batch) Commit() error   { return b.tx.Commit() }
func (b *Batch) Rollback() error { return b.tx.Rollback() }

func (b *Batch) UpsertItem(it *Item) error {
	return upsertItem(b.tx, it)
}

func (b *Batch) UpsertMediaSource(src *MediaSource) error {
	return upsertMediaSource(b.tx, src)
}

// ReplaceMediaStreams 扫描时写文件名解析出的基础轨道信息。
// 已经 ffprobe 探测过的片源保留探测结果，不被重新扫描覆盖。
func (b *Batch) ReplaceMediaStreams(sourceID string, streams []MediaStream) error {
	var status string
	err := b.tx.QueryRow(`SELECT probe_status FROM media_sources WHERE id = ?`, sourceID).Scan(&status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if status == "done" {
		return nil
	}
	return replaceMediaStreams(b.tx, sourceID, streams)
}
