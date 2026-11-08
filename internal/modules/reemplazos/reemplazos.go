// Package reemplazos registra el cambio de componentes (RAM, discos, fuentes, etc.)
// de un equipo, ya sea dentro de un mantenimiento o de forma independiente.
package reemplazos

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/modules/bodega"
	"sgat-api/internal/modules/inventario"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

// Item describe un reemplazo. Debe indicar al menos uno de: componente retirado,
// componente instalado (activo inventariado) o repuesto de bodega.
type Item struct {
	ComponenteRetiradoID  *int64  `json:"componenteRetiradoId" validate:"omitempty,gt=0"`
	DestinoRetirado       string  `json:"destinoRetirado" validate:"omitempty,oneof=DANADO DADO_DE_BAJA DISPONIBLE"`
	ComponenteInstaladoID *int64  `json:"componenteInstaladoId" validate:"omitempty,gt=0"`
	RepuestoID            *int64  `json:"repuestoId" validate:"omitempty,gt=0"`
	Cantidad              int     `json:"cantidad" validate:"omitempty,gt=0,max=1000"`
	Descripcion           string  `json:"descripcion" validate:"required,max=500"`
	Motivo                string  `json:"motivo" validate:"max=300"`
	Costo                 float64 `json:"costo" validate:"gte=0"`
}

// Registrar aplica el reemplazo sobre el equipo dentro de la transacción.
func Registrar(ctx context.Context, tx database.Tx, equipo inventario.Activo, mantenimientoID *int64, fecha types.Date, it Item) (int64, error) {
	if it.ComponenteRetiradoID == nil && it.ComponenteInstaladoID == nil && it.RepuestoID == nil {
		return 0, apperr.BadRequest("Indique el componente retirado, el componente instalado o el repuesto utilizado")
	}
	if it.Cantidad == 0 {
		it.Cantidad = 1
	}
	var detalles []string

	if it.ComponenteRetiradoID != nil {
		c, err := inventario.Lock(ctx, tx, *it.ComponenteRetiradoID)
		if err != nil {
			return 0, err
		}
		if c.PadreID == nil || *c.PadreID != equipo.ID {
			return 0, apperr.Conflict("El componente " + c.Codigo + " no está instalado en el equipo " + equipo.Codigo)
		}
		if err := retirar(ctx, tx, c, equipo, it.DestinoRetirado, it.Motivo, fecha); err != nil {
			return 0, err
		}
		detalles = append(detalles, "retirado "+c.Etiqueta())
	}

	if it.ComponenteInstaladoID != nil {
		c, err := inventario.LockOperable(ctx, tx, *it.ComponenteInstaladoID)
		if err != nil {
			return 0, err
		}
		if !c.EsComponente() {
			return 0, apperr.Conflict(c.Codigo + " no es un componente (RAM, disco, procesador, fuente...)")
		}
		if c.PadreID != nil {
			return 0, apperr.Conflict("El componente " + c.Codigo + " ya está instalado en otro equipo")
		}
		if c.ID == equipo.ID {
			return 0, apperr.Conflict("Un activo no puede instalarse en sí mismo")
		}
		if err := instalar(ctx, tx, c, equipo); err != nil {
			return 0, err
		}
		detalles = append(detalles, "instalado "+c.Etiqueta())
	}

	if it.RepuestoID != nil {
		_, costoUnit, err := bodega.RegistrarMovimiento(ctx, tx, bodega.NuevoMovimiento{
			RepuestoID: *it.RepuestoID, Tipo: "SALIDA", Cantidad: it.Cantidad, ActivoID: &equipo.ID,
			MantenimientoID: mantenimientoID, Motivo: "Reemplazo en " + equipo.Codigo + ": " + it.Descripcion,
		})
		if err != nil {
			return 0, err
		}
		if it.Costo == 0 {
			it.Costo = costoUnit * float64(it.Cantidad)
		}
		detalles = append(detalles, fmt.Sprintf("repuesto de bodega x%d", it.Cantidad))
	}

	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO reemplazos (activo_id, mantenimiento_id, componente_retirado_id, componente_instalado_id, repuesto_id,
		                        cantidad, descripcion, motivo, costo, fecha, usuario_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10, $11);
		SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
		equipo.ID, mantenimientoID, it.ComponenteRetiradoID, it.ComponenteInstaladoID, it.RepuestoID, it.Cantidad,
		it.Descripcion, it.Motivo, it.Costo, fecha, security.UserID(ctx)).Scan(&id)
	if err != nil {
		return 0, err
	}

	desc := "Reemplazo: " + it.Descripcion
	for i, d := range detalles {
		if i == 0 {
			desc += " ("
		} else {
			desc += ", "
		}
		desc += d
		if i == len(detalles)-1 {
			desc += ")"
		}
	}
	if err := audit.History(ctx, tx, audit.Event{ActivoID: equipo.ID, Tipo: audit.EvReemplazo, Descripcion: desc,
		Referencia: "reemplazos", ReferenciaID: &id, Datos: it}); err != nil {
		return 0, err
	}
	return id, nil
}

