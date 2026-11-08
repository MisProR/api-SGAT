// Package database administra el pool de conexiones a SQL Server y las transacciones.
//
// Expone una API pequeña (Exec / Query / QueryRow / transacciones) sobre database/sql y
// el driver github.com/microsoft/go-mssqldb. Las consultas pueden escribir los parámetros
// como $1, $2... (se traducen a @p1, @p2... antes de enviarlas a SQL Server).
package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

// ErrNoRows se devuelve cuando QueryRow/Get no encuentran filas.
var ErrNoRows = sql.ErrNoRows

// Opciones de sesión aplicadas a cada conexión nueva. ANSI_NULLS, QUOTED_IDENTIFIER, etc.
// son obligatorias para escribir en tablas con índices filtrados; XACT_ABORT revierte
// la transacción completa ante cualquier error (igual que PostgreSQL).
const sessionInitSQL = `SET ANSI_NULLS ON; SET ANSI_PADDING ON; SET ANSI_WARNINGS ON; SET ARITHABORT ON;
SET CONCAT_NULL_YIELDS_NULL ON; SET QUOTED_IDENTIFIER ON; SET NUMERIC_ROUNDABORT OFF; SET XACT_ABORT ON;
SET DATEFORMAT ymd;`

// CommandTag informa el resultado de un Exec.
type CommandTag struct{ rows int64 }

// RowsAffected devuelve la cantidad de filas afectadas por la sentencia.
func (c CommandTag) RowsAffected() int64 { return c.rows }

// Querier es implementado tanto por el pool como por una transacción,
// lo que permite que los repositorios funcionen dentro o fuera de una tx.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (*Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) *Row
}

// Tx es una transacción abierta.
type Tx interface {
	Querier
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type Options struct {
	URL      string
	MaxConns int32
	MinConns int32
}

// DB es el pool de conexiones a SQL Server.
type DB struct{ sqlDB *sql.DB }

func Connect(ctx context.Context, opt Options) (*DB, error) {
	connector, err := mssql.NewConnector(opt.URL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL inválida: %w", err)
	}
	connector.SessionInitSQL = sessionInitSQL

	sqlDB := sql.OpenDB(connector)
	if opt.MaxConns > 0 {
		sqlDB.SetMaxOpenConns(int(opt.MaxConns))
	}
	idle := int(opt.MinConns)
	if idle < 2 {
		idle = 2
	}
	sqlDB.SetMaxIdleConns(idle)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	sqlDB.SetConnMaxLifetime(time.Hour)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("no se pudo conectar a SQL Server: %w", err)
	}
	return &DB{sqlDB: sqlDB}, nil
}

func (d *DB) Ping(ctx context.Context) error { return d.sqlDB.PingContext(ctx) }

func (d *DB) Close() { _ = d.sqlDB.Close() }

func (d *DB) Exec(ctx context.Context, query string, args ...any) (CommandTag, error) {
	return execOn(ctx, d.sqlDB, query, args)
}

func (d *DB) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	return queryOn(ctx, d.sqlDB, query, args)
}

func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *Row {
	return queryRowOn(ctx, d.sqlDB, query, args)
}

// Begin abre una transacción.
func (d *DB) Begin(ctx context.Context) (Tx, error) {
	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &txConn{tx: tx}, nil
}

type txConn struct{ tx *sql.Tx }

func (t *txConn) Exec(ctx context.Context, query string, args ...any) (CommandTag, error) {
	return execOn(ctx, t.tx, query, args)
}

func (t *txConn) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	return queryOn(ctx, t.tx, query, args)
}

func (t *txConn) QueryRow(ctx context.Context, query string, args ...any) *Row {
	return queryRowOn(ctx, t.tx, query, args)
}

func (t *txConn) Commit(context.Context) error   { return t.tx.Commit() }
func (t *txConn) Rollback(context.Context) error { return t.tx.Rollback() }

