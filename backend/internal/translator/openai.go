package translator

import (
	"encoding/json"

	"ai-router-gateway/internal/models"
)

// OpenAITranslator 是 OpenAI 原生协议的透传实现：
// 请求直接下发，响应原样回写（仍能容错 map 形态，避免类型断言失败）。
// 其流式格式与 OpenAI 兼容，故 NewStream 返回 nil，引擎走默认 OpenAI chunk 解析。
type OpenAITranslator struct{}

func (t *OpenAITranslator) TranslateRequest(req *models.ChatRequest) (any, map[string]string, error) {
	return req, nil, nil
}

func (t *OpenAITranslator) TranslateResponse(resp any) (*models.ChatResponse, error) {
	switch v := resp.(type) {
	case *models.ChatResponse:
		return v, nil
	default:
		b, err := json.Marshal(resp)
		if err != nil {
			return nil, err
		}
		var cr models.ChatResponse
		if err := json.Unmarshal(b, &cr); err != nil {
			return nil, err
		}
		return &cr, nil
	}
}

func (t *OpenAITranslator) NewStream() StreamTranslator {
	return nil
}
