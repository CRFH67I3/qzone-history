package persistence

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"gorm.io/gorm"
	"qzone-history/internal/domain/entity"
	"qzone-history/internal/domain/repository"
	"qzone-history/pkg/database"
	"strings"
)

type momentRepository struct {
	db database.Database
}

func NewMomentRepository(db database.Database) repository.MomentRepository {
	return &momentRepository{db: db}
}

func (r *momentRepository) BatchImport(ctx context.Context, moments []entity.Moment) error {
	return r.db.DB().WithContext(ctx).Create(&moments).Error
}

func (r *momentRepository) Insert(ctx context.Context, moment entity.Moment) error {
	return r.db.DB().WithContext(ctx).Create(&moment).Error
}

func (r *momentRepository) UpsertMoment(ctx context.Context, moment entity.Moment) error {
	return r.db.DB().WithContext(ctx).Save(&moment).Error
}

func (r *momentRepository) FindByUserQQ(ctx context.Context, userQQ string, limit, offset int) ([]entity.Moment, error) {
	var moments []entity.Moment
	err := r.db.DB().WithContext(ctx).Where("user_qq = ?", userQQ).Limit(limit).Offset(offset).Find(&moments).Error
	return moments, err
}

func (r *momentRepository) AddLike(ctx context.Context, momentID string) error {
	return r.db.DB().WithContext(ctx).Model(&entity.Moment{}).Where("id = ?", momentID).UpdateColumn("likes", gorm.Expr("likes + ?", 1)).Error
}

func (r *momentRepository) AddComment(ctx context.Context, comment entity.Comment) error {
	return r.db.DB().WithContext(ctx).Create(&comment).Error
}

func (r *momentRepository) IncrementViews(ctx context.Context, momentID string) error {
	return r.db.DB().WithContext(ctx).Model(&entity.Moment{}).Where("id = ?", momentID).UpdateColumn("views", gorm.Expr("views + ?", 1)).Error
}

func (r *momentRepository) MarkAsDeleted(ctx context.Context, momentID string) error {
	return r.db.DB().WithContext(ctx).Model(&entity.Moment{}).Where("id = ?", momentID).Update("is_deleted", true).Error
}

func (r *momentRepository) MarkAsReconstructed(ctx context.Context, momentID string) error {
	return r.db.DB().WithContext(ctx).Model(&entity.Moment{}).Where("id = ?", momentID).Update("is_reconstructed", true).Error
}

func (r *momentRepository) FindByID(ctx context.Context, momentID string) (*entity.Moment, error) {
	var moment entity.Moment
	err := r.db.DB().WithContext(ctx).Where("id = ?", momentID).First(&moment).Error
	if err != nil {
		return nil, err
	}
	return &moment, nil
}

// SaveMoment 写入说说及其评论（评论按说说整体替换，避免重复累积）
func (r *momentRepository) SaveMoment(ctx context.Context, moment entity.Moment) error {
	db := r.db.DB().WithContext(ctx)
	existing, err := r.FindByID(ctx, moment.ID)
	if err == nil && existing != nil {
		// 保留已有的统计信息
		moment.Likes = maxInt(moment.Likes, existing.Likes)
		moment.Views = maxInt(moment.Views, existing.Views)
		if moment.Content == "" {
			moment.Content = existing.Content
		}
		if moment.Timestamp.IsZero() {
			moment.Timestamp = existing.Timestamp
			moment.TimeText = existing.TimeText
		}
		if len(moment.ImageURLs) == 0 {
			moment.ImageURLs = existing.ImageURLs
		}
	}
	if strings.HasPrefix(moment.ID, "rec_") {
		moment.IsReconstructed = true
		moment.Source = "activity"
	} else if moment.Source == "" {
		moment.Source = "qzone"
	}
	comments := moment.Comments
	moment.Comments = nil
	if err := db.Save(&moment).Error; err != nil {
		return err
	}
	if err := db.Where("moment_id = ?", moment.ID).Delete(&entity.Comment{}).Error; err != nil {
		return err
	}
	for i := range comments {
		c := comments[i]
		c.MomentID = moment.ID
		if c.ID == "" {
			h := md5.Sum([]byte(moment.ID + c.Content + c.TimeText))
			c.ID = hex.EncodeToString(h[:])
		}
		if c.UserQQ == "" {
			c.UserQQ = moment.UserQQ
		}
		if err := db.Save(&c).Error; err != nil {
			return err
		}
	}
	return nil
}

// PurgeByUserQQ 清掉该用户全部说说与评论
func (r *momentRepository) PurgeByUserQQ(ctx context.Context, userQQ string) error {
	db := r.db.DB().WithContext(ctx)
	ids := make([]string, 0)
	if err := db.Model(&entity.Moment{}).Where("user_qq = ?", userQQ).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) > 0 {
		if err := db.Where("moment_id IN ?", ids).Delete(&entity.Comment{}).Error; err != nil {
			return err
		}
	}
	return db.Where("user_qq = ?", userQQ).Delete(&entity.Moment{}).Error
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