// WithTx ejecuta fn dentro de una transacción; hace rollback si fn devuelve error.
func WithTx(ctx context.Context, db *DB, fn func(tx Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
//  Ejecución
// ---------------------------------------------------------------------------

type sqlRunner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func execOn(ctx context.Context, r sqlRunner, query string, args []any) (CommandTag, error) {
	res, err := r.ExecContext(ctx, rebind(query), convertArgs(args)...)
	if err != nil {
		return CommandTag{}, err
	}
	n, _ := res.RowsAffected()
	return CommandTag{rows: n}, nil
}

func queryOn(ctx context.Context, r sqlRunner, query string, args []any) (*Rows, error) {
	rows, err := r.QueryContext(ctx, rebind(query), convertArgs(args)...)
	if err != nil {
		return nil, err
	}
	return &Rows{rows: rows}, nil
}

func queryRowOn(ctx context.Context, r sqlRunner, query string, args []any) *Row {
	return &Row{row: r.QueryRowContext(ctx, rebind(query), convertArgs(args)...)}
}

// Rows recorre el resultado de una consulta.
type Rows struct{ rows *sql.Rows }

func (r *Rows) Next() bool                 { return r.rows.Next() }
func (r *Rows) Err() error                 { return r.rows.Err() }
func (r *Rows) Close()                     { _ = r.rows.Close() }
func (r *Rows) Columns() ([]string, error) { return r.rows.Columns() }
func (r *Rows) Scan(dest ...any) error     { return r.rows.Scan(adaptDest(dest)...) }

// Values devuelve los valores de la fila actual (texto en lugar de []byte).
func (r *Rows) Values() ([]any, error) {
	cols, err := r.rows.Columns()
	if err != nil {
		return nil, err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := r.rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	for i, v := range vals {
		if b, ok := v.([]byte); ok {
			vals[i] = string(b)
		}
	}
	return vals, nil
}

// Row es el resultado de QueryRow.
type Row struct{ row *sql.Row }

func (r *Row) Scan(dest ...any) error { return r.row.Scan(adaptDest(dest)...) }

// ---------------------------------------------------------------------------
//  Parámetros
// ---------------------------------------------------------------------------

// rebind convierte los marcadores $1, $2... en @p1, @p2... (fuera de literales de texto y comentarios).
func rebind(query string) string {
	if !strings.Contains(query, "$") {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 16)
	inString := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '\'' {
			inString = !inString
			b.WriteByte(c)
			continue
		}
		// Comentario de línea: se copia tal cual hasta el fin de línea
		if !inString && c == '-' && i+1 < len(query) && query[i+1] == '-' {
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				b.WriteString(query[i:])
				break
			}
			b.WriteString(query[i : i+end])
			i += end - 1
			continue
		}
		if !inString && c == '$' && i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9' {
			b.WriteString("@p")
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

var (
	timeType   = reflect.TypeOf(time.Time{})
	scannerTyp = reflect.TypeOf((*sql.Scanner)(nil)).Elem()
)

func convertArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = convertArg(a)
	}
	return out
}

// convertArg adapta los valores Go a tipos que SQL Server entiende:
// JSON ([]byte / json.RawMessage) como texto y listas, mapas o structs serializados a JSON
// (se consultan con OPENJSON).
func convertArg(a any) any {
	switch v := a.(type) {
	case nil:
		return nil
	case driver.Valuer:
		rv := reflect.ValueOf(a)
		if rv.Kind() == reflect.Pointer && rv.IsNil() {
			return nil
		}
		val, err := v.Value()
		if err != nil {
			return a
		}
		return val
	case string, int64, float64, bool, time.Time:
		return a
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int16:
		return int64(v)
	case int8:
		return int64(v)
	case float32:
		return float64(v)
	case json.RawMessage:
		if v == nil {
			return nil
		}
		return string(v)
	case []byte:
		if v == nil {
			return nil
		}
		return string(v)
	}

	rv := reflect.ValueOf(a)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return convertArg(rv.Elem().Interface())
	case reflect.Slice, reflect.Array, reflect.Map:
		if rv.Kind() != reflect.Array && rv.IsNil() {
			return nil
		}
		b, err := json.Marshal(a)
		if err != nil {
			return a
		}
		return string(b)
	case reflect.Struct:
		if rv.Type() == timeType {
			return a
		}
		b, err := json.Marshal(a)
		if err != nil {
			return a
		}
		return string(b)
	case reflect.String:
		return rv.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint())
	case reflect.Bool:
		return rv.Bool()
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	}
	return a
}

// ---------------------------------------------------------------------------
//  Lectura de columnas
// ---------------------------------------------------------------------------

// adaptDest envuelve los destinos que database/sql no sabe llenar por sí solo:
// columnas JSON (NVARCHAR) hacia json.RawMessage, mapas, slices o structs.
func adaptDest(dest []any) []any {
	out := make([]any, len(dest))
	for i, d := range dest {
		out[i] = adaptOne(d)
	}
	return out
}

func adaptOne(d any) any {
	if d == nil {
		return d
	}
	if _, ok := d.(sql.Scanner); ok {
		return d
	}
	rv := reflect.ValueOf(d)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return d
	}
	if needsJSON(rv.Type().Elem()) {
		return &jsonScanner{target: rv}
	}
	return d
}

// needsJSON indica si el tipo se debe llenar decodificando JSON.
func needsJSON(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		return needsJSON(t.Elem())
	}
	if reflect.PointerTo(t).Implements(scannerTyp) || t == timeType {
		return false
	}
	switch t.Kind() {
	case reflect.Map, reflect.Struct:
		return true
	case reflect.Slice:
		return true // incluye json.RawMessage y []byte (texto JSON)
	}
	return false
}

// jsonScanner llena el destino a partir de texto JSON; NULL deja el valor en cero.
type jsonScanner struct{ target reflect.Value }

func (j *jsonScanner) Scan(src any) error {
	elem := j.target.Elem()
	var raw []byte
	switch v := src.(type) {
	case nil:
		elem.SetZero()
		return nil
	case string:
		raw = []byte(v)
	case []byte:
		raw = append([]byte(nil), v...)
	default:
		return fmt.Errorf("no se puede leer %T como JSON", src)
	}

	t := elem.Type()
	if t.Kind() == reflect.Pointer {
		ptr := reflect.New(t.Elem())
		if err := (&jsonScanner{target: ptr}).Scan(raw); err != nil {
			return err
		}
		elem.Set(ptr)
		return nil
	}
	// json.RawMessage / []byte: se copia el texto tal cual
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		elem.SetBytes(raw)
		return nil
	}
	return json.Unmarshal(raw, j.target.Interface())
}

// discard descarta columnas que no tienen campo en el struct destino.
type discard struct{}

func (discard) Scan(any) error { return nil }
