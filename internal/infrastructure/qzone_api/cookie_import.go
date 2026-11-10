package qzone_api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"qzone-history/internal/domain/entity"
	"runtime"
	"strings"
	"time"
)

// CookieLogin 兜底登录：不依赖 ptlogin2 接口。
// 起一个本地小页面让用户粘贴一段 Cookie（浏览器 F12 里复制），
// 之后所有请求都用这份 Cookie —— QQ 接口收紧时这是最稳的路子。
func (c *qzoneAPIClient) CookieLogin(ctx context.Context, listenAddr string) (map[string]string, error) {
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("无法监听端口: %w", err)
	}
	defer ln.Close()
	url := "http://" + ln.Addr().String()

	got := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(cookiePage))
	})
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		cookie := strings.TrimSpace(r.FormValue("cookie"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if cookie == "" {
			w.Write([]byte("<meta charset=utf-8>Cookie 为空，返回再试"))
			return
		}
		w.Write([]byte("<meta charset=utf-8><h3>已收到 Cookie，可以关闭这个页面了</h3>"))
		select {
		case got <- cookie:
		default:
		}
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	fmt.Printf("\n┌─ 需要手动提供 Cookie（说明见下方链接）\n│  打开: %s\n│  步骤: 电脑浏览器登录 user.qzone.qq.com → F12 → Network → 任选一个请求 →\n│        Request Headers 里复制整行 Cookie 粘贴进去 → 提交\n└─\n", url)
	openBrowserSafe(url)

	timeout := time.NewTimer(15 * time.Minute)
	defer timeout.Stop()
	select {
	case raw := <-got:
		cookies := parseCookieHeader(raw)
		if len(cookies) == 0 {
			return nil, fmt.Errorf("Cookie 解析失败")
		}
		return cookies, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timeout.C:
		return nil, fmt.Errorf("等待 Cookie 超时（15 分钟）")
	}
}

func parseCookieHeader(raw string) map[string]string {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "Cookie:")
	out := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 || kv[0] == "" {
			continue
		}
		out[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return out
}

func openBrowserSafe(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "android":
		if p, err := exec.LookPath("termux-open"); err == nil {
			cmd = exec.Command(p, url)
			break
		}
		return
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Start()
}

// LoginWithCookie 用 Cookie 拿用户信息，组装成 entity.User
func (c *qzoneAPIClient) LoginWithCookie(ctx context.Context, cookies map[string]string) (*entity.User, error) {
	u, err := c.GetUserInfo(cookies)
	if err != nil {
		return nil, fmt.Errorf("Cookie 可能已失效: %w", err)
	}
	u.Cookies = cookies
	return u, nil
}

const cookiePage = `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>粘贴 QQ空间 Cookie</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;max-width:720px;margin:24px auto;padding:0 16px;color:#222;background:#f7f8fa}
h2{font-size:20px}.card{background:#fff;border-radius:10px;padding:16px;box-shadow:0 1px 3px rgba(0,0,0,.08);margin:12px 0}
textarea{width:100%;height:140px;font-family:ui-monospace,monospace;font-size:13px;border:1px solid #ddd;border-radius:8px;padding:10px;box-sizing:border-box}
button{margin-top:12px;width:100%;padding:12px;font-size:16px;border:0;border-radius:8px;background:#1677ff;color:#fff}
ol{line-height:1.9;font-size:14px;padding-left:20px}code{background:#eef1f5;padding:1px 5px;border-radius:4px}
</style></head><body>
<h2>粘贴 Cookie 继续</h2>
<div class="card"><ol>
<li>电脑浏览器打开 <code>user.qzone.qq.com</code> 并登录</li>
<li>按 <code>F12</code> 打开开发者工具 → <code>Network</code>（网络）</li>
<li>刷新页面，点左侧任意一个 <code>qzone.qq.com</code> 请求</li>
<li>右侧 <code>Headers</code> → 找到 <code>Cookie:</code>，把后面那一长串整体复制</li>
<li>粘贴到下面，点提交</li>
</ol></div>
<div class="card">
<form method="post" action="/submit">
<textarea name="cookie" placeholder="uin=o0123456789; skey=@abc123; p_skey=...; p_uin=o0123456789; ..."></textarea>
<button type="submit">提交 Cookie</button>
</form></div>
<p style="color:#888;font-size:13px">Cookie 只在你本机使用，程序不会上传到任何地方。</p>
</body></html>`
