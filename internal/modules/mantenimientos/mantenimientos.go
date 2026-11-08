// Package mantenimientos gestiona el ciclo de vida de los mantenimientos
// preventivos y correctivos: programar → iniciar → finalizar / cancelar.
package mantenimientos

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
	"sgat-api/internal/modules/inventario"
	"sgat-api/internal/modules/reemplazos"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

const (
	Programado = "PROGRAMADO"
	EnProceso  = "EN_PROCESO"
	Finalizado = "FINALIZADO"
	Cancelado  = "CANCELADO"
)

type Mantenimiento struct {
	ID                int64                  `json:"id" db:"id"`
	Codigo            string                 `json:"codigo" db:"codigo"`
	ActivoID          int64                  `json:"activoId" db:"activo_id"`
	ActivoCodigo      string                 `json:"activoCodigo" db:"activo_codigo"`
	Categoria         string                 `json:"categoria" db:"categoria"`
	ActivoDescripcion string                 `json:"activoDescripcion" db:"activo_descripcion"`
	AgenciaID         int64                  `json:"agenciaId" db:"agencia_id"`
	Agencia           string                 `json:"agencia" db:"agencia"`
	AveriaID          *int64                 `json:"averiaId" db:"averia_id"`
	AveriaCodigo      *string                `json:"averiaCodigo" db:"averia_codigo"`
	TecnicoID         int64                  `json:"tecnicoId" db:"tecnico_id"`
	Tecnico           string                 `json:"tecnico" db:"tecnico"`
	TecnicoTipo       string                 `json:"tecnicoTipo" db:"tecnico_tipo"`
	TecnicoEmpresa    *string                `json:"tecnicoEmpresa" db:"tecnico_empresa"`
	Tipo              string                 `json:"tipo" db:"tipo"`
	Estado            string                 `json:"estado" db:"estado"`
	FechaProgramada   types.Date             `json:"fechaProgramada" db:"fecha_programada"`
	FechaInicio       *types.Date            `json:"fechaInicio" db:"fecha_inicio"`
	FechaFin          *types.Date            `json:"fechaFin" db:"fecha_fin"`
	Diagnostico       *string                `json:"diagnostico" db:"diagnostico"`
	TrabajoRealizado  *string                `json:"trabajoRealizado" db:"trabajo_realizado"`
	Costo             float64                `json:"costo" db:"costo"`
	ProximaFecha      *types.Date            `json:"proximaFecha" db:"proxima_fecha"`
	Observaciones     *string                `json:"observaciones" db:"observaciones"`
	Vencido           bool                   `json:"vencido" db:"vencido"`
	TotalReemplazos   int64                  `json:"totalReemplazos" db:"total_reemplazos"`
	CreatedAt         time.Time              `json:"createdAt" db:"created_at"`
	UpdatedAt         time.Time              `json:"updatedAt" db:"updated_at"`
	Reemplazos        []reemplazos.Reemplazo `json:"reemplazos,omitempty" db:"-"`
}

type Handler struct{ db *database.DB }

func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.HandleCreated(h.create))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Put("/{id}", httpx.Handle(h.update))
	r.Post("/{id}/iniciar", httpx.Handle(h.iniciar))
	r.Post("/{id}/finalizar", httpx.Handle(h.finalizar))
	r.Post("/{id}/cancelar", httpx.Handle(h.cancelar))
}

var sortable = map[string]string{
	"codigo": "v.codigo", "fechaProgramada": "v.fecha_programada", "fechaFin": "v.fecha_fin", "estado": "v.estado",
	"tipo": "v.tipo", "activoCodigo": "v.activo_codigo", "tecnico": "v.tecnico", "costo": "v.costo", "agencia": "v.agencia",
}

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("v.codigo + ' ' + v.activo_codigo + ' ' + v.activo_descripcion + ' ' + v.tecnico", lq.Search)
	database.Eq(w, "v.estado", httpx.QueryString(r, "estado"))
	database.Eq(w, "v.tipo", httpx.QueryString(r, "tipo"))
	database.Eq(w, "v.tecnico_id", httpx.QueryInt64(r, "tecnicoId"))
	database.Eq(w, "v.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	database.Eq(w, "v.activo_id", httpx.QueryInt64(r, "activoId"))
	database.Eq(w, "v.vencido", httpx.QueryBool(r, "vencido"))
	if v := httpx.QueryString(r, "desde"); v != nil {
		w.Add("coalesce(v.fecha_fin, v.fecha_programada) >= CAST(? AS DATE)", *v)
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		w.Add("coalesce(v.fecha_fin, v.fecha_programada) <= CAST(? AS DATE)", *v)
	}
	return database.Paginate[Mantenimiento](r.Context(), h.db, "SELECT v.*", "FROM v_mantenimientos v", w,
		lq.OrderBy(sortable, "v.id DESC"), lq)
}

