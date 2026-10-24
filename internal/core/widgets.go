package core

import (
	"gorm.io/gorm"
	"time"
)

type Widget struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	Name      string         `json:"name" gorm:"size:80;uniqueIndex"`
	Title     string         `json:"title" gorm:"size:160"`
	Version   int            `json:"version"`
	Status    string         `json:"status" gorm:"size:16;index"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at,omitempty" gorm:"index"`
}
type WidgetVersion struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	WidgetID  uint           `json:"widget_id" gorm:"index:idx_widget_version,unique"`
	Version   int            `json:"version" gorm:"index:idx_widget_version,unique"`
	Source    string         `json:"source" gorm:"type:text"`
	Params    string         `json:"params" gorm:"type:jsonb"`
	Published bool           `json:"published" gorm:"index"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at,omitempty" gorm:"index"`
}
