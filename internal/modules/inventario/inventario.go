// Package inventario contiene operaciones transaccionales de bajo nivel sobre
// activos, compartidas por los módulos de movimientos, mantenimientos, averías,
// reemplazos y bajas. No expone endpoints.
package inventario

import (
	"context"
	"errors"
	"fmt"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/security"
)

const (
	EstadoActivo          = "ACTIVO"
	EstadoEnMantenimiento = "EN_MANTENIMIENTO"
	EstadoDanado          = "DANADO"
	EstadoBaja            = "DADO_DE_BAJA"
)

var EstadoNombre = map[string]string{
	EstadoActivo:          "Activo",
	EstadoEnMantenimiento: "En mantenimiento",
	EstadoDanado:          "Dañado",
	EstadoBaja:            "Dado de baja",
}

// Activo es la información mínima necesaria para validar operaciones.
type Activo struct {
	ID             int64
	Codigo         string
	Estado         string
	Condicion      string
	CategoriaTipo  string
	Categoria      string
	Area           string
	PadreID        *int64
	AgenciaID      int64
	DepartamentoID *int64
	EmpleadoID     *int64
}

func (a Activo) EsComponente() bool { return a.CategoriaTipo == "COMPONENTE" }

// Etiqueta devuelve "LAP-000001 (Laptop)" para mensajes.
func (a Activo) Etiqueta() string { return fmt.Sprintf("%s (%s)", a.Codigo, a.Categoria) }

// Lock obtiene el activo bloqueando la fila (WITH (UPDLOCK, ROWLOCK)) dentro de la tx.
func Lock(ctx context.Context, tx database.Tx, id int64) (Activo, error) {
	var a Activo
	err := tx.QueryRow(ctx, `
		SELECT a.id, a.codigo, a.estado, a.condicion, c.tipo, c.nombre, c.area, a.activo_padre_id, a.agencia_id,
		       a.departamento_id, a.empleado_id
		FROM activos a WITH (UPDLOCK, ROWLOCK) JOIN categorias c ON c.id = a.categoria_id
		WHERE a.id = $1`, id).
		Scan(&a.ID, &a.Codigo, &a.Estado, &a.Condicion, &a.CategoriaTipo, &a.Categoria, &a.Area, &a.PadreID, &a.AgenciaID,
			&a.DepartamentoID, &a.EmpleadoID)
	return a, apperr.FromDB(err, fmt.Sprintf("El activo %d no existe", id))
}

// LockOperable bloquea el activo y valida que no esté dado de baja, que no esté en un envío
// pendiente de recepción, que sea del área del usuario y, para un encargado, que pertenezca a su agencia.
func LockOperable(ctx context.Context, tx database.Tx, id int64) (Activo, error) {
	a, err := Lock(ctx, tx, id)
	if err != nil {
		return a, err
	}
	if a.Estado == EstadoBaja {
		return a, apperr.Conflict("El activo " + a.Codigo + " está dado de baja")
	}
	if area := security.AreaScope(ctx); area != nil && a.Area != *area {
		return a, apperr.Forbidden("El activo " + a.Codigo + " pertenece a otra área (" + NombreArea(a.Area) + ")")
	}
	if codigo, err := EnvioPendiente(ctx, tx, a.ID); err != nil {
		return a, err
	} else if codigo != "" {
		return a, apperr.Conflict("El activo " + a.Codigo + " está en tránsito (envío " + codigo + "); espere a que se reciba o rechace")
	}
	return a, VerificarAgencia(ctx, a)
}

// NombreArea traduce el código de área para los mensajes.
func NombreArea(area string) string {
	if area == security.AreaProveeduria {
		return "Proveeduría"
	}
	return "Seguridad física"
}

// AreaSQL es la condición SQL que limita una columna categoria_id al área del usuario ("" = sin restricción).
// Uso: if cond := inventario.AreaSQL(ctx, "v.categoria_id"); cond != "" { w.Add(cond, *security.AreaScope(ctx)) }
func AreaSQL(ctx context.Context, columna string) string {
	if security.AreaScope(ctx) == nil {
		return ""
	}
	return columna + " IN (SELECT id FROM categorias WHERE area = ?)"
}

