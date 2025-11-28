package core

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

// PostService handles post operations.
type PostService struct {
	db *gorm.DB
}

func NewPostService(db *gorm.DB) *PostService {
	return &PostService{db: db}
}

func (s *PostService) Create(post *Post) error {
	if post.Slug == "" {
		post.Slug = s.GenerateSlug(post.Title)
	}
	post.CreatedAt = time.Now()
	post.UpdatedAt = time.Now()
	return s.db.Create(post).Error
}

func (s *PostService) Update(post *Post) error {
	post.UpdatedAt = time.Now()
	return s.db.Save(post).Error
}

func (s *PostService) Delete(id uint) error {
	return s.db.Delete(&Post{}, id).Error
}

func (s *PostService) GetByID(id uint) (*Post, error) {
	var post Post
	err := s.db.First(&post, id).Error
	return &post, err
}

func (s *PostService) GenerateSlug(title string) string {
	slug := strings.ToLower(title)
	slug = strings.ReplaceAll(slug, " ", "-")
	// TODO: Remove special chars and check for duplicates
	return slug
}