// retirar desinstala el componente del equipo y define su destino.
func retirar(ctx context.Context, tx database.Tx, c, equipo inventario.Activo, destino, motivo string, fecha types.Date) error {
	estado := inventario.EstadoDanado
	switch destino {
	case "DADO_DE_BAJA":
		estado = inventario.EstadoBaja
	case "DISPONIBLE":
		estado = inventario.EstadoActivo
	}
	baja := estado == inventario.EstadoBaja
	if _, err := tx.Exec(ctx, `
		UPDATE activos SET activo_padre_id = NULL, empleado_id = NULL, estado = $2,
		       fecha_baja = CASE WHEN $3 = 1 THEN CAST($4 AS DATE) ELSE fecha_baja END,
		       motivo_baja = CASE WHEN $3 = 1 THEN $5 ELSE motivo_baja END, updated_by = $6
		WHERE id = $1`, c.ID, estado, baja, fecha, "Retirado de "+equipo.Codigo+". "+motivo, security.UserID(ctx)); err != nil {
		return err
	}
	tipo := audit.EvReemplazo
	if baja {
		tipo = audit.EvBaja
	}
	return audit.History(ctx, tx, audit.Event{ActivoID: c.ID, Tipo: tipo,
		Descripcion: fmt.Sprintf("Retirado del equipo %s. Nuevo estado: %s", equipo.Codigo, inventario.EstadoNombre[estado]),
		Referencia:  "activos", ReferenciaID: &equipo.ID})
}

