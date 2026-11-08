// Package organizacion gestiona agencias (matriz, sucursales, agencias, bodegas) y departamentos.
package organizacion

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/crud"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/middleware"
)

// ---- Modelos ----

type Agencia struct {
	ID         int64     `json:"id" db:"id"`
	Codigo     string    `json:"codigo" db:"codigo"`
	Nombre     string    `json:"nombre" db:"nombre"`
	Tipo       string    `json:"tipo" db:"tipo"`
	Provincia  *string   `json:"provincia" db:"provincia"`
	Ciudad     *string   `json:"ciudad" db:"ciudad"`
	Direccion  *string   `json:"direccion" db:"direccion"`
	Telefono   *string   `json:"telefono" db:"telefono"`
	Email      *string   `json:"email" db:"email"`
	Habilitado bool      `json:"habilitado" db:"habilitado"`
	Activos    int64     `json:"totalActivos" db:"total_activos"`
	Empleados  int64     `json:"totalEmpleados" db:"total_empleados"`
	CreatedAt  time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time `json:"updatedAt" db:"updated_at"`
}

// AgenciaInput no incluye el código: se genera al crear (ver siguienteCodigoAgencia) y no cambia al editar.
type AgenciaInput struct {
	Nombre     string  `json:"nombre" validate:"required,max=120"`
	Tipo       string  `json:"tipo" validate:"required,oneof=MATRIZ SUCURSAL AGENCIA VENTANILLA BODEGA"`
	Provincia  *string `json:"provincia" validate:"omitempty,max=80"`
	Ciudad     *string `json:"ciudad" validate:"omitempty,max=80"`
	Direccion  *string `json:"direccion" validate:"omitempty,max=250"`
	Telefono   *string `json:"telefono" validate:"omitempty,max=30"`
	Email      *string `json:"email" validate:"omitempty,email,max=150"`
	Habilitado *bool   `json:"habilitado"`
}

type Departamento struct {
	ID          int64     `json:"id" db:"id"`
	Codigo      string    `json:"codigo" db:"codigo"`
	Nombre      string    `json:"nombre" db:"nombre"`
	Descripcion *string   `json:"descripcion" db:"descripcion"`
	Habilitado  bool      `json:"habilitado" db:"habilitado"`
	Empleados   int64     `json:"totalEmpleados" db:"total_empleados"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
}

type DepartamentoInput struct {
	Codigo      string  `json:"codigo" validate:"required,max=20"`
	Nombre      string  `json:"nombre" validate:"required,max=120"`
	Descripcion *string `json:"descripcion" validate:"omitempty,max=250"`
	Habilitado  *bool   `json:"habilitado"`
}

// ---- Handler ----

type Handler struct {
	db            *database.DB
	agencias      crud.Resource[Agencia]
	departamentos crud.Resource[Departamento]
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{
		db: db,
		agencias: crud.Resource[Agencia]{Table: "agencias", NotFound: "Agencia no encontrada", Find: findAgencia,
			Label: func(a Agencia) string { return "Agencia " + a.Nombre }},
		departamentos: crud.Resource[Departamento]{Table: "departamentos", NotFound: "Departamento no encontrado",
			Find: findDepartamento, Label: func(d Departamento) string { return "Departamento " + d.Nombre }},
	}
}

func (h *Handler) AgenciaRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listAgencias))
	r.Get("/{id}", httpx.Handle(h.getAgencia))
	r.With(middleware.SuperAdminOnly).Post("/", httpx.HandleCreated(h.createAgencia))
	r.With(middleware.SuperAdminOnly).Put("/{id}", httpx.Handle(h.updateAgencia))
	r.With(middleware.SuperAdminOnly).Delete("/{id}", httpx.Handle(h.deleteAgencia))
}

func (h *Handler) DepartamentoRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listDepartamentos))
	r.Get("/{id}", httpx.Handle(h.getDepartamento))
	r.With(middleware.SuperAdminOnly).Post("/", httpx.HandleCreated(h.createDepartamento))
	r.With(middleware.SuperAdminOnly).Put("/{id}", httpx.Handle(h.updateDepartamento))
	r.With(middleware.SuperAdminOnly).Delete("/{id}", httpx.Handle(h.deleteDepartamento))
}

// ---- Agencias ----

const agenciaSelect = `SELECT a.*,
	(SELECT count(*) FROM activos x WHERE x.agencia_id = a.id AND x.estado <> 'DADO_DE_BAJA') AS total_activos,
	(SELECT count(*) FROM empleados e WHERE e.agencia_id = a.id AND e.habilitado = 1) AS total_empleados`

var agenciaSort = map[string]string{"codigo": "a.codigo", "nombre": "a.nombre", "tipo": "a.tipo", "ciudad": "a.ciudad"}

func (h *Handler) listAgencias(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("a.codigo + ' ' + a.nombre + ' ' + coalesce(a.ciudad, '')", lq.Search)
	database.Eq(w, "a.tipo", httpx.QueryString(r, "tipo"))
	database.Eq(w, "a.habilitado", httpx.QueryBool(r, "habilitado"))
	return database.Paginate[Agencia](r.Context(), h.db, agenciaSelect, "FROM agencias a", w,
		lq.OrderBy(agenciaSort, "a.tipo, a.nombre"), lq)
}

func findAgencia(ctx context.Context, q database.Querier, id int64) (Agencia, error) {
	a, err := database.Get[Agencia](ctx, q, agenciaSelect+" FROM agencias a WHERE a.id = $1", id)
	return a, apperr.FromDB(err, "Agencia no encontrada")
}

func (h *Handler) getAgencia(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return findAgencia(r.Context(), h.db, id)
}

func (h *Handler) createAgencia(r *http.Request) (any, error) {
	var in AgenciaInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	return h.agencias.Create(r.Context(), h.db, func(tx database.Tx) (id int64, err error) {
		codigo, err := siguienteCodigoAgencia(r.Context(), tx, in.Tipo)
		if err != nil {
			return 0, err
		}
		err = tx.QueryRow(r.Context(), `INSERT INTO agencias (codigo, nombre, tipo, provincia, ciudad, direccion, telefono, email, habilitado)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, coalesce($9, 1)); SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			codigo, in.Nombre, in.Tipo, in.Provincia, in.Ciudad, in.Direccion, in.Telefono, in.Email, in.Habilitado).Scan(&id)
		return
	})
}

