// Package types contiene tipos compartidos entre la base de datos y el JSON.
package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const DateLayout = "2006-01-02"

// Date representa una columna DATE de SQL Server y se serializa como "YYYY-MM-DD".
// Use *Date para columnas que admiten NULL.
type Date struct{ time.Time }

func Today() Date {
	y, m, d := time.Now().Date()
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

func (d Date) String() string { return d.Format(DateLayout) }

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Format(DateLayout))
}

// UnmarshalJSON acepta "YYYY-MM-DD" o una fecha ISO completa (toma solo la parte de fecha).
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("fecha inválida")
	}
	if len(s) >= 10 {
		s = s[:10]
	}
	t, err := time.Parse(DateLayout, strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("fecha inválida, formato esperado AAAA-MM-DD")
	}
	d.Time = t
	return nil
}

// Scan implementa sql.Scanner (columnas DATE de SQL Server).
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		d.Time = time.Time{}
	case time.Time:
		y, m, dd := v.Date()
		d.Time = time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
	case string:
		return d.parse(v)
	case []byte:
		return d.parse(string(v))
	default:
		return fmt.Errorf("no se puede leer %T como fecha", src)
	}
	return nil
}

func (d *Date) parse(s string) error {
	if len(s) >= 10 {
		s = s[:10]
	}
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return err
	}
	d.Time = t
	return nil
}

// Value implementa driver.Valuer: la fecha se envía a medianoche UTC.
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	y, m, dd := d.Date()
	return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC), nil
}
