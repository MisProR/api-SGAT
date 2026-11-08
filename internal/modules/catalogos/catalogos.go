// Package catalogos gestiona categorías de activo, marcas, proveedores y técnicos,
// además del endpoint de opciones usado por los formularios.
package catalogos

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
)

// ---- Modelos ----

type Categoria struct {
	ID                 int64     `json:"id" db:"id"`
	Codigo             string    `json:"codigo" db:"codigo"`
	Nombre             string    `json:"nombre" db:"nombre"`
	Tipo               string    `json:"tipo" db:"tipo"`
	// Área que administra la categoría: SEGURIDAD_FISICA (admin) o PROVEEDURIA
	Area               string    `json:"area" db:"area"`
	Prefijo            string    `json:"prefijo" db:"prefijo"`
	Icono              *string   `json:"icono" db:"icono"`
	VidaUtilMeses      int       `json:"vidaUtilMeses" db:"vida_util_meses"`
	MesesMantenimiento int       `json:"mesesMantenimiento" db:"meses_mantenimiento"`
	RequiereSerie      bool      `json:"requiereSerie" db:"requiere_serie"`
	Habilitado         bool      `json:"habilitado" db:"habilitado"`
	TotalActivos       int64     `json:"totalActivos" db:"total_activos"`
	CreatedAt          time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt          time.Time `json:"updatedAt" db:"updated_at"`
}

type CategoriaInput struct {
	Codigo             string  `json:"codigo" validate:"required,max=20"`
	Nombre             string  `json:"nombre" validate:"required,max=80"`
	Tipo               string  `json:"tipo" validate:"required,oneof=EQUIPO COMPONENTE PERIFERICO MOBILIARIO"`
	Area               string  `json:"area" validate:"omitempty,oneof=SEGURIDAD_FISICA PROVEEDURIA"`
	Prefijo            string  `json:"prefijo" validate:"required,alphanum,max=5"`
	Icono              *string `json:"icono" validate:"omitempty,max=40"`
	VidaUtilMeses      int     `json:"vidaUtilMeses" validate:"required,gt=0,max=600"`
	MesesMantenimiento int     `json:"mesesMantenimiento" validate:"gte=0,max=120"`
	RequiereSerie      bool    `json:"requiereSerie"`
	Habilitado         *bool   `json:"habilitado"`
}

type Marca struct {
	ID           int64     `json:"id" db:"id"`
	Nombre       string    `json:"nombre" db:"nombre"`
	Habilitado   bool      `json:"habilitado" db:"habilitado"`
	TotalActivos int64     `json:"totalActivos" db:"total_activos"`
	CreatedAt    time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time `json:"updatedAt" db:"updated_at"`
}

type MarcaInput struct {
	Nombre     string `json:"nombre" validate:"required,max=80"`
	Habilitado *bool  `json:"habilitado"`
}

