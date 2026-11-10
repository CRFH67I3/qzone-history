# qzone-history 修复版使用说明

## 这次改了什么

1. **换掉数据源**：不再依赖「互动消息」接口（`feeds2_html_pav_all`，只有别人赞我/评论我，拿不到自己的说说），
   改用说说列表接口 `emotion_cgi_msglist_v6`，直接拉账号自己发的说说：正文、原图、评论、点赞、总条数。
2. **修掉乱码**：旧版 `ProcessOldHTML` 会把整段响应当内容、还无条件删掉反斜杠（`\t`→`t`），
   现在严格按 `html:'…'` 边界取值 + 按 JSON 规则反转义，不再出现成串的 `t`。
3. **GBK 自动解码**：接口非 UTF-8 时不再出乱码（内置 GBK 表，无需额外依赖）。
4. **导出更好读**：JSON + CSV（Excel 直接打开）+ 单文件 HTML（手机也能看，带图片和评论）。
5. **登录兜底**：新增 `--cookie`，不走扫码，直接粘贴浏览器里的 Cookie（接口收紧时最稳）。
6. **默认数据库改成 `./app.db`**，与你现有文件同名，24 小时内的登录态可直接复用。

## 用法

```sh
# 正常：扫码登录（复用本地登录态）
./qzone-history

# 接口不稳定时：粘贴 Cookie 登录
./qzone-history --cookie

# 清掉旧脏数据重抓（推荐第一次升级后跑一次）
./qzone-history --refetch

# 不联网，只用本地库重新导出
./qzone-history --export-only

# 打到指定目录
./qzone-history --out ./data --db ./data/app.db
```

Termux 里：

```sh
cd ~/qzone-history
qzone-history --refetch
```

## 产物

| 文件 | 说明 |
|---|---|
| `app.db` | SQLite，原始数据；用 `sqlite3 app.db "select time_text,content from moments"` 查看 |
| `<QQ>_export.json` | 结构化备份 |
| `<QQ>_export.csv` | Excel / WPS 直接打开，一列一行 |
| `<QQ>_export.html` | 双击即看的图文页面，手机上也能滑动阅读 |

## 说明

- 说说接口只返回**未删除**的说说；仅自己可见的、已删除的拿不到（这是接口本身的限制）。
- `--activities` 仍保留，可以把互动消息（赞/评论/留言）也存一份，但默认关闭，避免脏数据污染主表。
- Cookie 只在本机使用，程序不做任何上传。
