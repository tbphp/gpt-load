package response

import (
	"math"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	DefaultPageSize = 15
	MaxPageSize     = 1000
)

// Pagination represents the pagination details in a response.
type Pagination struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
}

// PaginatedResponse is the standard structure for all paginated API responses.
type PaginatedResponse struct {
	Items      any        `json:"items"`
	Pagination Pagination `json:"pagination"`
}

// Paginate performs pagination on a GORM query and returns a standardized response.
// It takes a Gin context, a GORM query builder, and a destination slice for the results.
func Paginate(c *gin.Context, query *gorm.DB, dest any) (*PaginatedResponse, error) {
	// 1. Get page and page size from query parameters
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		page = 1
	}

	pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(DefaultPageSize)))
	if err != nil || pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	// 2. Get total count of items
	var totalItems int64
	if err := query.Count(&totalItems).Error; err != nil {
		return nil, err
	}

	// 3. Calculate offset and total pages
	offset := (page - 1) * pageSize
	totalPages := int(math.Ceil(float64(totalItems) / float64(pageSize)))

	// 4. Retrieve the data for the current page
	if err := query.Limit(pageSize).Offset(offset).Find(dest).Error; err != nil {
		return nil, err
	}

	// 5. Construct the paginated response
	paginatedData := &PaginatedResponse{
		Items: dest,
		Pagination: Pagination{
			Page:       page,
			PageSize:   pageSize,
			TotalItems: totalItems,
			TotalPages: totalPages,
		},
	}

	return paginatedData, nil
}
