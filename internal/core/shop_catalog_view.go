package core

import "html/template"

// CatalogProductView contains product fields used by the public shop cards.
// Image is the first safe, locally hosted image from Product.Images.
type CatalogProductView struct {
	Product
	Image            string
	DefaultVariantID uint
	Stock            int
	VariantCount     int
	Images           []string
	Summary          string
	DescriptionHTML  template.HTML
	Attributes       []CatalogProductAttribute
}

type CatalogProductAttribute struct {
	Label string
	Value string
}

// CatalogVariantView adds stock presentation state to a sellable variant.
type CatalogVariantView struct {
	ProductVariant
	Available bool
}
