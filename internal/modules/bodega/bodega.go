// Package bodega gestiona los repuestos e insumos y su kardex de movimientos.
// El stock se actualiza en la base de datos mediante el trigger trg_movimientos_stock.
package bodega

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/crud"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/middleware"
	"sgat-api/internal/security"
)

type Repuesto struct {
	ID              int64     `json:"id" db:"id"`
	Codigo          string    `json:"codigo" db:"codigo"`
	Nombre          string    `json:"nombre" db:"nombre"`
	CategoriaID     *int64    `json:"categoriaId" db:"categoria_id"`
	Categoria       *string   `json:"categoria" db:"categoria"`
	MarcaID         *int64    `json:"marcaId" db:"marca_id"`
	Marca           *string   `json:"marca" db:"marca"`
	Descripcion     *string   `json:"descripcion" db:"descripcion"`
	Unidad          string    `json:"unidad" db:"unidad"`
	StockActual     int       `json:"stockActual" db:"stock_actual"`
	StockMinimo     int       `json:"stockMinimo" db:"stock_minimo"`
	CostoUnitario   float64   `json:"costoUnitario" db:"costo_unitario"`
	ValorInventario float64   `json:"valorInventario" db:"valor_inventario"`
	StockBajo       bool      `json:"stockBajo" db:"stock_bajo"`
	AgenciaID       *int64    `json:"agenciaId" db:"agencia_id"`
	Agencia         *string   `json:"agencia" db:"agencia"`
	Ubicacion       *string   `json:"ubicacion" db:"ubicacion"`
	Habilitado      bool      `json:"habilitado" db:"habilitado"`
	CreatedAt       time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt       time.Time `json:"updatedAt" db:"updated_at"`
	// Bodega a la que pertenece: SEGURIDAD_FISICA (repuestos) o PROVEEDURIA (materiales)
	Area string `json:"area" db:"area"`
}

type RepuestoInput struct {
	// Solo el SUPERADMIN elige la bodega; los demás registran en la de su área
	Area          string  `json:"area" validate:"omitempty,oneof=SEGURIDAD_FISICA PROVEEDURIA"`
	Codigo        string  `json:"codigo" validate:"required,max=30"`
	Nombre        string  `json:"nombre" validate:"required,max=150"`
	CategoriaID   *int64  `json:"categoriaId" validate:"omitempty,gt=0"`
	MarcaID       *int64  `json:"marcaId" validate:"omitempty,gt=0"`
	Descripcion   *string `json:"descripcion" validate:"omitempty,max=500"`
	Unidad        string  `json:"unidad" validate:"required,max=20"`
	StockMinimo   int     `json:"stockMinimo" validate:"gte=0"`
	CostoUnitario float64 `json:"costoUnitario" validate:"gte=0"`
	AgenciaID     *int64  `json:"agenciaId" validate:"omitempty,gt=0"`
	Ubicacion     *string `json:"ubicacion" validate:"omitempty,max=120"`
	Habilitado    *bool   `json:"habilitado"`
	// Solo al crear: stock inicial (genera un movimiento de ENTRADA)
	StockInicial int `json:"stockInicial" validate:"gte=0"`
}

type Movimiento struct {
	ID              int64     `json:"id" db:"id"`
	RepuestoID      int64     `json:"repuestoId" db:"repuesto_id"`
	RepuestoCodigo  string    `json:"repuestoCodigo" db:"repuesto_codigo"`
	Repuesto        string    `json:"repuesto" db:"repuesto"`
	Tipo            string    `json:"tipo" db:"tipo"`
	Cantidad        int       `json:"cantidad" db:"cantidad"`
	StockAnterior   int       `json:"stockAnterior" db:"stock_anterior"`
	StockResultante int       `json:"stockResultante" db:"stock_resultante"`
	CostoUnitario   float64   `json:"costoUnitario" db:"costo_unitario"`
	Motivo          string    `json:"motivo" db:"motivo"`
	Documento       *string   `json:"documento" db:"documento"`
	ActivoID        *int64    `json:"activoId" db:"activo_id"`
	ActivoCodigo    *string   `json:"activoCodigo" db:"activo_codigo"`
	MantenimientoID *int64    `json:"mantenimientoId" db:"mantenimiento_id"`
	Usuario         *string   `json:"usuario" db:"usuario"`
	CreatedAt       time.Time `json:"createdAt" db:"created_at"`
}

// NuevoMovimiento se usa también desde reemplazos/mantenimientos para descargar repuestos.
type NuevoMovimiento struct {
	RepuestoID      int64    `json:"repuestoId" validate:"required,gt=0"`
	// BAJA: material dañado, vencido o perdido (descuenta stock igual que una salida, pero se reporta aparte)
	Tipo            string   `json:"tipo" validate:"required,oneof=ENTRADA SALIDA AJUSTE BAJA"`
	Cantidad        int      `json:"cantidad" validate:"gte=0"`
	CostoUnitario   *float64 `json:"costoUnitario" validate:"omitempty,gte=0"`
	Motivo          string   `json:"motivo" validate:"required,max=300"`
	Documento       *string  `json:"documento" validate:"omitempty,max=60"`
	ActivoID        *int64   `json:"activoId" validate:"omitempty,gt=0"`
	MantenimientoID *int64   `json:"-"`
}

