package compressor

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Constants for detection windows and limits
const (
	DETECT_WINDOW       = 500
	SMART_TRUNCATE_MIN  = 30
	SMART_TRUNCATE_HEAD = 20
	SMART_TRUNCATE_TAIL = 20
	DEDUP_LINE_MAX      = 100
	GREP_PER_FILE_MAX   = 20
	TREE_MAX_LINES      = 100
	GIT_DIFF_HUNK_MAX   = 100
	LS_NOISE_DIRS       = 3
)

// RTKCompressor 提供完整的 RTK 压缩功能
type RTKCompressor struct {
	filters map[string]func(string, int) string
}

// NewRTKCompressor 创建新的 RTK 压缩器
func NewRTKCompressor() *RTKCompressor {
	r := &RTKCompressor{
		filters: make(map[string]func(string, int) string),
	}
	r.registerDefaultFilters()
	return r
}

// registerDefaultFilters 注册所有默认过滤器
func (r *RTKCompressor) registerDefaultFilters() {
	r.filters["gitLog"] = r.filterGitLog
	r.filters["gitDiff"] = r.filterGitDiff
	r.filters["gitStatus"] = r.filterGitStatus
	r.filters["buildOutput"] = r.filterBuildOutput
	r.filters["grep"] = r.filterGrep
	r.filters["find"] = r.filterFind
	r.filters["tree"] = r.filterTree
	r.filters["ls"] = r.filterLs
	r.filters["readNumbered"] = r.filterReadNumbered
	r.filters["searchList"] = r.filterSearchList
	r.filters["dedupLog"] = r.filterDedupLog
	r.filters["smartTruncate"] = r.filterSmartTruncate
	r.filters["default"] = r.filterDefault
}

// AutoDetectFilter 自动检测内容类型并返回匹配的过滤器名称
func (r *RTKCompressor) AutoDetectFilter(text string) string {
	head := text
	if len(head) > DETECT_WINDOW {
		head = head[:DETECT_WINDOW]
	}

	if reGitLog.MatchString(head) {
		return "gitLog"
	}
	if reGitDiff.MatchString(head) || reGitHunk.MatchString(head) {
		return "gitDiff"
	}
	if reGitStatus.MatchString(head) || r.isMostlyPorcelain(head) {
		return "gitStatus"
	}
	// Build output检测更宽松
	if strings.Contains(head, "[ERROR]") || strings.Contains(head, "npm ERR!") ||
		strings.Contains(head, "Compiling") || strings.Contains(head, "BUILD FAILED") ||
		strings.Contains(head, "errors:") {
		return "buildOutput"
	}
	if r.isGrepFormat(head) {
		return "grep"
	}
	if r.isFindFormat(head) {
		return "find"
	}
	if reTreeGlyph.MatchString(head) {
		return "tree"
	}
	if reLsTotal.MatchString(head) || r.countLsRows(head) >= 2 {
		return "ls"
	}
	if reSearchListHeader.MatchString(head) {
		return "searchList"
	}
	lines := strings.Split(text, "\n")
	if len(lines) >= SMART_TRUNCATE_MIN && r.isLineNumbered(lines) {
		return "readNumbered"
	}
	if r.countNonEmptyLines(head) >= 5 {
		return "dedupLog"
	}
	if strings.Count(text, "\n") >= SMART_TRUNCATE_MIN {
		return "smartTruncate"
	}
	return "default"
}

// Compress 根据内容和级别压缩文本
func (r *RTKCompressor) Compress(content string, toolName string, level int) (string, int, int) {
	originalTokens := r.estimateTokens(content)

	var compressed string
	switch toolName {
	case "git_diff":
		compressed = r.filterGitDiff(content, level)
	case "git_log":
		compressed = r.filterGitLog(content, level)
	case "git_status":
		compressed = r.filterGitStatus(content, level)
	case "build_output":
		compressed = r.filterBuildOutput(content, level)
	case "grep":
		compressed = r.filterGrep(content, level)
	case "find":
		compressed = r.filterFind(content, level)
	case "tree":
		compressed = r.filterTree(content, level)
	case "ls":
		compressed = r.filterLs(content, level)
	case "read_numbered":
		compressed = r.filterReadNumbered(content, level)
	case "search_list":
		compressed = r.filterSearchList(content, level)
	case "tool_result":
		filterName := r.AutoDetectFilter(content)
		if fn, ok := r.filters[filterName]; ok {
			compressed = fn(content, level)
		} else {
			compressed = r.filterDefault(content, level)
		}
		// 记录统计
		RecordCompression(filterName, originalTokens, r.estimateTokens(compressed))
	default:
		compressed = r.filterDefault(content, level)
	}

	compressedTokens := r.estimateTokens(compressed)
	return compressed, originalTokens, compressedTokens
}

