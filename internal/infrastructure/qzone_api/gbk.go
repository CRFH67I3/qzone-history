package qzone_api

// GBK（CP936）双字节解码表，见 gbk_table.go（离线生成，避免额外依赖）。

import (
	"strings"
	"unicode/utf8"
)

// gbkDecode 把 GBK 字节流解码为 UTF-8 字符串
func gbkDecode(b []byte) (string, bool) {
	if len(gbkTables) == 0 {
		return "", false
	}
	var sb strings.Builder
	ok := true
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		if c == 0x80 { // 欧元符号的旧映射
			sb.WriteRune('€')
			i++
			continue
		}
		if i+1 >= len(b) {
			ok = false
			break
		}
		row, okRow := gbkTables[c]
		lo := b[i+1]
		if !okRow || lo < 0x40 || lo > 0xFE {
			ok = false
			i++
			continue
		}
		r := row[lo-0x40]
		if r == 0 {
			ok = false
			sb.WriteRune(utf8.RuneError)
		} else {
			sb.WriteRune(r)
		}
		i += 2
	}
	return sb.String(), ok
}