func (h *Handler) updateAgencia(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in AgenciaInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	return h.agencias.Update(r.Context(), h.db, id, func(tx database.Tx, _ Agencia) error {
		_, err := tx.Exec(r.Context(), `UPDATE agencias SET nombre = $2, tipo = $3, provincia = $4, ciudad = $5,
			direccion = $6, telefono = $7, email = $8, habilitado = coalesce($9, habilitado) WHERE id = $1`,
			id, in.Nombre, in.Tipo, in.Provincia, in.Ciudad, in.Direccion, in.Telefono, in.Email, in.Habilitado)
		return err
	})
}

// prefijosAgencia define la inicial del código según el tipo: AG01, SUC02, VEN01, BOD01, MAT01...
var prefijosAgencia = map[string]string{
	"MATRIZ":     "MAT",
	"SUCURSAL":   "SUC",
	"AGENCIA":    "AG",
	"VENTANILLA": "VEN",
	"BODEGA":     "BOD",
}

// siguienteCodigoAgencia devuelve el prefijo del tipo con el siguiente número libre (mínimo 2 dígitos).
// Toma el mayor número ya usado con ese prefijo, así respeta los códigos existentes (AG01, SUC1...).
func siguienteCodigoAgencia(ctx context.Context, tx database.Tx, tipo string) (string, error) {
	prefijo := prefijosAgencia[tipo]
	// Serializa la generación para que dos altas simultáneas no obtengan el mismo número
	if _, err := tx.Exec(ctx, `EXEC sp_getapplock @Resource = 'agencias.codigo', @LockMode = 'Exclusive', @LockOwner = 'Transaction'`); err != nil {
		return "", err
	}
	var siguiente int
	err := tx.QueryRow(ctx, `SELECT coalesce(max(TRY_CAST(SUBSTRING(codigo, LEN($1) + 1, 20) AS INT)), 0) + 1
		FROM agencias
		WHERE codigo LIKE $1 + '[0-9]%' AND SUBSTRING(codigo, LEN($1) + 1, 20) NOT LIKE '%[^0-9]%'`, prefijo).Scan(&siguiente)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%02d", prefijo, siguiente), nil
}

func (h *Handler) deleteAgencia(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	if err := h.agencias.Delete(r.Context(), h.db, id); err != nil {
		return nil, err
	}
	return httpx.MessageResult("Agencia eliminada"), nil
}

// ---- Departamentos ----

const departamentoSelect = `SELECT d.*,
	(SELECT count(*) FROM empleados e WHERE e.departamento_id = d.id AND e.habilitado = 1) AS total_empleados`

var departamentoSort = map[string]string{"codigo": "d.codigo", "nombre": "d.nombre"}

func (h *Handler) listDepartamentos(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("d.codigo + ' ' + d.nombre", lq.Search)
	database.Eq(w, "d.habilitado", httpx.QueryBool(r, "habilitado"))
	return database.Paginate[Departamento](r.Context(), h.db, departamentoSelect, "FROM departamentos d", w,
		lq.OrderBy(departamentoSort, "d.nombre"), lq)
}

func findDepartamento(ctx context.Context, q database.Querier, id int64) (Departamento, error) {
	d, err := database.Get[Departamento](ctx, q, departamentoSelect+" FROM departamentos d WHERE d.id = $1", id)
	return d, apperr.FromDB(err, "Departamento no encontrado")
}

func (h *Handler) getDepartamento(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return findDepartamento(r.Context(), h.db, id)
}

func (h *Handler) createDepartamento(r *http.Request) (any, error) {
	var in DepartamentoInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	return h.departamentos.Create(r.Context(), h.db, func(tx database.Tx) (id int64, err error) {
		err = tx.QueryRow(r.Context(), `INSERT INTO departamentos (codigo, nombre, descripcion, habilitado)
			VALUES (upper($1), $2, $3, coalesce($4, 1)); SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			in.Codigo, in.Nombre, in.Descripcion, in.Habilitado).Scan(&id)
		return
	})
}

func (h *Handler) updateDepartamento(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in DepartamentoInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	return h.departamentos.Update(r.Context(), h.db, id, func(tx database.Tx, _ Departamento) error {
		_, err := tx.Exec(r.Context(), `UPDATE departamentos SET codigo = upper($2), nombre = $3, descripcion = $4,
			habilitado = coalesce($5, habilitado) WHERE id = $1`, id, in.Codigo, in.Nombre, in.Descripcion, in.Habilitado)
		return err
	})
}

func (h *Handler) deleteDepartamento(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	if err := h.departamentos.Delete(r.Context(), h.db, id); err != nil {
		return nil, err
	}
	return httpx.MessageResult("Departamento eliminado"), nil
}