type Proveedor struct {
	ID          int64     `json:"id" db:"id"`
	Ruc         string    `json:"ruc" db:"ruc"`
	RazonSocial string    `json:"razonSocial" db:"razon_social"`
	Contacto    *string   `json:"contacto" db:"contacto"`
	Telefono    *string   `json:"telefono" db:"telefono"`
	Email       *string   `json:"email" db:"email"`
	Direccion   *string   `json:"direccion" db:"direccion"`
	Habilitado  bool      `json:"habilitado" db:"habilitado"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
}

type ProveedorInput struct {
	Ruc         string  `json:"ruc" validate:"required,ruc"`
	RazonSocial string  `json:"razonSocial" validate:"required,max=200"`
	Contacto    *string `json:"contacto" validate:"omitempty,max=120"`
	Telefono    *string `json:"telefono" validate:"omitempty,max=30"`
	Email       *string `json:"email" validate:"omitempty,email,max=150"`
	Direccion   *string `json:"direccion" validate:"omitempty,max=250"`
	Habilitado  *bool   `json:"habilitado"`
}

type Tecnico struct {
	ID             int64     `json:"id" db:"id"`
	Nombres        string    `json:"nombres" db:"nombres"`
	Tipo           string    `json:"tipo" db:"tipo"`
	Empresa        *string   `json:"empresa" db:"empresa"`
	Especialidad   *string   `json:"especialidad" db:"especialidad"`
	Telefono       *string   `json:"telefono" db:"telefono"`
	Email          *string   `json:"email" db:"email"`
	EmpleadoID     *int64    `json:"empleadoId" db:"empleado_id"`
	Habilitado     bool      `json:"habilitado" db:"habilitado"`
	Mantenimientos int64     `json:"totalMantenimientos" db:"total_mantenimientos"`
	CreatedAt      time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time `json:"updatedAt" db:"updated_at"`
}

type TecnicoInput struct {
	Nombres      string  `json:"nombres" validate:"required,max=150"`
	Tipo         string  `json:"tipo" validate:"required,oneof=INTERNO EXTERNO"`
	Empresa      *string `json:"empresa" validate:"required_if=Tipo EXTERNO,omitempty,max=150"`
	Especialidad *string `json:"especialidad" validate:"omitempty,max=120"`
	Telefono     *string `json:"telefono" validate:"omitempty,max=30"`
	Email        *string `json:"email" validate:"omitempty,email,max=150"`
	EmpleadoID   *int64  `json:"empleadoId" validate:"omitempty,gt=0"`
	Habilitado   *bool   `json:"habilitado"`
}

// ---- Handler ----

type Handler struct {
	db          *database.DB
	categorias  crud.Resource[Categoria]
	marcas      crud.Resource[Marca]
	proveedores crud.Resource[Proveedor]
	tecnicos    crud.Resource[Tecnico]
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{
		db: db,
		categorias: crud.Resource[Categoria]{Table: "categorias", NotFound: "Categoría no encontrada", Find: findCategoria,
			Label: func(c Categoria) string { return "Categoría " + c.Nombre }},
		marcas: crud.Resource[Marca]{Table: "marcas", NotFound: "Marca no encontrada", Find: findMarca,
			Label: func(m Marca) string { return "Marca " + m.Nombre }},
		proveedores: crud.Resource[Proveedor]{Table: "proveedores", NotFound: "Proveedor no encontrado", Find: findProveedor,
			Label: func(p Proveedor) string { return "Proveedor " + p.RazonSocial }},
		tecnicos: crud.Resource[Tecnico]{Table: "tecnicos", NotFound: "Técnico no encontrado", Find: findTecnico,
			Label: func(t Tecnico) string { return "Técnico " + t.Nombres }},
	}
}

func (h *Handler) CategoriaRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listCategorias))
	r.Get("/{id}", httpx.Handle(byID(findCategoria, h.db)))
	// El admin también administra las categorías, solo las de su área (seguridad física)
	gestion := middleware.RequireRoles(security.RoleSuperAdmin, security.RoleAdmin)
	r.With(gestion).Post("/", httpx.HandleCreated(h.createCategoria))
	r.With(gestion).Put("/{id}", httpx.Handle(h.updateCategoria))
	r.With(middleware.SuperAdminOnly).Delete("/{id}", httpx.Handle(deleteHandler(h.categorias, h.db, "Categoría eliminada")))
}

func (h *Handler) MarcaRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listMarcas))
	r.Get("/{id}", httpx.Handle(byID(findMarca, h.db)))
	r.Post("/", httpx.HandleCreated(h.createMarca))
	r.Put("/{id}", httpx.Handle(h.updateMarca))
	r.With(middleware.SuperAdminOnly).Delete("/{id}", httpx.Handle(deleteHandler(h.marcas, h.db, "Marca eliminada")))
}

func (h *Handler) ProveedorRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listProveedores))
	r.Get("/{id}", httpx.Handle(byID(findProveedor, h.db)))
	r.Post("/", httpx.HandleCreated(h.createProveedor))
	r.Put("/{id}", httpx.Handle(h.updateProveedor))
	r.With(middleware.SuperAdminOnly).Delete("/{id}", httpx.Handle(deleteHandler(h.proveedores, h.db, "Proveedor eliminado")))
}

func (h *Handler) TecnicoRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listTecnicos))
	r.Get("/{id}", httpx.Handle(byID(findTecnico, h.db)))
	r.Post("/", httpx.HandleCreated(h.createTecnico))
	r.Put("/{id}", httpx.Handle(h.updateTecnico))
	r.With(middleware.SuperAdminOnly).Delete("/{id}", httpx.Handle(deleteHandler(h.tecnicos, h.db, "Técnico eliminado")))
}

// byID y deleteHandler generan los handlers repetitivos de lectura y eliminación.
func byID[T any](find crud.Finder[T], db *database.DB) func(*http.Request) (any, error) {
	return func(r *http.Request) (any, error) {
		id, err := httpx.IDParam(r, "id")
		if err != nil {
			return nil, err
		}
		return find(r.Context(), db, id)
	}
}

func deleteHandler[T any](res crud.Resource[T], db *database.DB, msg string) func(*http.Request) (any, error) {
	return func(r *http.Request) (any, error) {
		id, err := httpx.IDParam(r, "id")
		if err != nil {
			return nil, err
		}
		if err := res.Delete(r.Context(), db, id); err != nil {
			return nil, err
		}
		return httpx.MessageResult(msg), nil
	}
}

func bindWithID(r *http.Request, dst any) (int64, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return 0, err
	}
	return id, httpx.Bind(r, dst)
}

// ---- Categorías ----

const categoriaSelect = `SELECT c.*, (SELECT COUNT_BIG(*) FROM activos a WHERE a.categoria_id = c.id) AS total_activos`

func findCategoria(ctx context.Context, q database.Querier, id int64) (Categoria, error) {
	c, err := database.Get[Categoria](ctx, q, categoriaSelect+" FROM categorias c WHERE c.id = $1", id)
	return c, apperr.FromDB(err, "Categoría no encontrada")
}

func (h *Handler) listCategorias(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("c.codigo + ' ' + c.nombre", lq.Search)
	database.Eq(w, "c.tipo", httpx.QueryString(r, "tipo"))
	database.Eq(w, "c.habilitado", httpx.QueryBool(r, "habilitado"))
	database.Eq(w, "c.area", httpx.QueryString(r, "area"))
	if area := security.AreaScope(r.Context()); area != nil {
		w.Add("c.area = ?", *area)
	}
	return database.Paginate[Categoria](r.Context(), h.db, categoriaSelect, "FROM categorias c", w,
		lq.OrderBy(map[string]string{"codigo": "c.codigo", "nombre": "c.nombre", "tipo": "c.tipo"}, "c.tipo, c.nombre"), lq)
}

func (h *Handler) createCategoria(r *http.Request) (any, error) {
	var in CategoriaInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	// Quien administra un área solo crea categorías de su área
	if area := security.AreaScope(r.Context()); area != nil {
		in.Area = *area
	}
	return h.categorias.Create(r.Context(), h.db, func(tx database.Tx) (id int64, err error) {
		err = tx.QueryRow(r.Context(), `INSERT INTO categorias (codigo, nombre, tipo, prefijo, icono, vida_util_meses,
			meses_mantenimiento, requiere_serie, habilitado, area)
			VALUES (upper($1), $2, $3, upper($4), $5, $6, $7, $8, coalesce($9, 1), coalesce(NULLIF($10, ''), 'SEGURIDAD_FISICA'));
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`, in.Codigo, in.Nombre, in.Tipo, in.Prefijo, in.Icono, in.VidaUtilMeses, in.MesesMantenimiento,
			in.RequiereSerie, in.Habilitado, in.Area).Scan(&id)
		return
	})
}

func (h *Handler) updateCategoria(r *http.Request) (any, error) {
	var in CategoriaInput
	id, err := bindWithID(r, &in)
	if err != nil {
		return nil, err
	}
	return h.categorias.Update(r.Context(), h.db, id, func(tx database.Tx, before Categoria) error {
		// El admin solo edita categorías de su área y no las cambia de área
		if area := security.AreaScope(r.Context()); area != nil {
			if before.Area != *area {
				return apperr.Forbidden("La categoría " + before.Nombre + " pertenece a otra área")
			}
			in.Area = *area
		}
		if before.TotalActivos > 0 && before.Prefijo != in.Prefijo {
			return apperr.Conflict("No se puede cambiar el prefijo de una categoría que ya tiene activos registrados")
		}
		_, err := tx.Exec(r.Context(), `UPDATE categorias SET codigo = upper($2), nombre = $3, tipo = $4, prefijo = upper($5),
			icono = $6, vida_util_meses = $7, meses_mantenimiento = $8, requiere_serie = $9, habilitado = coalesce($10, habilitado),
			area = coalesce(NULLIF($11, ''), area)
			WHERE id = $1`, id, in.Codigo, in.Nombre, in.Tipo, in.Prefijo, in.Icono, in.VidaUtilMeses, in.MesesMantenimiento,
			in.RequiereSerie, in.Habilitado, in.Area)
		return err
	})
}

// ---- Marcas ----

const marcaSelect = `SELECT m.*, (SELECT COUNT_BIG(*) FROM activos a WHERE a.marca_id = m.id) AS total_activos`

func findMarca(ctx context.Context, q database.Querier, id int64) (Marca, error) {
	m, err := database.Get[Marca](ctx, q, marcaSelect+" FROM marcas m WHERE m.id = $1", id)
	return m, apperr.FromDB(err, "Marca no encontrada")
}

func (h *Handler) listMarcas(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("m.nombre", lq.Search)
	database.Eq(w, "m.habilitado", httpx.QueryBool(r, "habilitado"))
	return database.Paginate[Marca](r.Context(), h.db, marcaSelect, "FROM marcas m", w,
		lq.OrderBy(map[string]string{"nombre": "m.nombre"}, "m.nombre"), lq)
}

func (h *Handler) createMarca(r *http.Request) (any, error) {
	var in MarcaInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	return h.marcas.Create(r.Context(), h.db, func(tx database.Tx) (id int64, err error) {
		err = tx.QueryRow(r.Context(), `INSERT INTO marcas (nombre, habilitado) VALUES ($1, coalesce($2, 1)); SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			in.Nombre, in.Habilitado).Scan(&id)
		return
	})
}

