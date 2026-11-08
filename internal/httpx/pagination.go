package httpx

import (
	"net/http"
	"strconv"
	"strings"
)

const (
	defaultLimit = 10
	maxLimit     = 500
)

type PageMeta struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"totalPages"`
}

type Page[T any] struct {
	Items []T
	Meta  PageMeta
}

func NewPage[T any](items []T, total int64, q ListQuery) Page[T] {
	pages := int((total + int64(q.Limit) - 1) / int64(q.Limit))
	return Page[T]{Items: items, Meta: PageMeta{Page: q.Page, Limit: q.Limit, Total: total, TotalPages: pages}}
}

// ListQuery contiene los parámetros comunes de listados: ?page=1&limit=10&search=x&sort=campo&order=asc
type ListQuery struct {
	Page   int
	Limit  int
	Search string
	Sort   string
	Desc   bool
}

func (q ListQuery) Offset() int { return (q.Page - 1) * q.Limit }

// OrderBy traduce el campo de ordenamiento solicitado usando una lista blanca
// (evita inyección SQL). Si no es válido usa el valor por defecto.
func (q ListQuery) OrderBy(allowed map[string]string, def string) string {
	col, ok := allowed[q.Sort]
	if !ok {
		return def
	}
	// SQL Server no tiene NULLS LAST: en DESC los NULL ya van al final; en ASC se fuerzan al final.
	if q.Desc {
		return col + " DESC"
	}
	return "CASE WHEN " + col + " IS NULL THEN 1 ELSE 0 END, " + col + " ASC"
}

func ParseListQuery(r *http.Request) ListQuery {
	v := r.URL.Query()
	page, _ := strconv.Atoi(v.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(v.Get("limit"))
	if limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	search := strings.TrimSpace(v.Get("search"))
	if len(search) > 100 {
		search = search[:100]
	}
	return ListQuery{
		Page:   page,
		Limit:  limit,
		Search: search,
		Sort:   v.Get("sort"),
		Desc:   strings.EqualFold(v.Get("order"), "desc"),
	}
}
