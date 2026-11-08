// Package empleados gestiona al personal responsable de los activos.
package empleados

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/crud"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/middleware"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

type Empleado struct {
	ID             int64       `json:"id" db:"id"`
	Cedula         string      `json:"cedula" db:"cedula"`
	Nombres        string      `json:"nombres" db:"nombres"`
	Apellidos      string      `json:"apellidos" db:"apellidos"`
	NombreCompleto string      `json:"nombreCompleto" db:"nombre_completo"`
	Email          *string     `json:"email" db:"email"`
	Telefono       *string     `json:"telefono" db:"telefono"`
	Cargo          *string     `json:"cargo" db:"cargo"`
	AgenciaID      int64       `json:"agenciaId" db:"agencia_id"`
	Agencia        string      `json:"agencia" db:"agencia"`
	DepartamentoID int64       `json:"departamentoId" db:"departamento_id"`
	Departamento   string      `json:"departamento" db:"departamento"`
	FechaIngreso   *types.Date `json:"fechaIngreso" db:"fecha_ingreso"`
	Habilitado     bool        `json:"habilitado" db:"habilitado"`
	// Jefe de su departamento en su agencia: responsable de los bienes del área (muebles, equipos compartidos)
	EsJefe bool `json:"esJefe" db:"es_jefe"`
	// Salida de la institución; si la registró un encargado queda pendiente hasta que un admin la confirma
	FechaSalida     *types.Date `json:"fechaSalida" db:"fecha_salida"`
	MotivoSalida    *string     `json:"motivoSalida" db:"motivo_salida"`
	SalidaPendiente bool        `json:"salidaPendiente" db:"salida_pendiente"`
	TotalActivos   int64       `json:"totalActivos" db:"total_activos"`
	CreatedAt      time.Time   `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time   `json:"updatedAt" db:"updated_at"`
}

type Input struct {
	Cedula         string      `json:"cedula" validate:"required,cedula"`
	Nombres        string      `json:"nombres" validate:"required,max=100"`
	Apellidos      string      `json:"apellidos" validate:"required,max=100"`
	Email          *string     `json:"email" validate:"omitempty,email,max=150"`
	Telefono       *string     `json:"telefono" validate:"omitempty,max=30"`
	Cargo          *string     `json:"cargo" validate:"omitempty,max=120"`
	AgenciaID      int64       `json:"agenciaId" validate:"required,gt=0"`
	DepartamentoID int64       `json:"departamentoId" validate:"required,gt=0"`
	FechaIngreso   *types.Date `json:"fechaIngreso"`
	Habilitado     *bool       `json:"habilitado"`
	EsJefe         bool        `json:"esJefe"`
}

// ActivoAsignado es el resumen de un activo a cargo del empleado.
type ActivoAsignado struct {
	ID              int64       `json:"id" db:"id"`
	Codigo          string      `json:"codigo" db:"codigo"`
	Categoria       string      `json:"categoria" db:"categoria"`
	CategoriaIcono  *string     `json:"categoriaIcono" db:"categoria_icono"`
	Marca           *string     `json:"marca" db:"marca"`
	Modelo          *string     `json:"modelo" db:"modelo"`
	NumeroSerie     *string     `json:"numeroSerie" db:"numero_serie"`
	Estado          string      `json:"estado" db:"estado"`
	Condicion       string      `json:"condicion" db:"condicion"`
	ActivoPadreID   *int64      `json:"activoPadreId" db:"activo_padre_id"`
	FechaAsignacion *types.Date `json:"fechaAsignacion" db:"fecha_asignacion"`
}

type Handler struct {
	db  *database.DB
	res crud.Resource[Empleado]
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{db: db, res: crud.Resource[Empleado]{Table: "empleados", NotFound: "Empleado no encontrado", Find: Find,
		Label: func(e Empleado) string { return "Empleado " + e.NombreCompleto }}}
}

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.HandleCreated(h.create))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Get("/{id}/activos", httpx.Handle(h.activos))
	r.Get("/{id}/devueltos", httpx.Handle(h.devueltos))
	r.Put("/{id}", httpx.Handle(h.update))
	r.With(middleware.RequireRoles(security.RoleSuperAdmin, security.RoleAdmin)).Post("/{id}/salida/confirmar", httpx.Handle(h.confirmarSalida))
	r.With(middleware.RequireRoles(security.RoleSuperAdmin, security.RoleAdmin)).Delete("/{id}/salida", httpx.Handle(h.anularSalida))
	r.With(middleware.SuperAdminOnly).Delete("/{id}", httpx.Handle(h.delete))
}

const selectSQL = `SELECT e.*, e.nombres + ' ' + e.apellidos AS nombre_completo, a.nombre AS agencia, d.nombre AS departamento,
	(SELECT count(*) FROM activos x WHERE x.empleado_id = e.id AND x.estado <> 'DADO_DE_BAJA') AS total_activos`

const fromSQL = `FROM empleados e JOIN agencias a ON a.id = e.agencia_id JOIN departamentos d ON d.id = e.departamento_id`

var sortable = map[string]string{
	"cedula": "e.cedula", "nombreCompleto": "e.apellidos", "apellidos": "e.apellidos", "cargo": "e.cargo",
	"agencia": "a.nombre", "departamento": "d.nombre", "fechaIngreso": "e.fecha_ingreso",
}

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("e.nombres + ' ' + e.apellidos + ' ' + e.cedula", lq.Search)
	database.Eq(w, "e.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	database.Eq(w, "e.departamento_id", httpx.QueryInt64(r, "departamentoId"))
	database.Eq(w, "e.habilitado", httpx.QueryBool(r, "habilitado"))
	database.Eq(w, "e.salida_pendiente", httpx.QueryBool(r, "salidaPendiente"))
	database.Eq(w, "e.es_jefe", httpx.QueryBool(r, "esJefe"))
	if v := httpx.QueryBool(r, "exempleados"); v != nil && *v {
		w.Add("e.fecha_salida IS NOT NULL")
	}
	if ag := security.AgenciaScope(r.Context()); ag != nil {
		w.Add("e.agencia_id = ?", *ag)
	}
	if dep := security.DepartamentoScope(r.Context()); dep != nil {
		w.Add("e.departamento_id = ?", *dep)
	}
	return database.Paginate[Empleado](r.Context(), h.db, selectSQL, fromSQL, w,
		lq.OrderBy(sortable, "e.apellidos, e.nombres"), lq)
}

// Find es usado también por otros módulos (asignaciones, actas).
func Find(ctx context.Context, q database.Querier, id int64) (Empleado, error) {
	e, err := database.Get[Empleado](ctx, q, selectSQL+" "+fromSQL+" WHERE e.id = $1", id)
	return e, apperr.FromDB(err, "Empleado no encontrado")
}

// findVisible busca el empleado; el encargado solo ve los de su agencia y el jefe los de su departamento.
func findVisible(ctx context.Context, q database.Querier, id int64) (Empleado, error) {
	e, err := Find(ctx, q, id)
	if err != nil {
		return e, err
	}
	if ag := security.AgenciaScope(ctx); ag != nil && e.AgenciaID != *ag {
		return e, apperr.NotFound("Empleado no encontrado")
	}
	if dep := security.DepartamentoScope(ctx); dep != nil && e.DepartamentoID != *dep {
		return e, apperr.NotFound("Empleado no encontrado")
	}
	return e, nil
}

func (h *Handler) get(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return findVisible(r.Context(), h.db, id)
}

func (h *Handler) activos(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	if _, err := findVisible(r.Context(), h.db, id); err != nil {
		return nil, err
	}
	return database.List[ActivoAsignado](r.Context(), h.db, `
		SELECT a.id, a.codigo, c.nombre AS categoria, c.icono AS categoria_icono, m.nombre AS marca, a.modelo,
		       a.numero_serie, a.estado, a.condicion, a.activo_padre_id, s.fecha_asignacion
		FROM activos a
		JOIN categorias c ON c.id = a.categoria_id
		LEFT JOIN marcas m ON m.id = a.marca_id
		LEFT JOIN asignaciones s ON s.activo_id = a.id AND s.estado = 'VIGENTE'
		WHERE a.empleado_id = $1 AND a.estado <> 'DADO_DE_BAJA'
		ORDER BY a.activo_padre_id, c.tipo, a.codigo`, id)
}

// devueltos lista los equipos que el empleado devolvió y que siguen libres (sin responsable ni envío en curso),
// para entregárselos a quien ocupe su puesto. FechaAsignacion trae la fecha de devolución.
func (h *Handler) devueltos(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	if _, err := findVisible(r.Context(), h.db, id); err != nil {
		return nil, err
	}
	return database.List[ActivoAsignado](r.Context(), h.db, `
		SELECT a.id, a.codigo, c.nombre AS categoria, c.icono AS categoria_icono, m.nombre AS marca, a.modelo,
		       a.numero_serie, a.estado, a.condicion, a.activo_padre_id, s.fecha_devolucion AS fecha_asignacion
		FROM activos a
		JOIN categorias c ON c.id = a.categoria_id
		LEFT JOIN marcas m ON m.id = a.marca_id
		CROSS APPLY (SELECT TOP 1 x.empleado_id, x.fecha_devolucion FROM asignaciones x WHERE x.activo_id = a.id
		             ORDER BY x.fecha_asignacion DESC, x.id DESC) s
		WHERE s.empleado_id = $1 AND a.empleado_id IS NULL AND a.activo_padre_id IS NULL AND a.estado <> 'DADO_DE_BAJA'
		  AND (CAST($2 AS BIGINT) IS NULL OR a.agencia_id = $2)
		  AND (CAST($3 AS BIGINT) IS NULL OR a.departamento_id = $3)
		  AND NOT EXISTS (SELECT 1 FROM traslados t JOIN envios e ON e.id = t.envio_id
		                  WHERE e.estado = 'EN_TRANSITO' AND t.activo_id = a.id)
		ORDER BY c.tipo, a.codigo`, id, security.AgenciaScope(r.Context()), security.DepartamentoScope(r.Context()))
}

// confirmarSalida: un admin confirma la salida registrada por el encargado y deshabilita al empleado.
func (h *Handler) confirmarSalida(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return h.res.Update(r.Context(), h.db, id, func(tx database.Tx, before Empleado) error {
		if !before.SalidaPendiente {
			return apperr.Conflict("El empleado no tiene una salida pendiente de confirmar")
		}
		if before.TotalActivos > 0 {
			return apperr.Conflict("El empleado aún tiene activos a su cargo; regístrelos como devueltos antes de confirmar")
		}
		_, err := tx.Exec(r.Context(), `UPDATE empleados SET habilitado = 0, salida_pendiente = 0 WHERE id = $1`, id)
		return err
	})
}

// anularSalida deja sin efecto una salida pendiente (registrada por error); los equipos siguen devueltos.
func (h *Handler) anularSalida(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return h.res.Update(r.Context(), h.db, id, func(tx database.Tx, before Empleado) error {
		if !before.SalidaPendiente {
			return apperr.Conflict("El empleado no tiene una salida pendiente")
		}
		_, err := tx.Exec(r.Context(), `UPDATE empleados SET salida_pendiente = 0, fecha_salida = NULL, motivo_salida = NULL
			WHERE id = $1`, id)
		return err
	})
}

func (h *Handler) create(r *http.Request) (any, error) {
	var in Input
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	return h.res.Create(r.Context(), h.db, func(tx database.Tx) (id int64, err error) {
		err = tx.QueryRow(r.Context(), `INSERT INTO empleados (cedula, nombres, apellidos, email, telefono, cargo, agencia_id,
			departamento_id, fecha_ingreso, habilitado, es_jefe) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, coalesce($10, 1), $11); SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			in.Cedula, in.Nombres, in.Apellidos, in.Email, in.Telefono, in.Cargo, in.AgenciaID, in.DepartamentoID,
			in.FechaIngreso, in.Habilitado, in.EsJefe).Scan(&id)
		return
	})
}