// instalar coloca el componente dentro del equipo con su misma ubicación y responsable.
func instalar(ctx context.Context, tx database.Tx, c, equipo inventario.Activo) error {
	// Si el componente estaba asignado de forma independiente, se cierra esa asignación
	if _, err := tx.Exec(ctx, `UPDATE asignaciones SET estado = 'DEVUELTA', fecha_devolucion = CASE WHEN CAST(GETDATE() AS DATE) > fecha_asignacion THEN CAST(GETDATE() AS DATE) ELSE fecha_asignacion END,
		motivo_devolucion = 'Instalado en ' + $2 WHERE activo_id = $1 AND estado = 'VIGENTE'`, c.ID, equipo.Codigo); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE activos SET activo_padre_id = $2, agencia_id = $3, departamento_id = $4, empleado_id = $5,
		       estado = 'ACTIVO', updated_by = $6
		WHERE id = $1`, c.ID, equipo.ID, equipo.AgenciaID, equipo.DepartamentoID, equipo.EmpleadoID, security.UserID(ctx)); err != nil {
		return err
	}
	return audit.History(ctx, tx, audit.Event{ActivoID: c.ID, Tipo: audit.EvReemplazo,
		Descripcion: "Instalado en el equipo " + equipo.Codigo, Referencia: "activos", ReferenciaID: &equipo.ID})
}

// ---- Endpoints ----

type Reemplazo struct {
	ID                    int64      `json:"id" db:"id"`
	ActivoID              int64      `json:"activoId" db:"activo_id"`
	ActivoCodigo          string     `json:"activoCodigo" db:"activo_codigo"`
	MantenimientoID       *int64     `json:"mantenimientoId" db:"mantenimiento_id"`
	MantenimientoCodigo   *string    `json:"mantenimientoCodigo" db:"mantenimiento_codigo"`
	ComponenteRetiradoID  *int64     `json:"componenteRetiradoId" db:"componente_retirado_id"`
	ComponenteRetirado    *string    `json:"componenteRetirado" db:"componente_retirado"`
	ComponenteInstaladoID *int64     `json:"componenteInstaladoId" db:"componente_instalado_id"`
	ComponenteInstalado   *string    `json:"componenteInstalado" db:"componente_instalado"`
	RepuestoID            *int64     `json:"repuestoId" db:"repuesto_id"`
	Repuesto              *string    `json:"repuesto" db:"repuesto"`
	Cantidad              int        `json:"cantidad" db:"cantidad"`
	Descripcion           string     `json:"descripcion" db:"descripcion"`
	Motivo                *string    `json:"motivo" db:"motivo"`
	Costo                 float64    `json:"costo" db:"costo"`
	Fecha                 types.Date `json:"fecha" db:"fecha"`
	Usuario               *string    `json:"usuario" db:"usuario"`
	CreatedAt             time.Time  `json:"createdAt" db:"created_at"`
}

const SelectSQL = `SELECT r.id, r.activo_id, a.codigo AS activo_codigo, r.mantenimiento_id, m.codigo AS mantenimiento_codigo,
	r.componente_retirado_id, cr.codigo + ' - ' + ccr.nombre AS componente_retirado,
	r.componente_instalado_id, ci.codigo + ' - ' + cci.nombre AS componente_instalado,
	r.repuesto_id, rp.codigo + ' - ' + rp.nombre AS repuesto,
	r.cantidad, r.descripcion, r.motivo, r.costo, r.fecha, u.nombres + ' ' + u.apellidos AS usuario, r.created_at`

const FromSQL = `FROM reemplazos r
	JOIN activos a ON a.id = r.activo_id
	LEFT JOIN mantenimientos m ON m.id = r.mantenimiento_id
	LEFT JOIN activos cr ON cr.id = r.componente_retirado_id
	LEFT JOIN categorias ccr ON ccr.id = cr.categoria_id
	LEFT JOIN activos ci ON ci.id = r.componente_instalado_id
	LEFT JOIN categorias cci ON cci.id = ci.categoria_id
	LEFT JOIN repuestos rp ON rp.id = r.repuesto_id
	LEFT JOIN usuarios u ON u.id = r.usuario_id`

type Handler struct{ db *database.DB }

func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.HandleCreated(h.create))
}

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("a.codigo + ' ' + r.descripcion", lq.Search)
	database.Eq(w, "r.activo_id", httpx.QueryInt64(r, "activoId"))
	database.Eq(w, "r.mantenimiento_id", httpx.QueryInt64(r, "mantenimientoId"))
	if v := httpx.QueryString(r, "desde"); v != nil {
		w.Add("r.fecha >= CAST(? AS DATE)", *v)
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		w.Add("r.fecha <= CAST(? AS DATE)", *v)
	}
	return database.Paginate[Reemplazo](r.Context(), h.db, SelectSQL, FromSQL, w,
		lq.OrderBy(map[string]string{"fecha": "r.fecha", "activoCodigo": "a.codigo", "costo": "r.costo"}, "r.id DESC"), lq)
}

type createInput struct {
	ActivoID int64       `json:"activoId" validate:"required,gt=0"`
	Fecha    *types.Date `json:"fecha"`
	Item
}

func (h *Handler) create(r *http.Request) (any, error) {
	var in createInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	fecha := types.Today()
	if in.Fecha != nil {
		fecha = *in.Fecha
	}
	var out Reemplazo
	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		equipo, err := inventario.LockOperable(ctx, tx, in.ActivoID)
		if err != nil {
			return err
		}
		if equipo.EsComponente() {
			return apperr.Conflict("Los reemplazos se registran sobre el equipo principal, no sobre un componente")
		}
		id, err := Registrar(ctx, tx, equipo, nil, fecha, in.Item)
		if err != nil {
			return err
		}
		if out, err = database.Get[Reemplazo](ctx, tx, SelectSQL+" "+FromSQL+" WHERE r.id = $1", id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Crear, Entidad: "reemplazos", EntidadID: &id,
			Descripcion: "Reemplazo en " + equipo.Codigo + ": " + in.Descripcion, Despues: out})
	})
	return out, apperr.FromDB(err, "")
}
