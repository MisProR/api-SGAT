// Package crud encapsula el flujo transaccional estándar de alta, edición y
// eliminación con auditoría, reutilizado por los módulos de catálogos.
package crud

import (
	"context"
	"strings"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
)

// Finder obtiene la entidad por id (debe devolver un apperr NotFound si no existe).
type Finder[T any] func(ctx context.Context, q database.Querier, id int64) (T, error)

// Resource describe una entidad para el flujo CRUD auditado.
type Resource[T any] struct {
	Table    string         // tabla (y nombre de entidad en auditoría)
	NotFound string         // mensaje cuando no existe
	Find     Finder[T]      // lectura por id
	Label    func(T) string // descripción legible para la auditoría
}

// Create ejecuta insert (que devuelve el id), relee la entidad y audita.
func (res Resource[T]) Create(ctx context.Context, db *database.DB, insert func(tx database.Tx) (int64, error)) (T, error) {
	var out T
	err := database.WithTx(ctx, db, func(tx database.Tx) error {
		id, err := insert(tx)
		if err != nil {
			return err
		}
		if out, err = res.Find(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Crear, Entidad: res.Table, EntidadID: &id,
			Descripcion: res.Label(out), Despues: out})
	})
	return out, apperr.FromDB(err, res.NotFound)
}

// Update valida existencia, ejecuta update (con el estado previo disponible), relee y audita.
func (res Resource[T]) Update(ctx context.Context, db *database.DB, id int64, update func(tx database.Tx, before T) error) (T, error) {
	var out T
	err := database.WithTx(ctx, db, func(tx database.Tx) error {
		before, err := res.Find(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := update(tx, before); err != nil {
			return err
		}
		if out, err = res.Find(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: res.Table, EntidadID: &id,
			Descripcion: res.Label(out), Antes: before, Despues: out})
	})
	return out, apperr.FromDB(err, res.NotFound)
}

// Delete elimina físicamente el registro (falla con Conflict si tiene dependencias).
func (res Resource[T]) Delete(ctx context.Context, db *database.DB, id int64) error {
	err := database.WithTx(ctx, db, func(tx database.Tx) error {
		before, err := res.Find(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM "+quoteIdent(res.Table)+" WHERE id = $1", id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Eliminar, Entidad: res.Table, EntidadID: &id,
			Descripcion: res.Label(before), Antes: before})
	})
	return apperr.FromDB(err, res.NotFound)
}

// quoteIdent delimita el nombre de la tabla con corchetes (SQL Server).
func quoteIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}
