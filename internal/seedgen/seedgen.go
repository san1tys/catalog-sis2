// Package seedgen создаёт небольшой фиксированный каталог для запуска и измерений.
package seedgen

import (
	"fmt"
	"github.com/adilbek/catalog-sis2/internal/domain"
	"github.com/adilbek/catalog-sis2/internal/store/memory"
	"strings"
)

func NewStore(n int) *memory.Store {
	cats := []domain.Category{{ID: 1, Slug: "electronics", Name: "Electronics"}, {ID: 2, ParentID: 1, Slug: "phones", Name: "Phones"}, {ID: 3, ParentID: 1, Slug: "laptops", Name: "Laptops"}}
	s := memory.New(cats, 5)
	for i := 1; i <= n; i++ {
		_, err := s.Add(domain.ProductInput{CategoryID: int64(2 + i%2), BrandID: int64(1 + i%5), Title: fmt.Sprintf("Product %05d phone", i), Description: strings.Repeat("Reliable product with warranty. ", 110), PriceMinor: int64(10000 + i%1000*100), Currency: "KZT", InStock: true, Attributes: map[string]string{"color": "black", "warranty": "12 months"}, ImageKeys: []string{fmt.Sprintf("products/%d.jpg", i)}})
		if err != nil {
			panic(err)
		}
	}
	return s
}
