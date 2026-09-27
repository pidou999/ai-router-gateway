package compressor

import (
	"testing"
)

func TestAutoDetectFilter(t *testing.T) {
	r := NewRTKCompressor()

	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "git diff",
			content:  "diff --git a/file.go b/file.go\n@@ -1,3 +1,3 @@\n-old\n+new\n",
			expected: "gitDiff",
		},
		{
			name:     "git log",
			content:  "commit abc1234def567890 (HEAD) -> main\nAuthor: Test\nDate:   Mon Jan 1 00:00:00 2024 +0000\n\n    commit message\n",
			expected: "gitLog",
		},
		{
			name:     "ls output",
			content:  "total 12\ndrwxr-xr-x  2 user group 4096 Jan  1 00:00 dir1/\n-rw-r--r--  1 user group  100 Jan  1 00:00 file.go\n",
			expected: "ls",
		},
		{
			name:     "grep output",
			content:  "file.go:10:func main() {\n",
			expected: "grep",
		},
		{
			name:     "find output",
			content:  "./src/main.go\n./src/utils/helper.go\n./README.md\n",
			expected: "find",
		},
		{
			name:     "build output",
			content:  "go build ./...\n# package\nCompiling main.go...\n",
			expected: "buildOutput",
		},
		{
			name:     "tree output",
			content:  "├── src\n│   └── main.go\n└── tests\n",
			expected: "tree",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.AutoDetectFilter(tt.content)
			if got != tt.expected {
				t.Errorf("AutoDetectFilter() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCompressGitDiff(t *testing.T) {
	r := NewRTKCompressor()
	content := "diff --git a/file.go b/file.go\n@@ -1,10 +1,10 @@\n line1\n-line2\n+new-line2\n line3\n"
	result, orig, comp := r.Compress(content, "git_diff", 1)
	if result == content {
		t.Log("Git diff content preserved (within limits)")
	}
	t.Logf("Original: %d tokens, Compressed: %d tokens", orig, comp)
}

func TestCompressToolResult(t *testing.T) {
	r := NewRTKCompressor()
	diff := "diff --git a/main.go b/main.go\n@@ -1,5 +1,5 @@\n package main\n \n func main() {\n-\tfmt.Println(\"old\")\n+\tfmt.Println(\"new\")\n }\n"
	result, orig, comp := r.Compress(diff, "tool_result", 1)
	if result == diff {
		t.Log("Content preserved")
	}
	t.Logf("Original: %d, Compressed: %d", orig, comp)
}
