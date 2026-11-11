package qzone_api

// 网页版 QQ 空间说说接口（ic2 通道）：
// 登录后打开 user.qzone.qq.com/{QQ} 看到的说说列表就是这条链路，
// 「仅自己可见」的说说也会返回，并带 "私密" 角标。
//
// 流程：先取首屏页拿 qzonetoken，再用 ic2/loadmore 分页。

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"qzone-history/internal/domain/entity"
)

var (
	reQzoneToken = regexp.MustCompile(`window\.shine0_Callback\(\{[\s\S]*?g_qzonetoken\s*=\s*"([^"]+)"`)
	reTid        = regexp.MustCompile(`data-tid="([^"]+)"`)
	rePrivate    = regexp.MustCompile(`数据-(?:私密|不公开)`)
	reTimeAttr   = regexp.MustCompile(`data-time="(\d+)"`)
	reContent    = regexp.MustCompile(`(?:class="con"[^>]*>|class="txt-box"[^>]*>)([\s\S]*?)</div>`)
	reImg        = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
)

// Ic2Available 判断当前 cookie 是否能走网页通道
func (c *qzoneAPIClient) Ic2Available(cookies map[string]string) bool {
	return cookies["p_skey"] != "" && ExtractUinFromCookies(cookies) != ""
}

// GetMomentsIc2 用网页通道拉说说（先试 filter=1 带私密，失败再退回 HTML 解析）
func (c *qzoneAPIClient) GetMomentsIc2(cookies map[string]string, pos, num int) ([]*entity.Moment, error) {
	uin := ExtractUinFromCookies(cookies)
	token, err := c.qzoneToken(cookies, uin)

	one := 1
	moments, ferr := c.GetMomentsWithFilter(cookies, pos, num, &one)
	if ferr == nil && len(moments) > 0 {
		for _, m := range moments {
			if m.Source == "" || m.Source == "qzone" {
				m.Source = "qzone-web"
			}
		}
		return moments, nil
	}
	if err != nil && ferr != nil {
		return nil, fmt.Errorf("网页通道不可用: %v / %v", err, ferr)
	}
	return c.getMomentsHTML(cookies, uin, token, pos, num)
}

// qzoneToken 取首屏里的 g_qzonetoken
func (c *qzoneAPIClient) qzoneToken(cookies map[string]string, uin string) (string, error) {
	req, err := c.newRequest("https://user.qzone.qq.com/"+uin, cookies)
	if err != nil {
		return "", err
	}
	body, err := c.fetchRaw(req)
	if err != nil {
		return "", err
	}
	if m := reQzoneToken.FindStringSubmatch(body); len(m) > 1 {
		return m[1], nil
	}
	// 兜底：cookie 里可能已有
	return cookies["qzonetoken"], nil
}

// getMomentsHTML 用 loadmore 的 HTML 结果兜底解析
func (c *qzoneAPIClient) getMomentsHTML(cookies map[string]string, uin, token string, pos, num int) ([]*entity.Moment, error) {
	target := fmt.Sprintf(
		"https://user.qzone.qq.com/proxy/domain/taotao.qq.com/cgi-bin/emotion_cgi_msglist_v6?uin=%s&ftype=0&sort=0&pos=%d&num=%d&replynum=50&g_tk=%s&callback=_preloadCallback&code_version=1&format=jsonp&need_private_comment=1&qzonetoken=%s",
		uin, pos, num, GenerateGTKValue(cookies["p_skey"]), url.QueryEscape(token))
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
		return nil, err
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("网页通道报错(%d): %s", r.Code, r.Message)
	}
	if len(r.MsgList) == 0 || string(r.MsgList) == "null" {
		return nil, nil
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(r.MsgList, &raw); err != nil {
		return nil, err
	}
	out := make([]*entity.Moment, 0, len(raw))
	for _, item := range raw {
		m := &entity.Moment{UserQQ: uin, SenderQQ: uin, Source: "qzone-web"}
		if cid := jsonStr(item["id"]); cid != "" {
			m.ID = "qzone_" + cid
		}
		m.Content = jsonStr(item["content"])
		created := jsonInt(item["created_time"])
		m.Timestamp = unixTime(created)
		if !m.Timestamp.IsZero() {
			m.TimeText = m.Timestamp.Format("2006年01月02日 15:04")
		}
		m.ImageURLs = parsePictures(item)
		if jsonInt(item["cmtnum"]) > 0 {
			m.Comments = parseComments(item["commentlist"], uin)
		}
		if priv := jsonStr(item["secret"]); priv == "1" {
			m.Source = "qzone-private"
		}
		out = append(out, m)
	}
	return out, nil
}

// parseHTMLMoments 解析 loadmore 返回的 HTML 片段（兜底用）
func parseHTMLMoments(document string, userQQ string) []*entity.Moment {
	blocks := reTid.FindAllStringSubmatchIndex(document, -1)
	var out []*entity.Moment
	for i, b := range blocks {
		end := len(document)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		seg := document[b[0]:end]
		tid := document[b[2]:b[3]]
		m := &entity.Moment{ID: "qzone_" + tid, UserQQ: userQQ, SenderQQ: userQQ, Source: "qzone-web"}
		if rePrivate.MatchString(seg) {
			m.Source = "qzone-private"
		}
		if tm := reTimeAttr.FindStringSubmatch(seg); len(tm) > 1 {
			ts, _ := strconv.ParseInt(tm[1], 10, 64)
			m.Timestamp = unixTime(ts)
			if !m.Timestamp.IsZero() {
				m.TimeText = m.Timestamp.Format("2006年01月02日 15:04")
			}
		}
		if c := reContent.FindStringSubmatch(seg); len(c) > 1 {
			m.Content = strings.TrimSpace(html.UnescapeString(stripTags(c[1])))
		}
		for _, im := range reImg.FindAllStringSubmatch(seg, -1) {
			if u := im[1]; strings.Contains(u, "qpic.cn") {
				m.ImageURLs = append(m.ImageURLs, u)
			}
		}
		out = append(out, m)
	}
	return out
}

func stripTags(s string) string {
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
	return strings.ReplaceAll(s, "&nbsp;", " ")
}

var _ = time.Now
