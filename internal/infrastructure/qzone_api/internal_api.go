package qzone_api

import (
	"context"

	"qzone-history/internal/domain/entity"
)

// InternalAPI 是给应用层额外使用的接口（说这说明页拉取、总数查询）
type InternalAPI interface {
	GetMomentCount(cookies map[string]string) (int, error)
	GetMoments(cookies map[string]string, pos, num int) ([]*entity.Moment, error)
	CookieLogin(ctx context.Context, listenAddr string) (map[string]string, error)
	LoginWithCookie(ctx context.Context, cookies map[string]string) (*entity.User, error)
	GetMomentsWithFilter(cookies map[string]string, pos, num int, filter *int) ([]*entity.Moment, error)
	GetMomentsIc2(cookies map[string]string, pos, num int) ([]*entity.Moment, error)
	Ic2Available(cookies map[string]string) bool
	GetMomentCountWithFilter(cookies map[string]string, filter int) (int, error)
}
