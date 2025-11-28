package core

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

type ProductService struct {
	db *gorm.DB
}

func NewProductService(db *gorm.DB) *ProductService {
	return &ProductService{db: db}
}

func (s *ProductService) Create(product *Product) error {
	if product.Slug == "" {
		product.Slug = s.GenerateSlug(product.Title)
	}
	product.CreatedAt = time.Now()
	product.UpdatedAt = time.Now()
	return s.db.Create(product).Error
}

func (s *ProductService) Update(product *Product) error {
	product.UpdatedAt = time.Now()
	return s.db.Save(product).Error
}

func (s *ProductService) Delete(id uint) error {
	return s.db.Delete(&Product{}, id).Error
}

func (s *ProductService) GetByID(id uint) (*Product, error) {
	var product Product
	err := s.db.First(&product, id).Error
	return &product, err
}

func (s *ProductService) GenerateSlug(title string) string {
	slug := strings.ToLower(title)
	slug = strings.ReplaceAll(slug, " ", "-")
	return slug
}
