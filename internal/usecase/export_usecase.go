// export_usecase.go

package usecase

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"qzone-history/internal/domain/entity"
	"qzone-history/internal/domain/repository"
	"qzone-history/internal/domain/usecase"
	"sort"
	"strings"
)

type exportUseCase struct {
	momentRepo       repository.MomentRepository
	boardMessageRepo repository.BoardMessageRepository
	friendRepo       repository.FriendRepository
	outDir           string
}

func NewExportUseCase(
	momentRepo repository.MomentRepository,
	boardMessageRepo repository.BoardMessageRepository,
	friendRepo repository.FriendRepository,
	outDir string,
) usecase.ExportUseCase {
	if outDir == "" {
		outDir = "."
	}
	return &exportUseCase{
		momentRepo:       momentRepo,
		boardMessageRepo: boardMessageRepo,
		friendRepo:       friendRepo,
		outDir:           outDir,
	}
}

func (u *exportUseCase) path(name string) string {
	return filepath.Join(u.outDir, name)
}

func (u *exportUseCase) collect(ctx context.Context, userQQ string) ([]entity.Moment, []entity.BoardMessage, []entity.Friend, error) {
	moments, err := u.momentRepo.FindByUserQQ(ctx, userQQ, -1, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	boards, _ := u.boardMessageRepo.FindByUserQQ(ctx, userQQ, -1, 0)
	friends, _ := u.friendRepo.FindFriendsByUserQQ(ctx, userQQ)
	sort.SliceStable(moments, func(i, j int) bool {
		return moments[i].Timestamp.After(moments[j].Timestamp)
	})
	return moments, boards, friends, nil
}

func (u *exportUseCase) ExportUserDataToJSON(ctx context.Context, userQQ string) error {
	moments, boards, friends, err := u.collect(ctx, userQQ)
	if err != nil {
		return err
	}
	data := struct {
		QQ            string
		Moments       []entity.Moment
		BoardMessages []entity.BoardMessage
		Friends       []entity.Friend
	}{QQ: userQQ, Moments: moments, BoardMessages: boards, Friends: friends}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 JSON 失败: %w", err)
	}
	file := u.path(fmt.Sprintf("%s_export.json", userQQ))
	if err := os.WriteFile(file, raw, 0644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", file, err)
	}
	fmt.Printf("已导出 JSON : %s（%d 条说说）\n", file, len(moments))
	return nil
}

// ExportUserDataToExcel 说明导出为 CSV（Excel 可直接打开，无需第三方库）
func (u *exportUseCase) ExportUserDataToExcel(ctx context.Context, userQQ string) error {
	moments, _, _, err := u.collect(ctx, userQQ)
	if err != nil {
		return err
	}
	file := u.path(fmt.Sprintf("%s_export.csv", userQQ))
	f, err := os.Create(file)
	if err != nil {
		return fmt.Errorf("创建 %s 失败: %w", file, err)
	}
	defer f.Close()
	f.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM，Excel 识别 UTF-8
	w := csv.NewWriter(f)
	_ = w.Write([]string{"时间", "内容", "图片", "评论数", "点赞/转发", "是否重建", "说说ID"})
	commentText := func(m entity.Moment) string {
		var b strings.Builder
		for _, c := range m.Comments {
			b.WriteString(fmt.Sprintf("[%s] %s\n", c.TimeText, c.Content))
		}
		return b.String()
	}
	for _, m := range moments {
		ts := m.TimeText
		if ts == "" && !m.Timestamp.IsZero() {
			ts = m.Timestamp.Format("2006-01-02 15:04")
		}
		_ = w.Write([]string{ts, m.Content, strings.Join(m.ImageURLs, "\n"), fmt.Sprint(len(m.Comments)), fmt.Sprint(m.Likes), fmt.Sprint(m.IsReconstructed), m.ID})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	fmt.Printf("已导出 CSV  : %s（%d 条说说）\n", file, len(moments))
	_ = commentText
	return nil
}

func (u *exportUseCase) ExportUserDataToHTML(ctx context.Context, userQQ string) error {
	moments, _, _, err := u.collect(ctx, userQQ)
	if err != nil {
		return err
	}
	file := u.path(fmt.Sprintf("%s_export.html", userQQ))
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	b.WriteString("<title>QQ空间说说备份 " + html.EscapeString(userQQ) + "</title><style>")
	b.WriteString("body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;max-width:760px;margin:0 auto;padding:16px;background:#f2f3f5;color:#222}")
	b.WriteString(".m{background:#fff;border-radius:10px;padding:14px 16px;margin:12px 0;box-shadow:0 1px 3px rgba(0,0,0,.08)}")
	b.WriteString(".t{color:#888;font-size:12px;margin-bottom:8px}.c{white-space:pre-wrap;line-height:1.6;font-size:15px}")
	b.WriteString(".p img{max-width:120px;border-radius:6px;margin:4px 4px 0 0}.cm{margin-top:8px;padding-top:8px;border-top:1px dashed #ddd;font-size:13px;color:#555}")
	b.WriteString(".cm div{margin:4px 0}.tag{font-size:11px;color:#b26a00;background:#fff4e5;border-radius:4px;padding:1px 6px;margin-left:6px}")
	b.WriteString("</style></head><body>")
	b.WriteString(fmt.Sprintf("<h2>QQ空间说说备份 · %s</h2><p class=\"t\">共 %d 条 · 生成时间 %s</p>", html.EscapeString(userQQ), len(moments), "现在"))
	for _, m := range moments {
		ts := m.TimeText
		if ts == "" && !m.Timestamp.IsZero() {
			ts = m.Timestamp.Format("2006-01-02 15:04")
		}
		b.WriteString("<div class=\"m\"><div class=\"t\">" + html.EscapeString(ts))
		if m.IsReconstructed {
			b.WriteString("<span class=\"tag\">由动态记录重建</span>")
		}
		b.WriteString("</div>")
		if m.Content != "" {
			b.WriteString("<div class=\"c\">" + html.EscapeString(m.Content) + "</div>")
		}
		if len(m.ImageURLs) > 0 {
			b.WriteString("<div class=\"p\">")
			for _, u2 := range m.ImageURLs {
				b.WriteString("<img src=\"" + html.EscapeString(u2) + "\" loading=\"lazy\">")
			}
			b.WriteString("</div>")
		}
		if len(m.Comments) > 0 {
			b.WriteString("<div class=\"cm\">")
			for _, c := range m.Comments {
				b.WriteString("<div>" + html.EscapeString(c.Content) + " <span class=\"t\">" + html.EscapeString(c.TimeText) + "</span></div>")
			}
			b.WriteString("</div>")
		}
		b.WriteString("</div>")
	}
	b.WriteString("</body></html>")
	if err := os.WriteFile(file, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", file, err)
	}
	fmt.Printf("已导出 HTML : %s（%d 条说说）\n", file, len(moments))
	return nil
}
