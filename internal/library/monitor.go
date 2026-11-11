package library

import (
	"context"
	"log/slog"
	"strconv"
	"sync"

	"lime/internal/store"
)

// Monitor 管理实时监控的启停，运行状态落库，面板开关即时生效。
type Monitor struct {
	db       *store.DB
	scanner  *Scanner
	log      *slog.Logger
	onIngest func([]string)
	defOn    bool

	mu      sync.Mutex
	baseCtx context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewMonitor 创建实时监控控制器；defOn 是没有运行时设置时的默认值。
func NewMonitor(db *store.DB, scanner *Scanner, log *slog.Logger, defOn bool, onIngest func([]string)) *Monitor {
	return &Monitor{db: db, scanner: scanner, log: log, defOn: defOn, onIngest: onIngest}
}

// Enabled 返回当前生效的开关：数据库设置优先，其次用默认值。
func (m *Monitor) Enabled() bool {
	if v, err := m.db.GetSetting("realtime_monitor"); err == nil && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return m.defOn
}

// SetEnabled 保存开关并立即启停监控。
func (m *Monitor) SetEnabled(on bool) error {
	if err := m.db.SetSetting("realtime_monitor", strconv.FormatBool(on)); err != nil {
		return err
	}
	if on {
		m.start()
	} else {
		m.stop()
	}
	return nil
}

// Run 阻塞到 ctx 取消；启动时按当前开关决定是否开始监控。
func (m *Monitor) Run(ctx context.Context) {
	m.mu.Lock()
	m.baseCtx = ctx
	m.mu.Unlock()

	if m.Enabled() {
		m.start()
	} else {
		m.log.Info("realtime monitor disabled")
	}
	<-ctx.Done()
	m.stop()
}

func (m *Monitor) start() {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return
	}
	base := m.baseCtx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	done := make(chan struct{})
	m.cancel = cancel
	m.done = done
	m.mu.Unlock()

	w := NewWatcher(m.db, m.scanner, m.log, 0, m.onIngest)
	m.log.Info("realtime monitor enabled")
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
}

func (m *Monitor) stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	<-done
	m.log.Info("realtime monitor disabled")
}
