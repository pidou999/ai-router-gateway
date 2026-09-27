package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newProbeServer 构造一个按固定状态码与响应体作答的假上游。
func newProbeServer(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func TestProbeVision(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantKnown  bool // 是否应得出明确结论
		wantVision bool // 仅在 wantKnown 为 true 时校验
	}{
		{
			name:       "2xx 正常返回视为支持图片",
			status:     200,
			body:       `{"choices":[{"message":{"content":"ok"}}]}`,
			wantKnown:  true,
			wantVision: true,
		},
		{
			name:       "400 明确点名不支持图片",
			status:     400,
			body:       `{"error":{"message":"This model does not support image input"}}`,
			wantKnown:  true,
			wantVision: false,
		},
		{
			name:       "400 content 必须是字符串（纯文本模型的典型报错）",
			status:     400,
			body:       `{"error":{"message":"content must be a string"}}`,
			wantKnown:  true,
			wantVision: false,
		},
		{
			name:       "422 参数错误也判定为不支持",
			status:     422,
			body:       `{"error":{"message":"invalid request payload"}}`,
			wantKnown:  true,
			wantVision: false,
		},
		{
			name:      "401 鉴权失败不下结论",
			status:    401,
			body:      `{"error":{"message":"invalid api key"}}`,
			wantKnown: false,
		},
		{
			name:      "429 限流不下结论",
			status:    429,
			body:      `{"error":{"message":"rate limit exceeded"}}`,
			wantKnown: false,
		},
		{
			name:      "500 服务端错误不下结论",
			status:    500,
			body:      `{"error":{"message":"internal error"}}`,
			wantKnown: false,
		},
		{
			name:      "400 但原因是余额不足，与图片能力无关",
			status:    400,
			body:      `{"error":{"message":"insufficient balance"}}`,
			wantKnown: false,
		},
		{
			name:       "200 包裹的错误体仍能识别为不支持",
			status:     200,
			body:       `{"error":{"message":"unsupported content type: image_url"}}`,
			wantKnown:  true,
			wantVision: false,
		},
	}

	h := &ProviderHandler{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newProbeServer(tc.status, tc.body)
			defer srv.Close()

			got := h.probeVision(srv.URL, "test-key", "some-model")
			if tc.wantKnown {
				if got.Supported == nil {
					t.Fatalf("期望得出明确结论，实际未得出（message=%s）", got.Message)
				}
				if *got.Supported != tc.wantVision {
					t.Errorf("vision = %v，期望 %v（message=%s）", *got.Supported, tc.wantVision, got.Message)
				}
				return
			}
			if got.Supported != nil {
				t.Errorf("期望不下结论，实际得到 vision=%v（message=%s）", *got.Supported, got.Message)
			}
		})
	}
}

func TestMergeProbedCaps(t *testing.T) {
	yes, no := true, false

	t.Run("空历史写入新结论", func(t *testing.T) {
		out, changed := mergeProbedCaps("", map[string]*bool{"vision": &yes})
		if !changed {
			t.Error("期望标记为已变更")
		}
		var caps map[string]bool
		if err := json.Unmarshal([]byte(out), &caps); err != nil {
			t.Fatalf("输出不是合法 JSON：%v", err)
		}
		if !caps["vision"] {
			t.Errorf("期望 vision=true，得到 %v", caps)
		}
	})

	t.Run("nil 结论不覆盖历史值", func(t *testing.T) {
		out, changed := mergeProbedCaps(`{"vision":true}`, map[string]*bool{"vision": nil})
		if changed {
			t.Error("未得出结论时不应标记变更")
		}
		var caps map[string]bool
		json.Unmarshal([]byte(out), &caps)
		if !caps["vision"] {
			t.Errorf("历史结论应被保留，得到 %v", caps)
		}
	})

	t.Run("新结论覆盖旧结论且保留其它键", func(t *testing.T) {
		out, changed := mergeProbedCaps(`{"vision":true,"audio":true}`, map[string]*bool{"vision": &no})
		if !changed {
			t.Error("结论翻转应标记为已变更")
		}
		var caps map[string]bool
		json.Unmarshal([]byte(out), &caps)
		if caps["vision"] {
			t.Errorf("vision 应更新为 false，得到 %v", caps)
		}
		if !caps["audio"] {
			t.Errorf("未涉及的 audio 键应保持不变，得到 %v", caps)
		}
	})

	t.Run("历史值为非法 JSON 时不影响写入", func(t *testing.T) {
		out, _ := mergeProbedCaps("not-json", map[string]*bool{"vision": &yes})
		var caps map[string]bool
		if err := json.Unmarshal([]byte(out), &caps); err != nil {
			t.Fatalf("输出不是合法 JSON：%v", err)
		}
		if !caps["vision"] {
			t.Errorf("期望 vision=true，得到 %v", caps)
		}
	})
}

func TestCapabilityStrings(t *testing.T) {
	t.Run("实测支持时为名字平平的模型补上 vision", func(t *testing.T) {
		caps := capabilityStrings("my-custom-model", map[string]bool{"vision": true})
		if !containsStr(caps, "vision") {
			t.Errorf("期望包含 vision，得到 %v", caps)
		}
		if !containsStr(caps, "text") {
			t.Errorf("text 应始终保留，得到 %v", caps)
		}
	})

	t.Run("实测不支持时剔除关键词误判的 vision", func(t *testing.T) {
		caps := capabilityStrings("gpt-4o", map[string]bool{"vision": false})
		if containsStr(caps, "vision") {
			t.Errorf("实测不支持应剔除 vision，得到 %v", caps)
		}
	})

	t.Run("未探测时沿用关键词推断", func(t *testing.T) {
		caps := capabilityStrings("Qwen/Qwen3-VL-32B-Instruct", nil)
		if !containsStr(caps, "vision") {
			t.Errorf("VL 系列应推断出 vision，得到 %v", caps)
		}
	})
}

func containsStr(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