// CompressBatch 批量压缩多个输出
func (r *RTKCompressor) CompressBatch(outputs map[string]string, level int) (map[string]string, int, int, map[string]int) {
	result := make(map[string]string)
	totalOriginal := 0
	totalCompressed := 0
	stats := make(map[string]int)

	for name, content := range outputs {
		filterName := r.AutoDetectFilter(content)
		compressed := r.filterByType(filterName, content, level)
		result[name] = compressed
		stats[filterName]++

		orig, comp := r.estimateTokens(content), r.estimateTokens(compressed)
		totalOriginal += orig
		totalCompressed += comp
	}

	return result, totalOriginal, totalCompressed, stats
}

// GetFilterStats 获取压缩统计信息
func (r *RTKCompressor) GetFilterStats(outputs map[string]string, level int) map[string]int {
	stats := make(map[string]int)
	for _, content := range outputs {
		stats[r.AutoDetectFilter(content)]++
	}
	return stats
}

func (r *RTKCompressor) filterByType(filterName string, content string, level int) string {
	if fn, ok := r.filters[filterName]; ok {
		return fn(content, level)
	}
	return r.filterDefault(content, level)
}

// ========== 过滤器实现 ==========

func (r *RTKCompressor) filterGitLog(content string, level int) string {
	lines := strings.Split(content, "\n")
	var result []string
	seen := make(map[string]bool)
	maxCommits := 10 + level*5

	for _, line := range lines {
		if strings.HasPrefix(line, "commit ") {
			if len(result) >= maxCommits {
				break
			}
			hash := line[7:14]
			if !seen[hash] {
				seen[hash] = true
				result = append(result, line)
			}
		} else if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "|") {
			result = append(result, line)
		} else if strings.HasPrefix(line, "Author:") || strings.HasPrefix(line, "Date:") {
			result = append(result, line)
		} else if strings.TrimSpace(line) != "" && len(result) > 0 {
			msgLines := 0
			for _, l := range result {
				if strings.HasPrefix(l, "    ") || (!strings.HasPrefix(l, "|") && !strings.HasPrefix(l, "commit ")) {
					msgLines++
				}
			}
			if msgLines < 5 {
				result = append(result, line)
			}
		}
	}

	if len(result) == 0 {
		return content
	}
	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterGitDiff(content string, level int) string {
	lines := strings.Split(content, "\n")
	var result []string
	hunkLines := 0
	maxHunkLines := GIT_DIFF_HUNK_MAX + level*20
	maxTotalLines := 200 + level*100

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			if len(result) >= maxTotalLines {
				result = append(result, "... (truncated)")
				break
			}
			result = append(result, line)
			hunkLines = 0
		} else if strings.HasPrefix(line, "@@") {
			result = append(result, line)
			hunkLines = 0
		} else if hunkLines < maxHunkLines && len(result) < maxTotalLines {
			result = append(result, line)
			hunkLines++
		}
	}

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterGitStatus(content string, level int) string {
	lines := strings.Split(content, "\n")
	var result []string
	seen := make(map[string]bool)
	maxEntries := 20 + level*10

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "On branch") || strings.HasPrefix(trimmed, "nothing to commit") {
			result = append(result, line)
			continue
		}
		if !seen[trimmed] {
			seen[trimmed] = true
			if len(result) < maxEntries+3 {
				result = append(result, line)
			}
		}
	}

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterBuildOutput(content string, level int) string {
	lines := strings.Split(content, "\n")
	var result []string
	maxLines := 30 + level*10
	errorCount := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "ERROR") || strings.Contains(trimmed, "error") {
			result = append(result, line)
			errorCount++
			continue
		}
		if strings.Contains(trimmed, "WARN") || strings.Contains(trimmed, "warning") {
			result = append(result, line)
			continue
		}
		if errorCount <= 5 && len(result) < maxLines {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterGrep(content string, level int) string {
	byFile := make(map[string][]string)
	total := 0

	for _, line := range strings.Split(content, "\n") {
		first := strings.Index(line, ":")
		if first == -1 {
			continue
		}
		second := strings.Index(line[first+1:], ":")
		if second == -1 {
			continue
		}
		second += first + 1
		file := line[:first]
		lineNum := line[first+1 : second]
		if !isDigits(lineNum) {
			continue
		}
		total++
		byFile[file] = append(byFile[file], line)
	}

	if total == 0 {
		return content
	}

	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	maxPerFile := GREP_PER_FILE_MAX + level*5
	var result []string
	result = append(result, fmt.Sprintf("%d matches in %d files:", total, len(files)))

	for _, file := range files {
		matches := byFile[file]
		show := matches
		if len(show) > maxPerFile {
			show = show[:maxPerFile]
			result = append(result, fmt.Sprintf("\n[file] %s (%d matches, %d shown):", file, len(matches), len(show)))
		} else {
			result = append(result, fmt.Sprintf("\n[file] %s (%d):", file, len(matches)))
		}
		result = append(result, show...)
	}

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterFind(content string, level int) string {
	lines := strings.Split(content, "\n")
	seen := make(map[string]bool)
	var unique []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !seen[trimmed] {
			seen[trimmed] = true
			unique = append(unique, trimmed)
		}
	}

	sort.Strings(unique)

	maxFiles := 50 + level*25
	if len(unique) > maxFiles {
		unique = unique[:maxFiles]
		unique = append(unique, fmt.Sprintf("... %d more results omitted", len(lines)-maxFiles))
	}

	return strings.Join(unique, "\n")
}

func (r *RTKCompressor) filterTree(content string, level int) string {
	lines := strings.Split(content, "\n")
	var result []string
	maxDepth := 5 + level
	skipped := 0

	for _, line := range lines {
		if strings.Contains(line, "directories") && strings.Contains(line, "files") {
			continue
		}
		if strings.TrimSpace(line) == "" && len(result) == 0 {
			continue
		}

		indent := 0
		for _, c := range line {
			if c == ' ' || c == '\t' {
				indent++
			} else {
				break
			}
		}

		if indent/2 > maxDepth {
			skipped++
			continue
		}

		if skipped > 0 {
			result = append(result, fmt.Sprintf("  ... %d items truncated at depth %d", skipped, maxDepth))
			skipped = 0
		}
		result = append(result, line)
	}

	if skipped > 0 {
		result = append(result, fmt.Sprintf("  ... %d items truncated", skipped))
	}

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterLs(content string, level int) string {
	lines := strings.Split(content, "\n")
	dirCounts := make(map[string]int)
	fileCounts := make(map[string]int)
	var result []string

	dirPattern := regexp.MustCompile(`^(.+)/$`)
	extPattern := regexp.MustCompile(`\.([a-zA-Z0-9]+)$`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "total") {
			continue
		}
		if match := dirPattern.FindStringSubmatch(trimmed); match != nil {
			dirCounts[match[1]]++
		} else {
			ext := "other"
			if match := extPattern.FindStringSubmatch(trimmed); match != nil {
				ext = match[1]
			}
			fileCounts[ext]++
		}
	}

	for dir, count := range dirCounts {
		if count > 1 {
			result = append(result, fmt.Sprintf("%d directories in %s/", count, dir))
		} else {
			result = append(result, dir+"/")
		}
	}

	maxExts := 5 + level
	extList := make([]string, 0, len(fileCounts))
	for ext := range fileCounts {
		extList = append(extList, ext)
	}
	sort.Strings(extList)

	for _, ext := range extList {
		if len(result) >= maxExts {
			break
		}
		result = append(result, fmt.Sprintf("%d *.%s files", fileCounts[ext], ext))
	}

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterReadNumbered(content string, level int) string {
	lines := strings.Split(content, "\n")
	maxLines := 30 + level*10

	if len(lines) <= maxLines {
		return content
	}

	result := make([]string, 0, maxLines+1)
	result = append(result, lines[:maxLines]...)
	result = append(result, fmt.Sprintf("... (%d more lines omitted)", len(lines)-maxLines))

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterSearchList(content string, level int) string {
	lines := strings.Split(content, "\n")
	var result []string
	headerDone := false

	for _, line := range lines {
		if reSearchListHeader.MatchString(line) {
			result = append(result, line)
			headerDone = true
			continue
		}
		if headerDone && len(result) < 20+level*5 {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterDedupLog(content string, level int) string {
	lines := strings.Split(content, "\n")
	var result []string
	prev := ""
	runCount := 0
	blankStreak := 0
	maxLines := DEDUP_LINE_MAX + level*20

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if blankStreak < 1 {
				result = append(result, line)
			}
			blankStreak++
			if runCount > 1 {
				result = append(result, fmt.Sprintf("  ... (%d duplicate lines)", runCount-1))
			}
			prev = ""
			runCount = 0
			continue
		}
		blankStreak = 0
		if line == prev {
			runCount++
			continue
		}
		if runCount > 1 {
			result = append(result, fmt.Sprintf("  ... (%d duplicate lines)", runCount-1))
		}
		result = append(result, line)
		prev = line
		runCount = 1
		if len(result) >= maxLines {
			result = append(result, "... (truncated)")
			break
		}
	}

	if runCount > 1 {
		result = append(result, fmt.Sprintf("  ... (%d duplicate lines)", runCount-1))
	}

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterSmartTruncate(content string, level int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= SMART_TRUNCATE_MIN {
		return content
	}

	keepStart := SMART_TRUNCATE_HEAD + level*5
	keepEnd := SMART_TRUNCATE_TAIL + level*5

	if keepStart+keepEnd >= len(lines) {
		return content
	}

	result := make([]string, 0, keepStart+keepEnd+1)
	result = append(result, lines[:keepStart]...)
	result = append(result, fmt.Sprintf("... (%d lines omitted) ...", len(lines)-keepStart-keepEnd))
	result = append(result, lines[len(lines)-keepEnd:]...)

	return strings.Join(result, "\n")
}

func (r *RTKCompressor) filterDefault(content string, level int) string {
	whitespaceRe := regexp.MustCompile(`[ \t]+`)
	lines := strings.Split(content, "\n")
	var result []string
	maxLines := 50 + level*20

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if len(result) >= maxLines {
			result = append(result, fmt.Sprintf("... (%d more lines omitted)", len(lines)-len(result)))
			break
		}
		collapsed := whitespaceRe.ReplaceAllString(trimmed, " ")
		result = append(result, collapsed)
	}

	return strings.Join(result, "\n")
}

// ========== 辅助函数 ==========

var (
	reGitLog             = regexp.MustCompile(`^[*\|/\\ ]*commit [0-9a-f]{7,40}`)
	reGitDiff            = regexp.MustCompile(`^diff --git `)
	reGitHunk            = regexp.MustCompile(`^@@ `)
	reGitStatus          = regexp.MustCompile(`^On branch |^nothing to commit|^Changes (not |to be )|^Untracked files:`)
	reBuildOutput        = regexp.MustCompile(`(?i)^(npm (warn|error|ERR!)|yarn (warn|error)|\s*Compiling\s+\S+|\s*Downloading\s+\S+|added \d+ package|\[ERROR\]|BUILD (SUCCESS|FAILED)|\s*Finished\s+|Successfully (installed|built)|ERROR:)`)
	reTreeGlyph          = regexp.MustCompile(`[├└]──|│  `)
	reLsTotal            = regexp.MustCompile(`^total \d+$`)
	reLsRow              = regexp.MustCompile(`^[-dlbcps][rwx-]{9}`)
	reSearchListHeader   = regexp.MustCompile(`^Search results for|^Found \d+ files`)
	reLineNumbered       = regexp.MustCompile(`^\d+\|`)
)

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

func (r *RTKCompressor) isGrepFormat(text string) bool {
	lines := strings.Split(text, "\n")
	first5 := lines
	if len(first5) > 5 {
		first5 = first5[:5]
	}
	for _, line := range first5 {
		if r.isGrepLine(line) {
			return true
		}
	}
	return false
}

func (r *RTKCompressor) isGrepLine(line string) bool {
	first := strings.Index(line, ":")
	if first == -1 {
		return false
	}
	second := strings.Index(line[first+1:], ":")
	if second == -1 {
		return false
	}
	second += first + 1
	lineNum := line[first+1 : second]
	return isDigits(lineNum)
}

func (r *RTKCompressor) isFindFormat(text string) bool {
	lines := strings.Split(text, "\n")
	nonEmpty := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		nonEmpty++
		if nonEmpty >= 3 {
			break
		}
		if strings.Contains(trimmed, ":") {
			return false
		}
		if !strings.HasPrefix(trimmed, ".") && !strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, "/") && !strings.HasPrefix(trimmed, `C:\`) && !strings.HasPrefix(trimmed, `D:\`) {
			return false
		}
	}
	return nonEmpty >= 3
}

func (r *RTKCompressor) isMostlyPorcelain(text string) bool {
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0)
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			filtered = append(filtered, line)
		}
	}
	if len(filtered) < 3 {
		return false
	}
	porcelainRe := regexp.MustCompile(`^[ MADRCU?!][ MADRCU?!] \S`)
	hits := 0
	for _, line := range filtered {
		if porcelainRe.MatchString(line) {
			hits++
		}
	}
	return hits*2 >= len(filtered)
}

func (r *RTKCompressor) isLineNumbered(lines []string) bool {
	count := 0
	for _, line := range lines {
		if reLineNumbered.MatchString(line) {
			count++
		}
	}
	return count >= int(float64(len(lines))*0.7)
}

func (r *RTKCompressor) countNonEmptyLines(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func (r *RTKCompressor) countLsRows(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if reLsRow.MatchString(line) {
			count++
		}
	}
	return count
}

func (r *RTKCompressor) estimateTokens(content string) int {
	return int(math.Max(1, float64(len(content))/4))
}