func (h *Handler) update(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in Input
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	return h.res.Update(r.Context(), h.db, id, func(tx database.Tx, before Empleado) error {
		if in.Habilitado != nil && !*in.Habilitado && before.TotalActivos > 0 {
			return apperr.Conflict("El empleado tiene activos a su cargo. Devuélvalos o reasígnelos antes de deshabilitarlo")
		}
		_, err := tx.Exec(r.Context(), `UPDATE empleados SET cedula = $2, nombres = $3, apellidos = $4, email = $5, telefono = $6,
			cargo = $7, agencia_id = $8, departamento_id = $9, fecha_ingreso = $10, habilitado = coalesce($11, habilitado),
			-- al volver a habilitarlo (reingreso) deja de figurar como exempleado
			fecha_salida = CASE WHEN coalesce($11, habilitado) = 1 AND habilitado = 0 THEN NULL ELSE fecha_salida END,
			motivo_salida = CASE WHEN coalesce($11, habilitado) = 1 AND habilitado = 0 THEN NULL ELSE motivo_salida END,
			es_jefe = $12
			WHERE id = $1`, id, in.Cedula, in.Nombres, in.Apellidos, in.Email, in.Telefono, in.Cargo, in.AgenciaID,
			in.DepartamentoID, in.FechaIngreso, in.Habilitado, in.EsJefe)
		return err
	})
}

func (h *Handler) delete(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	if err := h.res.Delete(r.Context(), h.db, id); err != nil {
		return nil, err
	}
	return httpx.MessageResult("Empleado eliminado"), nil
}
