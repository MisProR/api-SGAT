package database

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"sgat-api/internal/httpx"
)

// List ejecuta la consulta y mapea cada fila a T por nombre de columna (tag db).
func List[T any](ctx context.Context, q Querier, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []T{}
	scan, err := structScanner[T](rows)
	if err != nil {
		return items, err
	}
	for rows.Next() {
		var item T
		if err := scan(rows, &item); err != nil {
			return items, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Get devuelve exactamente una fila; ErrNoRows si no existe.
func Get[T any](ctx context.Context, q Querier, sql string, args ...any) (T, error) {
	var zero T
	items, err := List[T](ctx, q, sql, args...)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, ErrNoRows
	}
	return items[0], nil
}

// Column lee la primera columna de cada fila.
func Column[T any](rows *Rows) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var v T
		if err := rows.Scan(&v); err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ForEach recorre las filas leyendo en dest y llamando fn por cada una.
func ForEach(rows *Rows, dest []any, fn func() error) error {
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		if err := fn(); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ---------------------------------------------------------------------------
//  Mapeo fila -> struct por nombre de columna
// ---------------------------------------------------------------------------

// fieldIndex es la ruta (por structs embebidos) hasta un campo.
type fieldIndex []int

var fieldCache sync.Map // reflect.Type -> map[string]fieldIndex

// structFields devuelve las columnas que admite el tipo: tag db o nombre del campo
// (en minúsculas). Los structs embebidos sin tag se aplanan.
func structFields(t reflect.Type) map[string]fieldIndex {
	if v, ok := fieldCache.Load(t); ok {
		return v.(map[string]fieldIndex)
	}
	out := map[string]fieldIndex{}
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag, hasTag := f.Tag.Lookup("db")
			if tag == "-" {
				continue
			}
			idx := append(append([]int{}, prefix...), i)
			if f.Anonymous && !hasTag {
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft, idx)
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			name := tag
			if name == "" {
				name = f.Name
			}
			name = strings.ToLower(name)
			if _, exists := out[name]; !exists {
				out[name] = idx
			}
		}
	}
	walk(t, nil)
	fieldCache.Store(t, out)
	return out
}

// structScanner prepara la lectura de filas en T según las columnas del resultado.
// Las columnas sin campo correspondiente se descartan y los campos sin columna quedan en cero.
func structScanner[T any](rows *Rows) (func(*Rows, *T) error, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	t := reflect.TypeOf((*T)(nil)).Elem()
	if t.Kind() != reflect.Struct {
		return func(r *Rows, dst *T) error { return r.Scan(dst) }, nil
	}
	fields := structFields(t)
	paths := make([]fieldIndex, len(cols))
	for i, c := range cols {
		paths[i] = fields[strings.ToLower(c)]
	}
	return func(r *Rows, dst *T) error {
		v := reflect.ValueOf(dst).Elem()
		dest := make([]any, len(cols))
		for i, p := range paths {
			if p == nil {
				dest[i] = discard{}
				continue
			}
			dest[i] = fieldByIndexAlloc(v, p).Addr().Interface()
		}
		return r.Scan(dest...)
	}, nil
}

// fieldByIndexAlloc recorre la ruta creando los punteros a structs embebidos si son nil.
func fieldByIndexAlloc(v reflect.Value, idx []int) reflect.Value {
	for i, x := range idx {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v
}

// ---------------------------------------------------------------------------
//  Filtros dinámicos y paginación
// ---------------------------------------------------------------------------

// Where construye condiciones dinámicas con parámetros posicionales ($1, $2...).
// Las condiciones usan "?" como marcador del argumento.
type Where struct {
	conds []string
	args  []any
}

// Arg agrega un argumento y devuelve su marcador ($n).
func (w *Where) Arg(v any) string {
	w.args = append(w.args, v)
	return "$" + strconv.Itoa(len(w.args))
}

// Add agrega una condición; cada "?" se reemplaza por el mismo argumento.
func (w *Where) Add(cond string, arg ...any) *Where {
	if len(arg) > 0 {
		cond = strings.ReplaceAll(cond, "?", w.Arg(arg[0]))
	}
	w.conds = append(w.conds, cond)
	return w
}

// Eq agrega "col = valor" solo si el valor no es nil.
func Eq[T any](w *Where, col string, v *T) {
	if v != nil {
		w.Add(col+" = ?", *v)
	}
}

// SearchCollation compara sin distinguir mayúsculas ni tildes.
const SearchCollation = "Latin1_General_CI_AI"

// Search agrega una búsqueda sin tildes ni mayúsculas sobre la expresión indicada.
func (w *Where) Search(expr, term string) *Where {
	if term == "" {
		return w
	}
	return w.Add("("+expr+") COLLATE "+SearchCollation+" LIKE N'%' + CAST(? AS NVARCHAR(200)) + N'%' COLLATE "+SearchCollation, term)
}

func (w *Where) SQL() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

func (w *Where) Args() []any { return w.args }

// Paginate ejecuta el conteo total y la página solicitada.
//
//	selectSQL: "SELECT col1, col2"   fromSQL: "FROM tabla t JOIN ..."
func Paginate[T any](ctx context.Context, q Querier, selectSQL, fromSQL string, w *Where, orderBy string,
	lq httpx.ListQuery) (httpx.Page[T], error) {
	if w == nil {
		w = &Where{}
	}
	var total int64
	if err := q.QueryRow(ctx, "SELECT COUNT_BIG(*) "+fromSQL+w.SQL(), w.Args()...).Scan(&total); err != nil {
		return httpx.Page[T]{}, err
	}
	sql := selectSQL + " " + fromSQL + w.SQL() + " ORDER BY " + orderBy +
		" OFFSET " + w.Arg(lq.Offset()) + " ROWS FETCH NEXT " + w.Arg(lq.Limit) + " ROWS ONLY"
	items, err := List[T](ctx, q, sql, w.Args()...)
	if err != nil {
		return httpx.Page[T]{}, err
	}
	return httpx.NewPage(items, total, lq), nil
}
