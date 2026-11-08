// Package averias gestiona las fallas e incidencias reportadas sobre los activos.
package averias

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/modules/inventario"
	"sgat-api/internal/security"
)

type Averia struct {
	ID             int64      `json:"id" db:"id"`
	Codigo         string     `json:"codigo" db:"codigo"`
	ActivoID       int64      `json:"activoId" db:"activo_id"`
	ActivoCodigo   string     `json:"activoCodigo" db:"activo_codigo"`
	Categoria      string     `json:"categoria" db:"categoria"`
	AgenciaID      int64      `json:"agenciaId" db:"agencia_id"`
	Agencia        string     `json:"agencia" db:"agencia"`
	// Quien reportó pide un equipo de reemplazo (lo envía el administrador)
	SolicitaReemplazo bool `json:"solicitaReemplazo" db:"solicita_reemplazo"`
	ReportadoPorID *int64     `json:"reportadoPorId" db:"reportado_por"`
	ReportadoPor   *string    `json:"reportadoPorNombre" db:"reportado_por_nombre"`
	FechaReporte   time.Time  `json:"fechaReporte" db:"fecha_reporte"`
	Descripcion    string     `json:"descripcion" db:"descripcion"`
	Severidad      string     `json:"severidad" db:"severidad"`
	Estado         string     `json:"estado" db:"estado"`
	Solucion       *string    `json:"solucion" db:"solucion"`
	FechaSolucion  *time.Time `json:"fechaSolucion" db:"fecha_solucion"`
	Mantenimientos int64      `json:"totalMantenimientos" db:"total_mantenimientos"`
	Usuario        *string    `json:"usuario" db:"usuario"`
	CreatedAt      time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time  `json:"updatedAt" db:"updated_at"`
}

const selectSQL = `SELECT av.id, av.codigo, av.activo_id, a.codigo AS activo_codigo, c.nombre AS categoria, a.agencia_id,
	ag.nombre AS agencia, av.solicita_reemplazo,
	av.reportado_por, e.nombres + ' ' + e.apellidos AS reportado_por_nombre, av.fecha_reporte, av.descripcion, av.severidad,
	av.estado, av.solucion, av.fecha_solucion, u.nombres + ' ' + u.apellidos AS usuario, av.created_at, av.updated_at,
	(SELECT count(*) FROM mantenimientos m WHERE m.averia_id = av.id) AS total_mantenimientos`

const fromSQL = `FROM averias av
	JOIN activos a ON a.id = av.activo_id
	JOIN categorias c ON c.id = a.categoria_id
	JOIN agencias ag ON ag.id = a.agencia_id
	LEFT JOIN empleados e ON e.id = av.reportado_por
	LEFT JOIN usuarios u ON u.id = av.usuario_id`

type Handler struct{ db *database.DB }

func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.HandleCreated(h.create))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Put("/{id}", httpx.Handle(h.update))
	r.Patch("/{id}/estado", httpx.Handle(h.cambiarEstado))
}

// scope: el encargado ve los daños de su agencia y cada área, los de sus activos.
func scope(ctx context.Context, w *database.Where) {
	if ag := security.AgenciaScope(ctx); ag != nil {
		w.Add("a.agencia_id = ?", *ag)
	}
	if cond := inventario.DepartamentoSQL(ctx, "a.departamento_id"); cond != "" {
		w.Add(cond, *security.DepartamentoScope(ctx))
	}
	if cond := inventario.AreaSQL(ctx, "a.categoria_id"); cond != "" {
		w.Add(cond, *security.AreaScope(ctx))
	}
}

func find(ctx context.Context, q database.Querier, id int64) (Averia, error) {
	w := (&database.Where{}).Add("av.id = ?", id)
	scope(ctx, w)
	a, err := database.Get[Averia](ctx, q, selectSQL+" "+fromSQL+w.SQL(), w.Args()...)
	return a, apperr.FromDB(err, "Avería no encontrada")
}

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("av.codigo + ' ' + a.codigo + ' ' + av.descripcion", lq.Search)
	database.Eq(w, "av.estado", httpx.QueryString(r, "estado"))
	database.Eq(w, "av.severidad", httpx.QueryString(r, "severidad"))
	database.Eq(w, "av.activo_id", httpx.QueryInt64(r, "activoId"))
	database.Eq(w, "a.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	database.Eq(w, "av.solicita_reemplazo", httpx.QueryBool(r, "solicitaReemplazo"))
	if v := httpx.QueryBool(r, "abiertas"); v != nil && *v {
		w.Add("av.estado IN ('REPORTADA', 'EN_REVISION')")
	}
	scope(r.Context(), w)
	return database.Paginate[Averia](r.Context(), h.db, selectSQL, fromSQL, w,
		lq.OrderBy(map[string]string{"codigo": "av.codigo", "fechaReporte": "av.fecha_reporte", "severidad": "av.severidad",
			"estado": "av.estado", "activoCodigo": "a.codigo"}, "av.id DESC"), lq)
}

func (h *Handler) get(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return find(r.Context(), h.db, id)
}

