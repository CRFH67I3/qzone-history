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
	if u, ok, err := a.opts.Auth.CheckLocalLoginStatus(ctx); err == nil && ok {
		return u, nil
	}
	return nil, errors.New("本地没有可用的登录用户，无法只导出")
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
	total, err := o.Moments.GetMomentCount(user.Cookies)
	if err != nil {
		return fmt.Errorf("获取说说总数失败: %w", err)
	}
	log.Printf("账号 %s 共有 %d 条未删除说说", user.QQ, total)
	if total <= 0 {
		log.Println("没有可抓取的说说（注意：仅自己可见/已被删除的说说接口拿不到）")
		return nil
	}

	got := 0
	for pos := 0; pos < total; pos += pageSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		list, err := o.Moments.GetMoments(user.Cookies, pos, pageSize)
		if err != nil {
			log.Printf("第 %d~%d 条抓取失败: %v", pos+1, pos+pageSize, err)
			break
		}
		if len(list) == 0 {
			break
		}
		for _, m := range list {
			m.UserQQ = user.QQ
			if m.SenderQQ == "" {
				m.SenderQQ = user.QQ
			}
			if err := o.MomentRepo.SaveMoment(ctx, *m); err != nil {
				log.Printf("保存说说 %s 失败: %v", m.ID, err)
				continue
			}
			got++
		}
		log.Printf("进度 %d/%d", min(pos+len(list), total), total)
		time.Sleep(300 * time.Millisecond)
	}
	log.Printf("本次写入/更新说说 %d 条", got)
	return nil
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
