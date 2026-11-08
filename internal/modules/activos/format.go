package activos

import (
	"strconv"
	"strings"

	"sgat-api/internal/types"
)

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func date(d *types.Date) string {
	if d == nil || d.IsZero() {
		return ""
	}
	return d.Format("02/01/2006")
}

// money usa coma decimal (formato regional de Ecuador en Excel).
func money(v *float64) string {
	if v == nil {
		return ""
	}
	return strings.Replace(strconv.FormatFloat(*v, 'f', 2, 64), ".", ",", 1)
}