type createInput struct {
	ActivoID     int64  `json:"activoId" validate:"required,gt=0"`
	ReportadoPor *int64 `json:"reportadoPorId" validate:"omitempty,gt=0"`
	Descripcion  string `json:"descripcion" validate:"required,max=1000"`
	Severidad    string `json:"severidad" validate:"required,oneof=BAJA MEDIA ALTA CRITICA"`
	MarcarDanado bool   `json:"marcarDanado"`
	// Pide un equipo de reemplazo al administrador
	SolicitaReemplazo bool `json:"solicitaReemplazo"`
}

func (h *Handler) create(r *http.Request) (any, error) {
	var in createInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var out Averia
	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		a, err := inventario.LockOperable(ctx, tx, in.ActivoID)
		if err != nil {
			return err
		}
		reportado := in.ReportadoPor
		if reportado == nil {
			reportado = a.EmpleadoID
		}
		var id int64
		if err := tx.QueryRow(ctx, `DECLARE @codigo NVARCHAR(20);
			EXEC SGAT.sp_generar_codigo_anual 'AVR', @codigo OUTPUT;
			INSERT INTO averias (codigo, activo_id, reportado_por, descripcion, severidad, usuario_id, solicita_reemplazo)
			VALUES (@codigo, $1, $2, $3, $4, $5, $6);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`, a.ID, reportado, in.Descripcion, in.Severidad, security.UserID(ctx),
			in.SolicitaReemplazo).Scan(&id); err != nil {
			return err
		}
		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		if err := audit.History(ctx, tx, audit.Event{ActivoID: a.ID, Tipo: audit.EvAveria,
			Descripcion: "Avería " + out.Codigo + " (" + in.Severidad + "): " + in.Descripcion,
			Referencia:  "averias", ReferenciaID: &id}); err != nil {
			return err
		}
		if in.MarcarDanado {
			if err := inventario.CambiarEstado(ctx, tx, a, inventario.EstadoDanado, "Avería "+out.Codigo, "averias", &id); err != nil {
				return err
			}
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Crear, Entidad: "averias", EntidadID: &id,
			Descripcion: "Avería " + out.Codigo + " en " + a.Codigo, Despues: out})
	})
	return out, apperr.FromDB(err, "")
}

type updateInput struct {
	ReportadoPor *int64 `json:"reportadoPorId" validate:"omitempty,gt=0"`
	Descripcion  string `json:"descripcion" validate:"required,max=1000"`
	Severidad    string `json:"severidad" validate:"required,oneof=BAJA MEDIA ALTA CRITICA"`
}

func (h *Handler) update(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in updateInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var out Averia
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		before, err := find(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE averias SET reportado_por = $2, descripcion = $3, severidad = $4 WHERE id = $1`,
			id, in.ReportadoPor, in.Descripcion, in.Severidad); err != nil {
			return err
		}
		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "averias", EntidadID: &id,
			Descripcion: "Avería " + out.Codigo, Antes: before, Despues: out})
	})
	return out, apperr.FromDB(err, "Avería no encontrada")
}

type estadoInput struct {
	Estado       string  `json:"estado" validate:"required,oneof=REPORTADA EN_REVISION RESUELTA SIN_SOLUCION"`
	Solucion     *string `json:"solucion" validate:"required_if=Estado RESUELTA,required_if=Estado SIN_SOLUCION,omitempty,max=1000"`
	EstadoActivo *string `json:"estadoActivo" validate:"omitempty,oneof=ACTIVO DANADO DADO_DE_BAJA"`
}

func (h *Handler) cambiarEstado(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in estadoInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	if in.EstadoActivo != nil && *in.EstadoActivo == inventario.EstadoBaja {
		return nil, apperr.BadRequest("Para dar de baja el activo use la opción 'Dar de baja' del inventario")
	}
	ctx := r.Context()
	var out Averia
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		before, err := find(ctx, tx, id)
		if err != nil {
			return err
		}
		cerrada := in.Estado == "RESUELTA" || in.Estado == "SIN_SOLUCION"
		if _, err := tx.Exec(ctx, `UPDATE averias SET estado = $2, solucion = coalesce($3, solucion),
			fecha_solucion = CASE WHEN $4 = 1 THEN SYSDATETIMEOFFSET() ELSE NULL END WHERE id = $1`, id, in.Estado, in.Solucion, cerrada); err != nil {
			return err
		}
		if in.EstadoActivo != nil {
			a, err := inventario.LockOperable(ctx, tx, before.ActivoID)
			if err != nil {
				return err
			}
			if err := inventario.CambiarEstado(ctx, tx, a, *in.EstadoActivo, "Avería "+before.Codigo, "averias", &id); err != nil {
				return err
			}
		}
		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		if err := audit.History(ctx, tx, audit.Event{ActivoID: before.ActivoID, Tipo: audit.EvAveria,
			Descripcion: "Avería " + before.Codigo + " → " + in.Estado, Referencia: "averias", ReferenciaID: &id}); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "averias", EntidadID: &id,
			Descripcion: "Avería " + before.Codigo + " cambia a " + in.Estado, Antes: before, Despues: out})
	})
	return out, apperr.FromDB(err, "")
}