func (h *Handler) updateMarca(r *http.Request) (any, error) {
	var in MarcaInput
	id, err := bindWithID(r, &in)
	if err != nil {
		return nil, err
	}
	return h.marcas.Update(r.Context(), h.db, id, func(tx database.Tx, _ Marca) error {
		_, err := tx.Exec(r.Context(), `UPDATE marcas SET nombre = $2, habilitado = coalesce($3, habilitado) WHERE id = $1`,
			id, in.Nombre, in.Habilitado)
		return err
	})
}

// ---- Proveedores ----

func findProveedor(ctx context.Context, q database.Querier, id int64) (Proveedor, error) {
	p, err := database.Get[Proveedor](ctx, q, "SELECT * FROM proveedores WHERE id = $1", id)
	return p, apperr.FromDB(err, "Proveedor no encontrado")
}

func (h *Handler) listProveedores(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("p.ruc + ' ' + p.razon_social + ' ' + coalesce(p.contacto, '')", lq.Search)
	database.Eq(w, "p.habilitado", httpx.QueryBool(r, "habilitado"))
	return database.Paginate[Proveedor](r.Context(), h.db, "SELECT p.*", "FROM proveedores p", w,
		lq.OrderBy(map[string]string{"ruc": "p.ruc", "razonSocial": "p.razon_social"}, "p.razon_social"), lq)
}

