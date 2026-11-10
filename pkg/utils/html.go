package utils

import (
	"regexp"
	"strconv"
	"strings"
)

// ProcessOldHTML 把 QQ 空间互动消息接口返回的 JS 片段还原成可解析的 HTML。
//
// 修复要点（旧实现有 bug）：
//  1. 旧版 `sourceString[startIndex : len(sourceString)-37]` 丢掉了 endIndex，
//     会把整段响应（含其它条目和 JS）当成 HTML，导致内容重复/串行；
//  2. 旧版无条件删除所有反斜杠，会把 \t \uXXXX 等转义撕成 "t"/"u" 之类垃圾字符；
//     现在按 JSON 规则反转义（\u 解码、\t/\n 保留为空白、\/ 还原为 /）。
//
// 找不到 html:'…' 标记时直接返回原文，交给 goquery 尽力解析。
func ProcessOldHTML(message string) string {
	start := "html:'"
	i := strings.Index(message, start)
	if i < 0 {
		return replaceMultipleSpaces(unescapeJSONish(message))
	}
	rest := message[i+len(start):]
	end := findHTMLValueEnd(rest)
	if end < 0 {
		return replaceMultipleSpaces(unescapeJSONish(rest))
	}
	return replaceMultipleSpaces(unescapeJSONish(rest[:end]))
}

// findHTMLValueEnd 找到 html 字段值的结束位置（未被转义的 '）
func findHTMLValueEnd(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++ // 跳过转义字符
			continue
		}
		if s[i] == '\'' {
			return i
		}
	}
	return -1
}

// unescapeJSONish 处理 JS/JSON 字符串转义
func unescapeJSONish(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		next := s[i+1]
		switch next {
		case 'u':
			if i+5 < len(s) {
				if v, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
					b.WriteRune(rune(v))
					i += 5
					continue
				}
			}
			b.WriteByte(next)
			i++
		case 't':
			b.WriteByte(' ')
			i++
		case 'n', 'r':
			b.WriteByte(' ')
			i++
		case '"', '\'', '\\', '/':
			b.WriteByte(next)
			i++
		default:
			b.WriteByte(next)
			i++
		}
	}
	return b.String()
}

// replaceMultipleSpaces 合并空白
func replaceMultipleSpaces(s string) string {
	re := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(re.ReplaceAllString(s, " "))
}
