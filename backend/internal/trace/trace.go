// Package trace 实现网关请求链路的「四段式」结构化追踪：
//
//	auth → route → translate → upstream
//
// 每个请求在 handler 入口创建 *RequestTrace，经 context.Context 透传到路由引擎；
// 引擎在选路 / 翻译 / 上游三阶段分别记录耗时、选中账号/模型、翻译前后差异、上游状态与错误，
// 以及失败回退时的逐次尝试（attempts）。请求结束时输出为结构化 JSON，既可用于服务端日志排障，
// 也可经 request_logs.request_details 落库查询。
package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ai-router-gateway/internal/logger"
)

// Segment 是四段中的单段（auth/route/translate/upstream）。
type Segment struct {
	Name       string         `json:"name"`
	StartedAt  time.Time      `json:"started_at"`
	DurationMs int64          `json:"duration_ms"`
	Status     string         `json:"status"` // ok | error | skip
	Detail     map[string]any `json:"detail,omitempty"`
}

// Attempt 记录一次上游尝试（含失败回退），便于观察多服务商/多账户兜底过程。
type Attempt struct {
	ProviderID   int64  `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	AccountID    int64  `json:"account_id"`
	Model        string `json:"model"`
	APIType      string `json:"api_type"`
	StatusCode   int    `json:"status_code"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	LatencyMs    int64  `json:"latency_ms"`
}

// RequestTrace 是单次请求的完整追踪，可序列化为结构化 JSON。
type RequestTrace struct {
	mu        sync.Mutex
	RequestID string              `json:"request_id"`
	UserID    int64               `json:"user_id"`
	Model     string              `json:"model,omitempty"`
	StartedAt time.Time           `json:"started_at"`
	EndedAt   time.Time           `json:"ended_at,omitempty"`
	Segments  map[string]*Segment `json:"segments"`
	Attempts  []Attempt           `json:"attempts,omitempty"`
}

var reqCounter uint64

// New 创建一次请求追踪。request_id 由时间戳 + 进程内自增计数器组成，便于日志关联。
func New(userID int64) *RequestTrace {
	id := atomic.AddUint64(&reqCounter, 1)
	return &RequestTrace{
		RequestID: fmt.Sprintf("%d-%d", time.Now().UnixNano(), id),
		UserID:    userID,
		StartedAt: time.Now(),
		Segments:  map[string]*Segment{},
	}
}

type ctxKey struct{}

// WithTrace 把追踪挂到 context，供下游引擎读取。
func WithTrace(ctx context.Context, t *RequestTrace) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// FromContext 从 context 取出追踪；缺失时返回 nil（调用方需判空以保证兼容）。
func FromContext(ctx context.Context) *RequestTrace {
	if t, ok := ctx.Value(ctxKey{}).(*RequestTrace); ok {
		return t
	}
	return nil
}

// StartSegment 以当前时间为起点打开一段（用于自动计时的段）。
func (t *RequestTrace) StartSegment(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.Segments[name]; !ok {
		t.Segments[name] = &Segment{Name: name, StartedAt: time.Now()}
	}
}

// EndSegment 结束一段：耗时为 StartedAt 至今，状态与可选 detail 一并写入。
// detail 为 nil 时保留已存在的 detail（便于先 SetDetail 再 EndSegment）；
// 非 nil 时按合并语义写入，避免结束时把先前记录的上下文（如组合信息）整段丢弃。
func (t *RequestTrace) EndSegment(name, status string, detail map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.Segments[name]
	if !ok {
		s = &Segment{Name: name, StartedAt: t.StartedAt}
		t.Segments[name] = s
	}
	if s.StartedAt.IsZero() {
		s.StartedAt = time.Now()
	}
	s.DurationMs = time.Since(s.StartedAt).Milliseconds()
	s.Status = status
	if detail != nil {
		if s.Detail == nil {
			s.Detail = make(map[string]any, len(detail))
		}
		for k, v := range detail {
			s.Detail[k] = v
		}
	}
}

// RecordSegment 以显式耗时写入/覆盖一段（用于耗时不能简单由起止点表达的段，
// 如翻译 = 请求翻译 + 响应翻译之和、流式上游 = 整段流耗时）。
func (t *RequestTrace) RecordSegment(name, status string, durationMs int64, detail map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Segments[name] = &Segment{
		Name:       name,
		StartedAt:  t.StartedAt,
		DurationMs: durationMs,
		Status:     status,
		Detail:     detail,
	}
}

// SetDetail 合并写入某段的 detail（段不存在则创建，耗时记为 0）。
// 采用「合并」而非「替换」语义：同名 key 以后写为准，先写的其他 key 保留。
// 这样路由阶段先记录的组合信息（combo / intent / picked_model）不会被
// 上游胜出后写入的服务商信息（provider_id / api_type 等）整段覆盖。
func (t *RequestTrace) SetDetail(name string, detail map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.Segments[name]
	if !ok {
		s = &Segment{Name: name}
		t.Segments[name] = s
	}
	if s.Detail == nil {
		s.Detail = make(map[string]any, len(detail))
	}
	for k, v := range detail {
		s.Detail[k] = v
	}
}

// AddAttempt 追加一次上游尝试记录。
func (t *RequestTrace) AddAttempt(a Attempt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Attempts = append(t.Attempts, a)
}

// RecordModel 记录本次请求最终使用的模型名（便于追踪）。
func (t *RequestTrace) RecordModel(m string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Model = m
}

// PickedModel 返回路由阶段选中的「首选模型」（组合 auto 策略经意图识别 + 覆盖度排序后选出的模型）。
// 它与 RecordModel 记录的最终出结果模型（可能因上游报错而回退到组合内其他模型）不同，
// 用于日志中一眼区分「智能路由想用哪个」与「实际最终用了哪个」，避免回退掩盖路由决策。
func (t *RequestTrace) PickedModel() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.Segments["route"]; ok && s.Detail != nil {
		if v, ok := s.Detail["picked_model"]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// JSON 序列化整段追踪为字符串。
func (t *RequestTrace) JSON() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	b, _ := json.Marshal(t)
	return string(b)
}

// Log 输出结构化日志行（标记 [requestDetails]），便于服务端排障检索。
func (t *RequestTrace) Log() {
	t.mu.Lock()
	t.EndedAt = time.Now()
	b, _ := json.Marshal(t)
	t.mu.Unlock()
	l := logger.FromContext(nil)
	l.Info("[requestDetails]", "trace", string(b))
}
