package qzone_api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"qzone-history/internal/domain/entity"
	"strconv"
	"strings"
	"time"
)

// 说说接口：返回用户自己发的说说（正文、图片、评论、点赞），比互动消息列表干净
const momentListURL = "https://user.qzone.qq.com/proxy/domain/taotao.qq.com/cgi-bin/emotion_cgi_msglist_v6"

// newRequest 构造带 cookie 与常见请求头的 GET 请求
func (c *qzoneAPIClient) newRequest(target string, cookies map[string]string) (*http.Request, error) {
	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://user.qzone.qq.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	for name, value := range cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	return req, nil
}

// fetchJSONP 读取响应体并剥掉 _preloadCallback( ... ); 包装
func (c *qzoneAPIClient) fetchJSONP(req *http.Request) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(decodeBody(body, resp.Header.Get("Content-Type")))
	if i := strings.Index(text, "("); i >= 0 && strings.HasPrefix(text, "_preloadCallback") {
		text = text[i+1:]
		text = strings.TrimSpace(text)
		text = strings.TrimSuffix(text, ";")
		text = strings.TrimSpace(text)
		text = strings.TrimSuffix(text, ")")
		text = strings.TrimSpace(text)
	}
	if !strings.HasPrefix(text, "{") {
		return nil, fmt.Errorf("接口返回异常: %s", headText(text, 120))
	}
	return []byte(text), nil
}

func headText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

type momentListResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Total   int             `json:"total"`
	MsgList json.RawMessage `json:"msglist"`
}

// GetMomentCount 未删除说说的总数
func (c *qzoneAPIClient) GetMomentCount(cookies map[string]string) (int, error) {
	uin := ExtractUinFromCookies(cookies)
	target := fmt.Sprintf("%s?uin=%s&ftype=0&sort=0&pos=0&num=1&replynum=0&g_tk=%s&callback=_preloadCallback&code_version=1&format=jsonp",
		momentListURL, url.QueryEscape(uin), GenerateGTKValue(cookies["p_skey"]))
	req, err := c.newRequest(target, cookies)
	if err != nil {
		return 0, err
	}
	data, err := c.fetchJSONP(req)
	if err != nil {
		return 0, err
	}
	var r momentListResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, fmt.Errorf("解析说说总数失败: %w", err)
	}
	if r.Code != 0 {
		return 0, fmt.Errorf("说说接口报错(%d): %s", r.Code, r.Message)
	}
	return r.Total, nil
}

// GetMoments 拉取一页说说
func (c *qzoneAPIClient) GetMoments(cookies map[string]string, pos, num int) ([]*entity.Moment, error) {
	uin := ExtractUinFromCookies(cookies)
	target := fmt.Sprintf("%s?uin=%s&ftype=0&sort=0&pos=%d&num=%d&replynum=100&g_tk=%s&callback=_preloadCallback&code_version=1&format=jsonp&need_private_comment=1",
		momentListURL, url.QueryEscape(uin), pos, num, GenerateGTKValue(cookies["p_skey"]))
	req, err := c.newRequest(target, cookies)
	if err != nil {
		return nil, err
	}
	data, err := c.fetchJSONP(req)
	if err != nil {
		return nil, err
	}
	var r momentListResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("解析说说列表失败: %w", err)
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("说说接口报错(%d): %s", r.Code, r.Message)
	}
	if len(r.MsgList) == 0 || string(r.MsgList) == "null" {
		return nil, nil
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(r.MsgList, &raw); err != nil {
		return nil, fmt.Errorf("解析说说条目失败: %w", err)
	}

	moments := make([]*entity.Moment, 0, len(raw))
	for _, item := range raw {
		m := &entity.Moment{UserQQ: uin, SenderQQ: uin, Source: "qzone"}
		if cid := jsonStr(item["id"]); cid != "" {
			m.ID = "qzone_" + cid
		}
		m.Content = jsonStr(item["content"])
		created := jsonInt(item["created_time"])
		m.Timestamp = unixTime(created)
		if !m.Timestamp.IsZero() {
			m.TimeText = m.Timestamp.Format("2006年01月02日 15:04")
		}
		if jsonInt(item["cmtnum"]) > 0 {
			m.Comments = parseComments(item["commentlist"], uin)
		}
		m.Likes = int(jsonInt(item["fwdnum"])) // 转发数，点赞接口不在此处返回
		m.ImageURLs = parsePictures(item)
		moments = append(moments, m)
	}
	return moments, nil
}

