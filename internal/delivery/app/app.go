package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"qzone-history/internal/domain/entity"
	"qzone-history/internal/domain/repository"
	"qzone-history/internal/domain/usecase"
	"qzone-history/internal/infrastructure/qzone_api"
	"qzone-history/pkg/qrcode"
	"time"
)

// Options 应用运行参数
type Options struct {
	Auth            usecase.AuthUseCase
	Moment          usecase.MomentUseCase
	BoardMessage    usecase.BoardMessageUseCase
	Friend          usecase.FriendUseCase
	Export          usecase.ExportUseCase
	Activity        usecase.ActivityUseCase
	Reconstruct     usecase.ReconstructionUseCase
	Moments         qzone_api.InternalAPI
	MomentRepo      repository.MomentRepository
	ActivityRepo    repository.ActivityRepository
	UserRepo        repository.UserRepository
	ExportOnly      bool
	ForceLogin      bool
	Refetch         bool
	WantActivities  bool
	ListenAddr      string
	WantCookieLogin bool // 不走扫码，改用粘贴 Cookie
}

type App struct {
	opts Options
}

func NewApp(opts Options) *App {
	return &App{opts: opts}
}

const pageSize = 30

func (a *App) Run(ctx context.Context) error {
	o := a.opts

	// 0. 只导出：不联网，直接用本地库
	if o.ExportOnly {
		user, err := a.localUser(ctx)
		if err != nil {
			return err
		}
		return a.exportAll(ctx, user.QQ)
	}

	// 1. 登录（本地有效登录态优先）
	var user *entity.User
	if u, ok, err := o.Auth.CheckLocalLoginStatus(ctx); err == nil && ok && !o.ForceLogin {
		log.Printf("已使用本地保存的登录态：%s（%s）", u.Nickname, u.QQ)
		user = u
	} else if o.WantCookieLogin {
		cookies, err := o.Moments.CookieLogin(ctx, o.ListenAddr)
		if err != nil {
			return err
		}
		u, err := o.Moments.LoginWithCookie(ctx, cookies)
		if err != nil {
			return err
		}
		u.LoginExpireTime = time.Now().Add(24 * time.Hour)
		u.LoginStatus = entity.LoginStatusSuccess
		if err := o.UserRepo.Update(ctx, *u); err != nil {
			log.Printf("保存登录态失败（不影响本次运行）: %v", err)
		}
		log.Printf("Cookie 登录成功：%s（%s）", u.Nickname, u.QQ)
		user = u
	} else {
		u, err := a.login(ctx)
		if err != nil {
			return err
		}
		user = u
	}

	// 2. 可选刷新：清掉旧数据与旧登录态
	if o.Refetch {
		log.Println("--refetch：清理本地旧数据（说说/互动消息/评论/留言板/好友）")
		if err := o.MomentRepo.PurgeByUserQQ(ctx, user.QQ); err != nil {
			return fmt.Errorf("清理说说失败: %w", err)
		}
		if o.ActivityRepo != nil {
			if err := o.ActivityRepo.PurgeByUserQQ(ctx, user.QQ); err != nil {
				return fmt.Errorf("清理互动消息失败: %w", err)
			}
		}
	}

	// 3. 抓说说（主数据源）
	log.Println("开始抓取说说列表…")
	if err := a.fetchMoments(ctx, user); err != nil {
		return err
	}

	// 4. 可选：互动消息 → 重建历史（默认关闭，数据质量差）
	if o.WantActivities {
		log.Println("开始抓取互动消息（--activities）…")
		if _, err := o.Activity.FetchActivities(ctx, *user); err != nil {
			log.Printf("抓取互动消息失败（已跳过）: %v", err)
		} else {
			if err := o.Reconstruct.ReconstructMomentsFromActivities(ctx, user.QQ); err != nil {
				log.Printf("重建说说失败: %v", err)
			}
			if err := o.Reconstruct.ReconstructBoardMessagesFromActivities(ctx, user.QQ); err != nil {
				log.Printf("重建留言板失败: %v", err)
			}
		}
	}

	// 5. 导出
	return a.exportAll(ctx, user.QQ)
}

func (a *App) localUser(ctx context.Context) (*entity.User, error) {
	// 只导出模式：优先取本地任意一个已登录用户，登录态过期也照样导出（导出不联网）
	if u, err := a.opts.UserRepo.GetLastLoginUser(ctx); err == nil && u != nil && u.QQ != "" {
		return u, nil
	}
	if u, ok, err := a.opts.Auth.CheckLocalLoginStatus(ctx); err == nil && ok {
		return u, nil
	}
	return nil, errors.New("本地数据库里没有用户记录，无法只导出")
}