func find(ctx context.Context, q database.Querier, id int64) (Mantenimiento, error) {
	m, err := database.Get[Mantenimiento](ctx, q, "SELECT * FROM v_mantenimientos WHERE id = $1", id)
	if err != nil {
		return m, apperr.FromDB(err, "Mantenimiento no encontrado")
	}
	m.Reemplazos, err = database.List[reemplazos.Reemplazo](ctx, q,
		reemplazos.SelectSQL+" "+reemplazos.FromSQL+" WHERE r.mantenimiento_id = $1 ORDER BY r.id", id)
	return m, err
}

func (h *Handler) get(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return find(r.Context(), h.db, id)
}

// lockMantenimiento bloquea el mantenimiento y devuelve su estado y activo.
func lockMantenimiento(ctx context.Context, tx database.Tx, id int64) (estado string, activoID int64, codigo string, averiaID *int64, err error) {
	err = tx.QueryRow(ctx, `SELECT estado, activo_id, codigo, averia_id FROM mantenimientos WITH (UPDLOCK, ROWLOCK) WHERE id = $1`, id).
		Scan(&estado, &activoID, &codigo, &averiaID)
	err = apperr.FromDB(err, "Mantenimiento no encontrado")
	return
}

// ---- Crear / editar ----

type input struct {
	ActivoID        int64      `json:"activoId" validate:"required,gt=0"`
	TecnicoID       int64      `json:"tecnicoId" validate:"required,gt=0"`
	AveriaID        *int64     `json:"averiaId" validate:"omitempty,gt=0"`
	Tipo            string     `json:"tipo" validate:"required,oneof=PREVENTIVO CORRECTIVO"`
	FechaProgramada types.Date `json:"fechaProgramada" validate:"required"`
	Diagnostico     *string    `json:"diagnostico" validate:"omitempty,max=1000"`
	Observaciones   *string    `json:"observaciones" validate:"omitempty,max=1000"`
	IniciarAhora    bool       `json:"iniciarAhora"`
}

func (h *Handler) create(r *http.Request) (any, error) {
	var in input
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var out Mantenimiento
	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		a, err := inventario.LockOperable(ctx, tx, in.ActivoID)
		if err != nil {
			return err
		}
		estado, inicio := Programado, (*types.Date)(nil)
		if in.IniciarAhora {
			hoy := types.Today()
			estado, inicio = EnProceso, &hoy
		}
		var id int64
		if err := tx.QueryRow(ctx, `DECLARE @codigo NVARCHAR(20);
			EXEC SGAT.sp_generar_codigo_anual 'MNT', @codigo OUTPUT;
			INSERT INTO mantenimientos (codigo, activo_id, averia_id, tecnico_id, tipo, estado, fecha_programada,
			fecha_inicio, diagnostico, observaciones, usuario_id) VALUES (@codigo, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			a.ID, in.AveriaID, in.TecnicoID, in.Tipo, estado, in.FechaProgramada, inicio, in.Diagnostico, in.Observaciones,
			security.UserID(ctx)).Scan(&id); err != nil {
			return err
		}
		if in.AveriaID != nil {
			if _, err := tx.Exec(ctx, `UPDATE averias SET estado = 'EN_REVISION' WHERE id = $1 AND estado = 'REPORTADA'`,
				*in.AveriaID); err != nil {
				return err
			}
		}
		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		desc := fmt.Sprintf("Mantenimiento %s %s programado para %s con %s", out.Codigo, out.Tipo, out.FechaProgramada, out.Tecnico)
		if err := audit.History(ctx, tx, audit.Event{ActivoID: a.ID, Tipo: audit.EvMantenimiento, Descripcion: desc,
			Referencia: "mantenimientos", ReferenciaID: &id}); err != nil {
			return err
		}
		if estado == EnProceso {
			if err := inventario.CambiarEstado(ctx, tx, a, inventario.EstadoEnMantenimiento, "Inicio de "+out.Codigo,
				"mantenimientos", &id); err != nil {
				return err
			}
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Crear, Entidad: "mantenimientos", EntidadID: &id,
			Descripcion: desc, Despues: out})
	})
	return out, apperr.FromDB(err, "")
}

func (h *Handler) update(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in input
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var out Mantenimiento
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		estado, activoID, _, _, err := lockMantenimiento(ctx, tx, id)
		if err != nil {
			return err
		}
		if estado == Finalizado || estado == Cancelado {
			return apperr.Conflict("No se puede editar un mantenimiento finalizado o cancelado")
		}
		if activoID != in.ActivoID && estado != Programado {
			return apperr.Conflict("No se puede cambiar el activo de un mantenimiento en proceso")
		}
		before, err := find(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE mantenimientos SET activo_id = $2, averia_id = $3, tecnico_id = $4, tipo = $5,
			fecha_programada = $6, diagnostico = $7, observaciones = $8 WHERE id = $1`,
			id, in.ActivoID, in.AveriaID, in.TecnicoID, in.Tipo, in.FechaProgramada, in.Diagnostico, in.Observaciones); err != nil {
			return err
		}
		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "mantenimientos", EntidadID: &id,
			Descripcion: "Mantenimiento " + out.Codigo, Antes: before, Despues: out})
	})
	return out, apperr.FromDB(err, "Mantenimiento no encontrado")
}