func parseComments(raw json.RawMessage, userQQ string) []entity.Comment {
	var list []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &list) != nil {
		return nil
	}
	out := make([]entity.Comment, 0, len(list))
	for _, c := range list {
		uin := jsonStr(c["uin"])
		content := jsonStr(c["content"])
		created := jsonInt(c["createTime"])
		cm := entity.Comment{
			ID:        "qzone_c_" + jsonStr(c["commentid"]),
			UserQQ:    uin,
			Content:   content,
			Timestamp: unixTime(created),
		}
		if t := unixTime(created); !t.IsZero() {
			cm.TimeText = t.Format("2006-01-02 15:04")
		}
		if jsonStr(c["commentid"]) == "" {
			cm.ID = ""
		}
		out = append(out, cm)
	}
	return out
}

func parsePictures(item map[string]json.RawMessage) []string {
	var pics []string
	add := func(raw json.RawMessage) {
		var list []map[string]json.RawMessage
		if len(raw) == 0 || json.Unmarshal(raw, &list) != nil {
			return
		}
		for _, p := range list {
			u := jsonStr(p["url1"])
			if u == "" {
				u = jsonStr(p["url2"])
			}
			if u != "" {
				pics = append(pics, u)
			}
		}
	}
	add(item["pic"])
	add(item["video"])
	return pics
}

func jsonStr(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

func jsonInt(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		if v, err := n.Int64(); err == nil {
			return v
		}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		return v
	}
	return 0
}

func unixTime(ts int64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	// 13 位是毫秒，10 位是秒
	if ts > 1e12 {
		return time.UnixMilli(ts).In(time.Local)
	}
	return time.Unix(ts, 0).In(time.Local)
}

// ExtractUinFromCookies 从 cookie 的 uin（形如 o01325937016）里取出纯数字 QQ
func ExtractUinFromCookies(cookies map[string]string) string {
	u := cookies["uin"]
	if u == "" {
		u = cookies["p_uin"]
	}
	u = strings.TrimPrefix(u, "o")
	return strings.TrimLeft(u, "0")
}

// GenerateGTKValue 由 p_skey 计算 g_tk
func GenerateGTKValue(skey string) string {
	h := 5381
	for i := 0; i < len(skey); i++ {
		h += (h << 5) + int(skey[i])
	}
	return strconv.Itoa(h & 0x7fffffff)
}

// readLimited 读取响应体（限制 32MB，防止异常响应撑爆内存）
func readLimited(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	chunk := make([]byte, 64*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			if len(buf) > 32*1024*1024 {
				return buf, fmt.Errorf("响应体过大")
			}
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}

// decodeBody QQ 空间部分接口在无 UTF-8 声明时返回 GBK
func decodeBody(body []byte, contentType string) string {
	low := strings.ToLower(contentType)
	if strings.Contains(low, "utf-8") || strings.Contains(low, "utf8") {
		return string(body)
	}
	if !isValidUTF8(body) || strings.Contains(low, "gbk") || strings.Contains(low, "gb2312") {
		if s, ok := gbkDecode(body); ok {
			return s
		}
	}
	return string(body)
}

func isValidUTF8(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c < 0x80:
			i++
		case c>>5 == 0x6:
			if i+1 >= len(b) || b[i+1]&0xC0 != 0x80 {
				return false
			}
			i += 2
		case c>>4 == 0xE:
			if i+2 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
		case c>>3 == 0x1E:
			if i+3 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 || b[i+3]&0xC0 != 0x80 {
				return false
			}
			i += 4
		default:
			return false
		}
	}
	return true
}
