package server

import (
	"errors"
	"net/http"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// softDeleteProduct moves a product to the recycle bin: its slug becomes "<slug>~<id>"
// so a new product may reuse the slug, and DeletedByID records the admin who deleted it.
// Unknown or already trashed products answer 404.
func softDeleteProduct(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Product not found"})
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		var product core.Product
		if err := tx.Where("id = ?", id).Take(&product).Error; err != nil {
			return err
		}
		if err := tx.Model(&product).Updates(map[string]any{
			"slug":          trashedSlug(product.Slug, id),
			"deleted_by_id": user.ID,
		}).Error; err != nil {
			return err
		}
		return tx.Delete(&product).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Product not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to delete product"})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
}
