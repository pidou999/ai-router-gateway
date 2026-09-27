package proxy

import "testing"

func TestSubstituteTemplate(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		extra   string
		want    string
	}{
		{
			name:    "cloudflare accountId",
			baseURL: "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/v1/chat/completions",
			extra:   `{"accountId":"abc123"}`,
			want:    "https://api.cloudflare.com/client/v4/accounts/abc123/ai/v1/chat/completions",
		},
		{
			name:    "no placeholder",
			baseURL: "https://api.openai.com/v1/chat/completions",
			extra:   `{"accountId":"abc"}`,
			want:    "https://api.openai.com/v1/chat/completions",
		},
		{
			name:    "empty extra config",
			baseURL: "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/v1/chat/completions",
			extra:   ``,
			want:    "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/v1/chat/completions",
		},
		{
			name:    "invalid json keeps template",
			baseURL: "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/v1",
			extra:   `not-json`,
			want:    "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/v1",
		},
		{
			name:    "multiple placeholders",
			baseURL: "https://x.com/{orgId}/{accountId}/chat",
			extra:   `{"orgId":"o1","accountId":"a1"}`,
			want:    "https://x.com/o1/a1/chat",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SubstituteTemplate(c.baseURL, c.extra)
			if got != c.want {
				t.Fatalf("SubstituteTemplate(%q,%q) = %q, want %q", c.baseURL, c.extra, got, c.want)
			}
		})
	}
}