// VerificarArea impide registrar o recategorizar un activo con una categoría de otra área.
func VerificarArea(ctx context.Context, q database.Querier, categoriaID int64) error {
	area := security.AreaScope(ctx)
	if area == nil {
		return nil
	}
	var catArea string
	if err := q.QueryRow(ctx, `SELECT area FROM categorias WHERE id = $1`, categoriaID).Scan(&catArea); err != nil {
		return apperr.FromDB(err, "Categoría no encontrada")
	}
	if catArea != *area {
		return apperr.Validation(map[string]string{"categoriaId": "La categoría pertenece a " + NombreArea(catArea)})
	}
	return nil
}

// EnvioPendiente devuelve el código del envío EN_TRANSITO que contiene al activo (o a su equipo padre), o "".
func EnvioPendiente(ctx context.Context, q database.Querier, activoID int64) (string, error) {
	var codigo string
	err := q.QueryRow(ctx, `
		SELECT TOP 1 e.codigo FROM traslados t JOIN envios e ON e.id = t.envio_id
		WHERE e.estado = 'EN_TRANSITO'
		  AND t.activo_id IN ($1, (SELECT activo_padre_id FROM activos WHERE id = $1))`, activoID).Scan(&codigo)
	if errors.Is(err, database.ErrNoRows) {
		return "", nil
	}
	return codigo, err
}

// VerificarAgencia impide que un encargado opere activos de otra agencia, o un jefe los de otro departamento.
func VerificarAgencia(ctx context.Context, a Activo) error {
	if ag := security.AgenciaScope(ctx); ag != nil && a.AgenciaID != *ag {
		return apperr.Forbidden("El activo " + a.Codigo + " no pertenece a su agencia")
	}
	if dep := security.DepartamentoScope(ctx); dep != nil && (a.DepartamentoID == nil || *a.DepartamentoID != *dep) {
		return apperr.Forbidden("El activo " + a.Codigo + " no pertenece a su departamento")
	}
	return nil
}

// DepartamentoSQL es la condición que limita al jefe a su departamento ("" = sin restricción).
// Uso: if cond := inventario.DepartamentoSQL(ctx, "v.departamento_id"); cond != "" { w.Add(cond, *security.DepartamentoScope(ctx)) }
func DepartamentoSQL(ctx context.Context, columna string) string {
	if security.DepartamentoScope(ctx) == nil {
		return ""
	}
	return columna + " = ?"
}

// CambiarEstado actualiza el estado del activo y lo registra en el historial.
func CambiarEstado(ctx context.Context, q database.Querier, a Activo, nuevo, motivo, ref string, refID *int64) error {
	if a.Estado == nuevo {
		return nil
	}
	if _, err := q.Exec(ctx, `UPDATE activos SET estado = $2, updated_by = $3 WHERE id = $1`,
		a.ID, nuevo, security.UserID(ctx)); err != nil {
		return err
	}
	desc := fmt.Sprintf("Estado: %s → %s", EstadoNombre[a.Estado], EstadoNombre[nuevo])
	if motivo != "" {
		desc += ". " + motivo
	}
	return audit.History(ctx, q, audit.Event{ActivoID: a.ID, Tipo: audit.EvCambioEstado, Descripcion: desc,
		Datos: map[string]string{"anterior": a.Estado, "nuevo": nuevo}, Referencia: ref, ReferenciaID: refID})
}

// Ubicacion es la ubicación/responsable que se aplica a un activo.
type Ubicacion struct {
	AgenciaID      int64
	DepartamentoID *int64
	EmpleadoID     *int64
}

// Mover actualiza la ubicación del activo y de todos sus componentes instalados.
func Mover(ctx context.Context, q database.Querier, activoID int64, u Ubicacion) error {
	_, err := q.Exec(ctx, `
		UPDATE activos SET agencia_id = $2, departamento_id = $3, empleado_id = $4, updated_by = $5
		WHERE id = $1 OR activo_padre_id = $1`,
		activoID, u.AgenciaID, u.DepartamentoID, u.EmpleadoID, security.UserID(ctx))
	return err
}

// Componentes devuelve los ids de los componentes instalados en el equipo.
func Componentes(ctx context.Context, q database.Querier, activoID int64) ([]int64, error) {
	rows, err := q.Query(ctx, `SELECT id FROM activos WHERE activo_padre_id = $1 ORDER BY id`, activoID)
	if err != nil {
		return nil, err
	}
	return database.Column[int64](rows)
}