func (h *Handler) createProveedor(r *http.Request) (any, error) {
	var in ProveedorInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	return h.proveedores.Create(r.Context(), h.db, func(tx database.Tx) (id int64, err error) {
		err = tx.QueryRow(r.Context(), `INSERT INTO proveedores (ruc, razon_social, contacto, telefono, email, direccion, habilitado)
			VALUES ($1, $2, $3, $4, $5, $6, coalesce($7, 1)); SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			in.Ruc, in.RazonSocial, in.Contacto, in.Telefono, in.Email, in.Direccion, in.Habilitado).Scan(&id)
		return
	})
}

func (h *Handler) updateProveedor(r *http.Request) (any, error) {
	var in ProveedorInput
	id, err := bindWithID(r, &in)
	if err != nil {
		return nil, err
	}
	return h.proveedores.Update(r.Context(), h.db, id, func(tx database.Tx, _ Proveedor) error {
		_, err := tx.Exec(r.Context(), `UPDATE proveedores SET ruc = $2, razon_social = $3, contacto = $4, telefono = $5,
			email = $6, direccion = $7, habilitado = coalesce($8, habilitado) WHERE id = $1`,
			id, in.Ruc, in.RazonSocial, in.Contacto, in.Telefono, in.Email, in.Direccion, in.Habilitado)
		return err
	})
}

// ---- Técnicos ----

const tecnicoSelect = `SELECT t.*, (SELECT COUNT_BIG(*) FROM mantenimientos m WHERE m.tecnico_id = t.id) AS total_mantenimientos`

func findTecnico(ctx context.Context, q database.Querier, id int64) (Tecnico, error) {
	t, err := database.Get[Tecnico](ctx, q, tecnicoSelect+" FROM tecnicos t WHERE t.id = $1", id)
	return t, apperr.FromDB(err, "Técnico no encontrado")
}

func (h *Handler) listTecnicos(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("t.nombres + ' ' + coalesce(t.empresa, '') + ' ' + coalesce(t.especialidad, '')", lq.Search)
	database.Eq(w, "t.tipo", httpx.QueryString(r, "tipo"))
	database.Eq(w, "t.habilitado", httpx.QueryBool(r, "habilitado"))
	return database.Paginate[Tecnico](r.Context(), h.db, tecnicoSelect, "FROM tecnicos t", w,
		lq.OrderBy(map[string]string{"nombres": "t.nombres", "tipo": "t.tipo", "empresa": "t.empresa"}, "t.nombres"), lq)
}

func (h *Handler) createTecnico(r *http.Request) (any, error) {
	var in TecnicoInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	return h.tecnicos.Create(r.Context(), h.db, func(tx database.Tx) (id int64, err error) {
		err = tx.QueryRow(r.Context(), `INSERT INTO tecnicos (nombres, tipo, empresa, especialidad, telefono, email, empleado_id,
			habilitado) VALUES ($1, $2, $3, $4, $5, $6, $7, coalesce($8, 1)); SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			in.Nombres, in.Tipo, in.Empresa, in.Especialidad, in.Telefono, in.Email, in.EmpleadoID, in.Habilitado).Scan(&id)
		return
	})
}