func (a *App) login(ctx context.Context) (*entity.User, error) {
	o := a.opts
	qr, qrsig, err := o.Auth.GetLoginQRCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取登录二维码失败: %w", err)
	}
	shutdown, notifySuccess, err := qrcode.OpenInBrowser(qr, o.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("展示二维码失败: %w", err)
	}
	defer shutdown()
	log.Println("等待手机 QQ 扫码（二维码 2 分钟内有效，无需重试即可）")

	failures := 0
	for {
		status, res, err := o.Auth.CheckQRCodeLoginStatus(ctx, qrsig)
		if err != nil {
			failures++
			if failures > 5 {
				return nil, fmt.Errorf("检查登录状态连续失败: %w", err)
			}
		} else {
			failures = 0
			switch status {
			case entity.LoginStatusExpired:
				return nil, errors.New("二维码已失效，请重新运行")
			case entity.LoginStatusSuccess:
				u, err := o.Auth.CompleteLogin(ctx, res)
				if err != nil {
					return nil, fmt.Errorf("完成登录失败: %w", err)
				}
				notifySuccess()
				log.Printf("扫码登成功：%s（%s）", u.Nickname, u.QQ)
				time.Sleep(time.Second)
				return u, nil
			case entity.LoginStatusScanning:
				log.Println("已扫码，等待手机确认…")
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (a *App) fetchMoments(ctx context.Context, user *entity.User) error {
	o := a.opts
	cookies := user.Cookies

	// 1) 探测：公开 / 含私密 两个范围的 total
	publicTotal, err1 := o.Moments.GetMomentCount(cookies)
	privTotal := 0
	for _, f := range []int{1, 0, 2} {
		fv := f
		if n, err := a.momentCountWithFilter(cookies, fv); err == nil && n > privTotal {
			privTotal = n
		}
	}
	if err1 != nil && privTotal == 0 {
		return fmt.Errorf("获取说说总数失败: %w", err1)
	}
	if privTotal < publicTotal {
		privTotal = publicTotal
	}
	log.Printf("公开可见 %d 条 / 含仅自己可见最多 %d 条", publicTotal, privTotal)

	seen := map[string]bool{}
	saved := 0

	save := func(m *entity.Moment) bool {
		if m == nil || m.ID == "" {
			return false
		}
		if seen[m.ID] {
			return false
		}
		m.UserQQ = user.QQ
		if m.SenderQQ == "" {
			m.SenderQQ = user.QQ
		}
		if err := o.MomentRepo.SaveMoment(ctx, *m); err != nil {
			log.Printf("保存说说 %s 失败: %v", m.ID, err)
			return false
		}
		seen[m.ID] = true
		saved++
		return true
	}

	// 2) 主通道：msglist_v6，公开范围
	fetch := func(name string, total int, get func(pos, num int) ([]*entity.Moment, error)) {
		if total <= 0 {
			return
		}
		got := 0
		for pos := 0; pos < total; pos += pageSize {
			select {
			case <-ctx.Done():
				return
			default:
			}
			list, err := get(pos, pageSize)
			if err != nil {
				log.Printf("[%s] 第 %d~%d 条失败: %v", name, pos+1, pos+pageSize, err)
				break
			}
			if len(list) == 0 {
				break
			}
			for _, m := range list {
				if save(m) {
					got++
				}
			}
			time.Sleep(300 * time.Millisecond)
		}
		log.Printf("[%s] 拉取完成，新增 %d 条", name, got)
	}

	fetch("公开通道", publicTotal, func(pos, num int) ([]*entity.Moment, error) {
		return o.Moments.GetMoments(cookies, pos, num)
	})

	// 3) 私密范围：带 filter 参数再扫一遍（哪个 filter 数量大用哪个）
	for _, f := range []int{1, 0, 2} {
		fv := f
		n, err := a.momentCountWithFilter(cookies, fv)
		if err != nil || n <= publicTotal {
			continue
		}
		log.Printf("[含私密通道] filter=%d 报告 %d 条，开始补拉", fv, n)
		fetch(fmt.Sprintf("含私密 filter=%d", fv), n, func(pos, num int) ([]*entity.Moment, error) {
			return o.Moments.GetMomentsWithFilter(cookies, pos, num, &fv)
		})
	}

	// 4) 网页通道：ic2 / qzonetoken，补拉公开通道漏掉的（含仅自己可见）
	if o.Moments.Ic2Available(cookies) {
		fetch("网页通道", privTotal, func(pos, num int) ([]*entity.Moment, error) {
			return o.Moments.GetMomentsIc2(cookies, pos, num)
		})
	} else {
		log.Println("[网页通道] 当前 Cookie 缺少 p_skey，跳过（可用 --cookie 重新登录）")
	}

	log.Printf("本次共写入/更新 %d 条说说（去重后）", saved)
	if saved == 0 {
		log.Println("提示：没有拉到任何说说，可用 --cookie 换用浏览器 Cookie 重试")
	}
	return nil
}

// momentCountWithFilter 用 filter 变体探测总数
func (a *App) momentCountWithFilter(cookies map[string]string, filter int) (int, error) {
	list, err := a.opts.Moments.GetMomentsWithFilter(cookies, 0, 1, &filter)
	if err != nil {
		return 0, err
	}
	_ = list
	return a.opts.Moments.GetMomentCountWithFilter(cookies, filter)
}

func (a *App) exportAll(ctx context.Context, qq string) error {
	o := a.opts
	if err := o.Export.ExportUserDataToJSON(ctx, qq); err != nil {
		return err
	}
	if err := o.Export.ExportUserDataToExcel(ctx, qq); err != nil {
		log.Printf("导出 CSV 失败: %v", err)
	}
	if err := o.Export.ExportUserDataToHTML(ctx, qq); err != nil {
		log.Printf("导出 HTML 失败: %v", err)
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