// ---- Transiciones ----

type iniciarInput struct {
	FechaInicio *types.Date `json:"fechaInicio"`
	Diagnostico *string     `json:"diagnostico" validate:"omitempty,max=1000"`
}

func (h *Handler) iniciar(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in iniciarInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	fecha := types.Today()
	if in.FechaInicio != nil {
		fecha = *in.FechaInicio
	}
	ctx := r.Context()
	var out Mantenimiento
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		estado, activoID, codigo, _, err := lockMantenimiento(ctx, tx, id)
		if err != nil {
			return err
		}
		if estado != Programado {
			return apperr.Conflict("Solo se pueden iniciar mantenimientos programados")
		}
		a, err := inventario.LockOperable(ctx, tx, activoID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE mantenimientos SET estado = 'EN_PROCESO', fecha_inicio = $2,
			diagnostico = coalesce($3, diagnostico) WHERE id = $1`, id, fecha, in.Diagnostico); err != nil {
			return err
		}
		if err := inventario.CambiarEstado(ctx, tx, a, inventario.EstadoEnMantenimiento, "Inicio de "+codigo,
			"mantenimientos", &id); err != nil {
			return err
		}
		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "mantenimientos", EntidadID: &id,
			Descripcion: "Inicio del mantenimiento " + codigo})
	})
	return out, apperr.FromDB(err, "")
}

type finalizarInput struct {
	FechaFin           types.Date        `json:"fechaFin" validate:"required"`
	Diagnostico        *string           `json:"diagnostico" validate:"omitempty,max=1000"`
	TrabajoRealizado   string            `json:"trabajoRealizado" validate:"required,max=2000"`
	Costo              float64           `json:"costo" validate:"gte=0"`
	ProximaFecha       *types.Date       `json:"proximaFecha"`
	ProgramarSiguiente bool              `json:"programarSiguiente"`
	EstadoActivo       string            `json:"estadoActivo" validate:"required,oneof=ACTIVO DANADO"`
	Condicion          *string           `json:"condicion" validate:"omitempty,oneof=NUEVO BUENO REGULAR MALO"`
	Observaciones      *string           `json:"observaciones" validate:"omitempty,max=1000"`
	Reemplazos         []reemplazos.Item `json:"reemplazos" validate:"max=30,dive"`
}

func (h *Handler) finalizar(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in finalizarInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	if in.ProximaFecha != nil && !in.ProximaFecha.After(in.FechaFin.Time) {
		return nil, apperr.Validation(map[string]string{"proximaFecha": "Debe ser posterior a la fecha de finalización"})
	}
	ctx := r.Context()
	var out Mantenimiento
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		estado, activoID, codigo, averiaID, err := lockMantenimiento(ctx, tx, id)
		if err != nil {
			return err
		}
		if estado == Finalizado || estado == Cancelado {
			return apperr.Conflict("El mantenimiento ya fue finalizado o cancelado")
		}
		a, err := inventario.LockOperable(ctx, tx, activoID)
		if err != nil {
			return err
		}

		costoReemplazos := 0.0
		for _, it := range in.Reemplazos {
			rid, err := reemplazos.Registrar(ctx, tx, a, &id, in.FechaFin, it)
			if err != nil {
				return err
			}
			var c float64
			if err := tx.QueryRow(ctx, `SELECT costo FROM reemplazos WHERE id = $1`, rid).Scan(&c); err != nil {
				return err
			}
			costoReemplazos += c
		}

		if _, err := tx.Exec(ctx, `UPDATE mantenimientos SET estado = 'FINALIZADO', fecha_inicio = coalesce(fecha_inicio, $2),
			fecha_fin = $2, diagnostico = coalesce($3, diagnostico), trabajo_realizado = $4, costo = $5,
			proxima_fecha = $6, observaciones = coalesce($7, observaciones) WHERE id = $1`,
			id, in.FechaFin, in.Diagnostico, in.TrabajoRealizado, in.Costo+costoReemplazos, in.ProximaFecha, in.Observaciones); err != nil {
			return err
		}
		if in.Condicion != nil {
			if _, err := tx.Exec(ctx, `UPDATE activos SET condicion = $2 WHERE id = $1`, a.ID, *in.Condicion); err != nil {
				return err
			}
		}
		if err := inventario.CambiarEstado(ctx, tx, a, in.EstadoActivo, "Finalización de "+codigo, "mantenimientos", &id); err != nil {
			return err
		}
		if averiaID != nil && in.EstadoActivo == inventario.EstadoActivo {
			if _, err := tx.Exec(ctx, `UPDATE averias SET estado = 'RESUELTA', fecha_solucion = SYSDATETIMEOFFSET(),
				solucion = coalesce(solucion, $2) WHERE id = $1 AND estado IN ('REPORTADA', 'EN_REVISION')`,
				*averiaID, in.TrabajoRealizado); err != nil {
				return err
			}
		}
		if err := audit.History(ctx, tx, audit.Event{ActivoID: a.ID, Tipo: audit.EvMantenimiento,
			Descripcion: "Mantenimiento " + codigo + " finalizado: " + in.TrabajoRealizado,
			Referencia:  "mantenimientos", ReferenciaID: &id,
			Datos: map[string]any{"costo": in.Costo + costoReemplazos, "reemplazos": len(in.Reemplazos)}}); err != nil {
			return err
		}

		// Programa automáticamente el siguiente mantenimiento preventivo
		if in.ProgramarSiguiente && in.ProximaFecha != nil {
			if _, err := tx.Exec(ctx, `DECLARE @codigo NVARCHAR(20);
				EXEC SGAT.sp_generar_codigo_anual 'MNT', @codigo OUTPUT;
				INSERT INTO mantenimientos (codigo, activo_id, tecnico_id, tipo, estado, fecha_programada,
				observaciones, usuario_id)
				SELECT @codigo, activo_id, tecnico_id, 'PREVENTIVO', 'PROGRAMADO', $2, N'Programado automáticamente desde ' + codigo, $3
				FROM mantenimientos WHERE id = $1`, id, *in.ProximaFecha, security.UserID(ctx)); err != nil {
				return err
			}
		}

		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "mantenimientos", EntidadID: &id,
			Descripcion: "Finalización del mantenimiento " + codigo, Despues: out})
	})
	return out, apperr.FromDB(err, "")
}

type cancelarInput struct {
	Motivo string `json:"motivo" validate:"required,max=500"`
}

func (h *Handler) cancelar(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in cancelarInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var out Mantenimiento
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		estado, activoID, codigo, _, err := lockMantenimiento(ctx, tx, id)
		if err != nil {
			return err
		}
		if estado == Finalizado || estado == Cancelado {
			return apperr.Conflict("El mantenimiento ya fue finalizado o cancelado")
		}
		if _, err := tx.Exec(ctx, `UPDATE mantenimientos SET estado = 'CANCELADO',
			observaciones = trim(coalesce(observaciones + CHAR(10), '') + 'Cancelado: ' + $2) WHERE id = $1`, id, in.Motivo); err != nil {
			return err
		}
		// Si estaba en proceso, el activo vuelve a estar activo
		if estado == EnProceso {
			a, err := inventario.Lock(ctx, tx, activoID)
			if err != nil {
				return err
			}
			if a.Estado == inventario.EstadoEnMantenimiento {
				if err := inventario.CambiarEstado(ctx, tx, a, inventario.EstadoActivo, "Cancelación de "+codigo,
					"mantenimientos", &id); err != nil {
					return err
				}
			}
		}
		if err := audit.History(ctx, tx, audit.Event{ActivoID: activoID, Tipo: audit.EvMantenimiento,
			Descripcion: "Mantenimiento " + codigo + " cancelado: " + in.Motivo, Referencia: "mantenimientos", ReferenciaID: &id}); err != nil {
			return err
		}
		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "mantenimientos", EntidadID: &id,
			Descripcion: "Cancelación del mantenimiento " + codigo + ": " + in.Motivo})
	})
	return out, apperr.FromDB(err, "")
}