func (h *Handler) updateTecnico(r *http.Request) (any, error) {
	var in TecnicoInput
	id, err := bindWithID(r, &in)
	if err != nil {
		return nil, err
	}
	return h.tecnicos.Update(r.Context(), h.db, id, func(tx database.Tx, _ Tecnico) error {
		_, err := tx.Exec(r.Context(), `UPDATE tecnicos SET nombres = $2, tipo = $3, empresa = $4, especialidad = $5,
			telefono = $6, email = $7, empleado_id = $8, habilitado = coalesce($9, habilitado) WHERE id = $1`,
			id, in.Nombres, in.Tipo, in.Empresa, in.Especialidad, in.Telefono, in.Email, in.EmpleadoID, in.Habilitado)
		return err
	})
}

// ---- Opciones para formularios ----

type Opcion struct {
	ID     int64   `json:"id" db:"id"`
	Nombre string  `json:"nombre" db:"nombre"`
	Extra  *string `json:"extra" db:"extra"`
}

type Opciones struct {
	Agencias      []Opcion `json:"agencias"`
	Departamentos []Opcion `json:"departamentos"`
	Categorias    []Opcion `json:"categorias"`
	Marcas        []Opcion `json:"marcas"`
	Proveedores   []Opcion `json:"proveedores"`
	Tecnicos      []Opcion `json:"tecnicos"`
}

// Opciones devuelve en una sola llamada los catálogos habilitados para combos.
// "extra" contiene el tipo (agencias/categorías/técnicos) o el icono de la categoría.
func (h *Handler) Opciones(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var out Opciones
	// El admin solo elige categorías de su área (el área sale de una constante del código)
	filtroCategorias := ""
	if area := security.AreaScope(ctx); area != nil {
		filtroCategorias = " AND area = '" + *area + "'"
	}
	queries := []struct {
		dst *[]Opcion
		sql string
	}{
		{&out.Agencias, `SELECT id, nombre, tipo AS extra FROM agencias WHERE habilitado = 1 ORDER BY CASE WHEN tipo = 'MATRIZ' THEN 0 ELSE 1 END, nombre`},
		{&out.Departamentos, `SELECT id, nombre, codigo AS extra FROM departamentos WHERE habilitado = 1 ORDER BY nombre`},
		{&out.Categorias, `SELECT id, nombre, codigo + '|' + tipo + '|' + coalesce(icono, '') + '|' + CASE WHEN requiere_serie = 1 THEN 'true' ELSE 'false' END AS extra
		       FROM categorias WHERE habilitado = 1` + filtroCategorias + ` ORDER BY tipo, nombre`},
		{&out.Marcas, `SELECT id, nombre, CAST(NULL AS NVARCHAR(MAX)) AS extra FROM marcas WHERE habilitado = 1 ORDER BY nombre`},
		{&out.Proveedores, `SELECT id, razon_social AS nombre, ruc AS extra FROM proveedores WHERE habilitado = 1 ORDER BY razon_social`},
		{&out.Tecnicos, `SELECT id, nombres AS nombre, tipo + coalesce(' - ' + empresa, '') AS extra FROM tecnicos
		       WHERE habilitado = 1 ORDER BY nombres`},
	}
	for _, q := range queries {
		items, err := database.List[Opcion](ctx, h.db, q.sql)
		if err != nil {
			httpx.Error(w, r, apperr.Internal(err))
			return
		}
		*q.dst = items
	}
	httpx.OK(w, out)
}
