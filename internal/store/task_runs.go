package store

import "time"

// TaskRun 是扫描/刮削/备份的一次执行记录。
type TaskRun struct {
	Kind       string `json:"Kind"`
	StartedAt  int64  `json:"StartedAt"`
	FinishedAt int64  `json:"FinishedAt"`
	Status     string `json:"Status"`
	Detail     string `json:"Detail"`
}

// RecordTaskRun 写入一条执行记录，只保留最近 200 条。
func (d *DB) RecordTaskRun(kind string, started, finished int64, status, detail string) error {
	if finished <= 0 {
		finished = time.Now().Unix()
	}
	if _, err := d.Exec(
		`INSERT INTO task_runs(kind, started_at, finished_at, status, detail) VALUES(?,?,?,?,?)`,
		kind, started, finished, status, detail); err != nil {
		return err
	}
	_, _ = d.Exec(`DELETE FROM task_runs WHERE id NOT IN (
		SELECT id FROM task_runs ORDER BY id DESC LIMIT 200)`)
	return nil
}

// TaskRuns 返回最近的执行记录，最新的在前。
func (d *DB) TaskRuns(limit int) ([]TaskRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := d.Query(`
SELECT kind, started_at, finished_at, status, detail
FROM task_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TaskRun, 0, limit)
	for rows.Next() {
		var t TaskRun
		if err := rows.Scan(&t.Kind, &t.StartedAt, &t.FinishedAt, &t.Status, &t.Detail); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