// RegistrarMovimiento inserta el movimiento (el trigger INSTEAD OF trg_movimientos_stock valida y actualiza el stock).
// Devuelve el id y el costo unitario aplicado.
func RegistrarMovimiento(ctx context.Context, tx database.Tx, m NuevoMovimiento) (int64, float64, error) {
	if m.Tipo != "AJUSTE" && m.Cantidad <= 0 {
		return 0, 0, apperr.Validation(map[string]string{"cantidad": "Debe ser mayor a 0"})
	}
	var id int64
	var costo float64
	// El trigger es INSTEAD OF: el id insertado se obtiene con @@IDENTITY (SCOPE_IDENTITY queda en el trigger)
	err := tx.QueryRow(ctx, `
		INSERT INTO movimientos_bodega (repuesto_id, tipo, cantidad, costo_unitario, motivo, documento, activo_id,
		                                mantenimiento_id, usuario_id)
		SELECT $1, $2, $3, coalesce($4, (SELECT costo_unitario FROM repuestos WHERE id = $1)), $5, $6, $7, $8, $9;
		SELECT id, costo_unitario FROM movimientos_bodega WHERE id = CAST(@@IDENTITY AS BIGINT)`,
		m.RepuestoID, m.Tipo, m.Cantidad, m.CostoUnitario, m.Motivo, m.Documento, m.ActivoID, m.MantenimientoID,
		security.UserID(ctx)).Scan(&id, &costo)
	if err != nil {
		return 0, 0, apperr.FromDB(err, "Repuesto no encontrado")
	}
	// En las entradas con costo se actualiza el costo unitario de referencia
	if m.Tipo == "ENTRADA" && m.CostoUnitario != nil {
		if _, err := tx.Exec(ctx, `UPDATE repuestos SET costo_unitario = $2 WHERE id = $1`, m.RepuestoID, *m.CostoUnitario); err != nil {
			return 0, 0, err
		}
	}
	return id, costo, nil
}

// ---- Endpoints ----

type Handler struct {
	db  *database.DB
	res crud.Resource[Repuesto]
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{db: db, res: crud.Resource[Repuesto]{Table: "repuestos", NotFound: "Repuesto no encontrado",
		Find: findRepuesto, Label: func(r Repuesto) string { return "Repuesto " + r.Codigo + " " + r.Nombre }}}
}

func (h *Handler) Routes(r chi.Router) {
	r.Get("/repuestos", httpx.Handle(h.list))
	r.Post("/repuestos", httpx.HandleCreated(h.create))
	r.Get("/repuestos/{id}", httpx.Handle(h.get))
	r.Put("/repuestos/{id}", httpx.Handle(h.update))
	r.With(middleware.SuperAdminOnly).Delete("/repuestos/{id}", httpx.Handle(h.delete))
	r.Get("/movimientos", httpx.Handle(h.listMovimientos))
	r.Post("/movimientos", httpx.HandleCreated(h.createMovimiento))
}

// findRepuesto busca el artículo; cada usuario solo ve los de la bodega de su área.
func findRepuesto(ctx context.Context, q database.Querier, id int64) (Repuesto, error) {
	r, err := database.Get[Repuesto](ctx, q, "SELECT * FROM v_repuestos WHERE id = $1", id)
	if err != nil {
		return r, apperr.FromDB(err, "Repuesto no encontrado")
	}
	if area := security.AreaScope(ctx); area != nil && r.Area != *area {
		return r, apperr.NotFound("Repuesto no encontrado")
	}
	return r, nil
}

// VerificarRepuesto impide mover stock de la bodega de otra área (usado también por las entregas de materiales).
func VerificarRepuesto(ctx context.Context, q database.Querier, id int64) (Repuesto, error) {
	return findRepuesto(ctx, q, id)
}

// areaDe decide en qué bodega se registra un artículo: la del usuario o, para el SUPERADMIN, la elegida.
func areaDe(ctx context.Context, elegida string) string {
	if area := security.AreaScope(ctx); area != nil {
		return *area
	}
	if elegida != "" {
		return elegida
	}
	return security.AreaSeguridadFisica
}

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("v.codigo + ' ' + v.nombre + ' ' + coalesce(v.marca, '')", lq.Search)
	database.Eq(w, "v.categoria_id", httpx.QueryInt64(r, "categoriaId"))
	database.Eq(w, "v.habilitado", httpx.QueryBool(r, "habilitado"))
	database.Eq(w, "v.stock_bajo", httpx.QueryBool(r, "stockBajo"))
	database.Eq(w, "v.area", httpx.QueryString(r, "area"))
	if area := security.AreaScope(r.Context()); area != nil {
		w.Add("v.area = ?", *area)
	}
	return database.Paginate[Repuesto](r.Context(), h.db, "SELECT v.*", "FROM v_repuestos v", w,
		lq.OrderBy(map[string]string{"codigo": "v.codigo", "nombre": "v.nombre", "stockActual": "v.stock_actual",
			"categoria": "v.categoria", "valorInventario": "v.valor_inventario"}, "v.stock_bajo DESC, v.nombre"), lq)
}

func (h *Handler) get(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return findRepuesto(r.Context(), h.db, id)
}

func (h *Handler) create(r *http.Request) (any, error) {
	var in RepuestoInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	return h.res.Create(ctx, h.db, func(tx database.Tx) (id int64, err error) {
		if err = tx.QueryRow(ctx, `INSERT INTO repuestos (codigo, nombre, categoria_id, marca_id, descripcion, unidad,
			stock_minimo, costo_unitario, agencia_id, ubicacion, habilitado, area)
			VALUES (upper($1), $2, $3, $4, $5, upper($6), $7, $8, $9, $10, coalesce($11, 1), $12);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			in.Codigo, in.Nombre, in.CategoriaID, in.MarcaID, in.Descripcion, in.Unidad, in.StockMinimo, in.CostoUnitario,
			in.AgenciaID, in.Ubicacion, in.Habilitado, areaDe(ctx, in.Area)).Scan(&id); err != nil {
			return
		}
		if in.StockInicial > 0 {
			_, _, err = RegistrarMovimiento(ctx, tx, NuevoMovimiento{RepuestoID: id, Tipo: "ENTRADA",
				Cantidad: in.StockInicial, Motivo: "Stock inicial"})
		}
		return
	})
}

func (h *Handler) update(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in RepuestoInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	// findRepuesto (usado por Update) ya impide editar artículos de la bodega de otra área
	return h.res.Update(r.Context(), h.db, id, func(tx database.Tx, before Repuesto) error {
		area := before.Area
		if security.AreaScope(r.Context()) == nil && in.Area != "" {
			area = in.Area
		}
		_, err := tx.Exec(r.Context(), `UPDATE repuestos SET codigo = upper($2), nombre = $3, categoria_id = $4, marca_id = $5,
			descripcion = $6, unidad = upper($7), stock_minimo = $8, costo_unitario = $9, agencia_id = $10, ubicacion = $11,
			habilitado = coalesce($12, habilitado), area = $13 WHERE id = $1`,
			id, in.Codigo, in.Nombre, in.CategoriaID, in.MarcaID, in.Descripcion, in.Unidad, in.StockMinimo, in.CostoUnitario,
			in.AgenciaID, in.Ubicacion, in.Habilitado, area)
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
	return httpx.MessageResult("Repuesto eliminado"), nil
}

const movimientoSelect = `SELECT m.id, m.repuesto_id, r.codigo AS repuesto_codigo, r.nombre AS repuesto, m.tipo, m.cantidad,
	m.stock_anterior, m.stock_resultante, m.costo_unitario, m.motivo, m.documento, m.activo_id, a.codigo AS activo_codigo,
	m.mantenimiento_id, u.nombres + ' ' + u.apellidos AS usuario, m.created_at`

const movimientoFrom = `FROM movimientos_bodega m
	JOIN repuestos r ON r.id = m.repuesto_id
	LEFT JOIN activos a ON a.id = m.activo_id
	LEFT JOIN usuarios u ON u.id = m.usuario_id`

func (h *Handler) listMovimientos(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("r.codigo + ' ' + r.nombre + ' ' + m.motivo + ' ' + coalesce(m.documento, '')", lq.Search)
	database.Eq(w, "m.repuesto_id", httpx.QueryInt64(r, "repuestoId"))
	database.Eq(w, "m.tipo", httpx.QueryString(r, "tipo"))
	if area := security.AreaScope(r.Context()); area != nil {
		w.Add("r.area = ?", *area)
	}
	if v := httpx.QueryString(r, "desde"); v != nil {
		w.Add("CAST(m.created_at AS DATE) >= CAST(? AS DATE)", *v)
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		w.Add("CAST(m.created_at AS DATE) <= CAST(? AS DATE)", *v)
	}
	return database.Paginate[Movimiento](r.Context(), h.db, movimientoSelect, movimientoFrom, w,
		lq.OrderBy(map[string]string{"createdAt": "m.created_at", "tipo": "m.tipo", "repuesto": "r.nombre"}, "m.id DESC"), lq)
}

func (h *Handler) createMovimiento(r *http.Request) (any, error) {
	var in NuevoMovimiento
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var out Movimiento
	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		if _, err := VerificarRepuesto(ctx, tx, in.RepuestoID); err != nil {
			return err
		}
		id, _, err := RegistrarMovimiento(ctx, tx, in)
		if err != nil {
			return err
		}
		if out, err = database.Get[Movimiento](ctx, tx, movimientoSelect+" "+movimientoFrom+" WHERE m.id = $1", id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Crear, Entidad: "movimientos_bodega", EntidadID: &id,
			Descripcion: in.Tipo + " de " + out.Repuesto + ": " + in.Motivo, Despues: out})
	})
	return out, apperr.FromDB(err, "")
}
