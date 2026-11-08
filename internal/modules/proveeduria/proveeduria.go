// Package proveeduria gestiona las entregas de materiales de la bodega de proveeduría a las agencias:
// el stock se descuenta al enviar, el encargado de la agencia recibe (y elige a qué empleado se entrega,
// generando el acta) o rechaza (el stock vuelve a la bodega).
package proveeduria

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
	"sgat-api/internal/modules/actas"
	"sgat-api/internal/modules/bodega"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

const (
	EstadoEnTransito = "EN_TRANSITO"
	EstadoRecibido   = "RECIBIDO"
	EstadoRechazado  = "RECHAZADO"
	// EstadoEntregado: entrega directa a una agencia sin encargado (no requiere recepción)
	EstadoEntregado = "ENTREGADO"
)

type Entrega struct {
	ID                     int64      `json:"id" db:"id"`
	Codigo                 string     `json:"codigo" db:"codigo"`
	AgenciaDestinoID       int64      `json:"agenciaDestinoId" db:"agencia_destino_id"`
	AgenciaDestino         string     `json:"agenciaDestino" db:"agencia_destino"`
	DepartamentoID         *int64     `json:"departamentoId" db:"departamento_id"`
	Departamento           *string    `json:"departamento" db:"departamento"`
	EmpleadoID             *int64     `json:"empleadoId" db:"empleado_id"`
	Empleado               *string    `json:"empleado" db:"empleado"`
	Motivo                 string     `json:"motivo" db:"motivo"`
	Fecha                  types.Date `json:"fecha" db:"fecha"`
	Estado                 string     `json:"estado" db:"estado"`
	EnviadoPorID           *int64     `json:"-" db:"enviado_por"`
	EnviadoPor             *string    `json:"enviadoPor" db:"enviado_por_nombre"`
	RecibidoPorID          *int64     `json:"-" db:"recibido_por"`
	RecibidoPor            *string    `json:"recibidoPor" db:"recibido_por_nombre"`
	FechaRecepcion         *time.Time `json:"fechaRecepcion" db:"fecha_recepcion"`
	ObservacionesRecepcion *string    `json:"observacionesRecepcion" db:"observaciones_recepcion"`
	ActaID                 *int64     `json:"actaId" db:"acta_id"`
	ActaNumero             *string    `json:"actaNumero" db:"acta_numero"`
	TotalItems             int64      `json:"totalItems" db:"total_items"`
	TotalUnidades          int64      `json:"totalUnidades" db:"total_unidades"`
	CreatedAt              time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt              time.Time  `json:"updatedAt" db:"updated_at"`
	// Acciones que el usuario actual puede hacer sobre la entrega
	PuedeRecibir  bool   `json:"puedeRecibir" db:"-"`
	PuedeRechazar bool   `json:"puedeRechazar" db:"-"`
	Items         []Item `json:"items,omitempty" db:"-"`
}

// Item es un material de la entrega.
type Item struct {
	RepuestoID    int64   `json:"repuestoId" db:"repuesto_id"`
	Codigo        string  `json:"codigo" db:"codigo"`
	Nombre        string  `json:"nombre" db:"nombre"`
	Marca         *string `json:"marca" db:"marca"`
	Unidad        string  `json:"unidad" db:"unidad"`
	Cantidad      int     `json:"cantidad" db:"cantidad"`
	CostoUnitario float64 `json:"costoUnitario" db:"costo_unitario"`
}

type Handler struct{ db *database.DB }

func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.HandleCreated(h.crear))
	r.Get("/por-recibir/total", httpx.Handle(h.totalPorRecibir))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Post("/{id}/recibir", httpx.Handle(h.recibir))
	r.Post("/{id}/rechazar", httpx.Handle(h.rechazar))
}

const selectSQL = `SELECT e.*, ag.nombre AS agencia_destino, d.nombre AS departamento,
	em.nombres + ' ' + em.apellidos AS empleado,
	ue.nombres + ' ' + ue.apellidos AS enviado_por_nombre, ur.nombres + ' ' + ur.apellidos AS recibido_por_nombre,
	ac.numero AS acta_numero,
	(SELECT count(*) FROM entregas_material_detalle x WHERE x.entrega_id = e.id) AS total_items,
	(SELECT coalesce(sum(x.cantidad), 0) FROM entregas_material_detalle x WHERE x.entrega_id = e.id) AS total_unidades`

const fromSQL = `FROM entregas_material e` + joinsSQL

// fromLockSQL bloquea la fila de la entrega (equivalente a FOR UPDATE OF e).
const fromLockSQL = `FROM entregas_material e WITH (UPDLOCK, ROWLOCK)` + joinsSQL

const joinsSQL = `
	JOIN agencias ag ON ag.id = e.agencia_destino_id
	LEFT JOIN departamentos d ON d.id = e.departamento_id
	LEFT JOIN empleados em ON em.id = e.empleado_id
	LEFT JOIN usuarios ue ON ue.id = e.enviado_por
	LEFT JOIN usuarios ur ON ur.id = e.recibido_por
	LEFT JOIN actas ac ON ac.id = e.acta_id`

// scope: el encargado ve las entregas a su agencia, el jefe las de su departamento; los roles de un área, las
// que llevan materiales de su bodega.
func scope(ctx context.Context, w *database.Where) {
	if ag := security.AgenciaScope(ctx); ag != nil {
		w.Add("e.agencia_destino_id = ?", *ag)
	}
	if dep := security.DepartamentoScope(ctx); dep != nil {
		w.Add("e.departamento_id = ?", *dep)
	}
	if area := security.AreaScope(ctx); area != nil {
		w.Add(`EXISTS (SELECT 1 FROM entregas_material_detalle x JOIN repuestos r ON r.id = x.repuesto_id
			WHERE x.entrega_id = e.id AND r.area = ?)`, *area)
	}
}

// filtroPorRecibir: entregas en tránsito hacia la agencia del encargado (solo el encargado las recibe).
func filtroPorRecibir(ctx context.Context, w *database.Where) {
	w.Add("e.estado = 'EN_TRANSITO'")
	if ag := security.AgenciaScope(ctx); ag != nil {
		w.Add("e.agencia_destino_id = ?", *ag)
		if dep := security.DepartamentoScope(ctx); dep != nil {
			w.Add("e.departamento_id = ?", *dep)
		}
	} else {
		w.Add("1 = 0")
	}
}

func permisos(ctx context.Context, e *Entrega) {
	p, ok := security.PrincipalFrom(ctx)
	if !ok || e.Estado != EstadoEnTransito {
		return
	}
	switch p.Rol {
	case security.RoleSuperAdmin:
		e.PuedeRecibir, e.PuedeRechazar = true, true
	case security.RoleEncargado:
		propia := p.AgenciaID != nil && *p.AgenciaID == e.AgenciaDestinoID
		e.PuedeRecibir, e.PuedeRechazar = propia, propia
	case security.RoleJefe:
		propia := p.AgenciaID != nil && *p.AgenciaID == e.AgenciaDestinoID &&
			p.DepartamentoID != nil && e.DepartamentoID != nil && *p.DepartamentoID == *e.DepartamentoID
		e.PuedeRecibir, e.PuedeRechazar = propia, propia
	case security.RoleProveeduria:
		// Proveeduría puede anular un envío que todavía no se recibe (el stock vuelve a su bodega)
		e.PuedeRechazar = true
	}
}

func (h *Handler) list(r *http.Request) (any, error) {
	ctx := r.Context()
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("e.codigo + ' ' + e.motivo + ' ' + ag.nombre + ' ' + coalesce(em.nombres + ' ' + em.apellidos, '')", lq.Search)
	database.Eq(w, "e.estado", httpx.QueryString(r, "estado"))
	database.Eq(w, "e.agencia_destino_id", httpx.QueryInt64(r, "agenciaId"))
	database.Eq(w, "e.empleado_id", httpx.QueryInt64(r, "empleadoId"))
	if v := httpx.QueryBool(r, "porRecibir"); v != nil && *v {
		filtroPorRecibir(ctx, w)
	}
	scope(ctx, w)
	page, err := database.Paginate[Entrega](ctx, h.db, selectSQL, fromSQL, w,
		lq.OrderBy(map[string]string{"codigo": "e.codigo", "fecha": "e.fecha", "estado": "e.estado"},
			"CASE WHEN e.estado = 'EN_TRANSITO' THEN 1 ELSE 0 END DESC, e.id DESC"), lq)
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		permisos(ctx, &page.Items[i])
	}
	return page, nil
}

// totalPorRecibir alimenta el contador del menú del encargado.
func (h *Handler) totalPorRecibir(r *http.Request) (any, error) {
	w := &database.Where{}
	filtroPorRecibir(r.Context(), w)
	var total int64
	err := h.db.QueryRow(r.Context(), "SELECT count(*) FROM entregas_material e"+w.SQL(), w.Args()...).Scan(&total)
	return map[string]int64{"total": total}, err
}

func find(ctx context.Context, q database.Querier, id int64, forUpdate bool) (Entrega, error) {
	w := (&database.Where{}).Add("e.id = ?", id)
	scope(ctx, w)
	from := fromSQL
	if forUpdate {
		from = fromLockSQL
	}
	sql := selectSQL + " " + from + w.SQL()
	e, err := database.Get[Entrega](ctx, q, sql, w.Args()...)
	if err != nil {
		return e, apperr.FromDB(err, "Entrega no encontrada")
	}
	permisos(ctx, &e)
	return e, nil
}

func items(ctx context.Context, q database.Querier, entregaID int64) ([]Item, error) {
	return database.List[Item](ctx, q, `
		SELECT x.repuesto_id, r.codigo, r.nombre, m.nombre AS marca, r.unidad, x.cantidad, x.costo_unitario
		FROM entregas_material_detalle x
		JOIN repuestos r ON r.id = x.repuesto_id
		LEFT JOIN marcas m ON m.id = r.marca_id
		WHERE x.entrega_id = $1 ORDER BY r.nombre`, entregaID)
}

func (h *Handler) get(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	e, err := find(r.Context(), h.db, id, false)
	if err != nil {
		return nil, err
	}
	if e.Items, err = items(r.Context(), h.db, id); err != nil {
		return nil, apperr.Internal(err)
	}
	return e, nil
}

// ---- Crear (enviar) ----

type itemInput struct {
	RepuestoID int64 `json:"repuestoId" validate:"required,gt=0"`
	Cantidad   int   `json:"cantidad" validate:"required,gt=0,max=100000"`
}

type crearInput struct {
	AgenciaDestinoID int64       `json:"agenciaDestinoId" validate:"required,gt=0"`
	// Departamento destino (opcional): si tiene jefe, él recibe la entrega
	DepartamentoID *int64        `json:"departamentoId" validate:"omitempty,gt=0"`
	EmpleadoID       *int64      `json:"empleadoId" validate:"omitempty,gt=0"`
	Motivo           string      `json:"motivo" validate:"required,max=300"`
	Fecha            *types.Date `json:"fecha"`
	Items            []itemInput `json:"items" validate:"required,min=1,max=100,dive"`
}

type destinatario struct {
	ID             int64
	Nombre         string
	AgenciaID      int64
	DepartamentoID int64
}

// buscarDestinatario valida que el empleado esté habilitado y pertenezca a la agencia destino.
func buscarDestinatario(ctx context.Context, q database.Querier, empleadoID, agenciaID int64) (destinatario, error) {
	var d destinatario
	var habilitado bool
	err := q.QueryRow(ctx, `SELECT id, nombres + ' ' + apellidos, agencia_id, departamento_id, habilitado
		FROM empleados WHERE id = $1`, empleadoID).Scan(&d.ID, &d.Nombre, &d.AgenciaID, &d.DepartamentoID, &habilitado)
	if err != nil {
		return d, apperr.FromDB(err, "Empleado no encontrado")
	}
	if !habilitado {
		return d, apperr.Validation(map[string]string{"empleadoId": "El empleado " + d.Nombre + " está deshabilitado"})
	}
	if d.AgenciaID != agenciaID {
		return d, apperr.Validation(map[string]string{"empleadoId": "El empleado " + d.Nombre + " no pertenece a la agencia destino"})
	}
	return d, nil
}

func (h *Handler) crear(r *http.Request) (any, error) {
	var in crearInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	fecha := types.Today()
	if in.Fecha != nil && !in.Fecha.IsZero() {
		fecha = *in.Fecha
	}
	// Une cantidades si el mismo material viene repetido
	cantidades := map[int64]int{}
	var orden []int64
	for _, it := range in.Items {
		if _, ok := cantidades[it.RepuestoID]; !ok {
			orden = append(orden, it.RepuestoID)
		}
		cantidades[it.RepuestoID] += it.Cantidad
	}

	var id int64
	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		var agencia string
		var habilitada bool
		if err := tx.QueryRow(ctx, `SELECT nombre, habilitado FROM agencias WHERE id = $1`, in.AgenciaDestinoID).
			Scan(&agencia, &habilitada); err != nil {
			return apperr.FromDB(err, "Agencia no encontrada")
		}
		if !habilitada {
			return apperr.Validation(map[string]string{"agenciaDestinoId": "La agencia " + agencia + " está deshabilitada"})
		}
		depID := in.DepartamentoID
		if in.EmpleadoID != nil {
			d, err := buscarDestinatario(ctx, tx, *in.EmpleadoID, in.AgenciaDestinoID)
			if err != nil {
				return err
			}
			depID = &d.DepartamentoID
		}
		// Recibe el encargado de la agencia o, si va a un departamento, su jefe. Si no hay quien reciba,
		// proveeduría entrega directamente
		var conResponsable bool
		if err := tx.QueryRow(ctx, `SELECT CAST(CASE WHEN EXISTS (SELECT 1 FROM usuarios x JOIN roles xr ON xr.id = x.rol_id
			WHERE x.habilitado = 1 AND xr.habilitado = 1 AND x.agencia_id = $1
			AND (xr.perfil = 'ENCARGADO' OR (xr.perfil = 'JEFE' AND x.departamento_id = CAST($2 AS BIGINT)))) THEN 1 ELSE 0 END AS BIT)`, in.AgenciaDestinoID, depID).
			Scan(&conResponsable); err != nil {
			return err
		}
		estado := EstadoEnTransito
		if !conResponsable {
			estado = EstadoEntregado
		}
		var codigo string
		if err := tx.QueryRow(ctx, `DECLARE @codigo NVARCHAR(20);
			EXEC SGAT.sp_generar_codigo_anual 'PRV', @codigo OUTPUT;
			INSERT INTO entregas_material (codigo, agencia_destino_id, departamento_id, empleado_id, motivo, fecha,
			estado, enviado_por, recibido_por, fecha_recepcion)
			VALUES (@codigo, $1, $2, $3, $4, $5, CAST($6 AS NVARCHAR(12)), $7, CASE WHEN CAST($6 AS NVARCHAR(12)) = 'ENTREGADO' THEN CAST($7 AS BIGINT) END,
			        CASE WHEN CAST($6 AS NVARCHAR(12)) = 'ENTREGADO' THEN SYSDATETIMEOFFSET() END);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT), @codigo`,
			in.AgenciaDestinoID, depID, in.EmpleadoID, in.Motivo, fecha, estado, security.UserID(ctx)).Scan(&id, &codigo); err != nil {
			return err
		}

		materiales := make([]actas.MaterialCantidad, 0, len(orden))
		for _, rid := range orden {
			rep, err := bodega.VerificarRepuesto(ctx, tx, rid)
			if err != nil {
				return err
			}
			if !rep.Habilitado {
				return apperr.Validation(map[string]string{"items": "El material " + rep.Nombre + " está deshabilitado"})
			}
			// El trigger del kardex valida el stock y lo descuenta
			_, costo, err := bodega.RegistrarMovimiento(ctx, tx, bodega.NuevoMovimiento{RepuestoID: rid, Tipo: "SALIDA",
				Cantidad: cantidades[rid], Motivo: "Entrega " + codigo + " a " + agencia, Documento: &codigo})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO entregas_material_detalle (entrega_id, repuesto_id, cantidad, costo_unitario)
				VALUES ($1, $2, $3, $4)`, id, rid, cantidades[rid], costo); err != nil {
				return err
			}
			materiales = append(materiales, actas.MaterialCantidad{RepuestoID: rid, Cantidad: cantidades[rid]})
		}

		if estado == EstadoEntregado {
			actaID, _, err := actas.Crear(ctx, tx, actas.Nueva{Tipo: actas.TipoEntrega, Fecha: fecha, EmpleadoID: in.EmpleadoID,
				AgenciaID: in.AgenciaDestinoID, DepartamentoID: depID, Observaciones: "Entrega de materiales " + codigo + ". " + in.Motivo,
				Materiales: materiales})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE entregas_material SET acta_id = $2 WHERE id = $1`, id, actaID); err != nil {
				return err
			}
		}
		desc := fmt.Sprintf("Entrega de materiales %s a %s", codigo, agencia)
		if estado == EstadoEnTransito {
			desc += " (pendiente de recepción)"
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Enviar, Entidad: "entregas_material", EntidadID: &id, Descripcion: desc})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	e, err := find(ctx, h.db, id, false)
	if err != nil {
		return nil, err
	}
	e.Items, err = items(ctx, h.db, id)
	return e, err
}

// ---- Recibir / rechazar ----

type recibirInput struct {
	// Empleado al que se entrega el material (si no se indicó al enviar); vacío = uso de la agencia
	EmpleadoID    *int64 `json:"empleadoId" validate:"omitempty,gt=0"`
	Observaciones string `json:"observaciones" validate:"max=500"`
}

func (h *Handler) recibir(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in recibirInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var actaID int64
	var actaNumero string
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		e, err := find(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if !e.PuedeRecibir {
			if e.Estado != EstadoEnTransito {
				return apperr.Conflict("La entrega " + e.Codigo + " ya fue procesada")
			}
			return apperr.Forbidden("Solo el encargado de " + e.AgenciaDestino + " puede recibir esta entrega")
		}
		empleadoID := e.EmpleadoID
		if in.EmpleadoID != nil {
			empleadoID = in.EmpleadoID
		}
		depID := e.DepartamentoID
		if empleadoID != nil {
			d, err := buscarDestinatario(ctx, tx, *empleadoID, e.AgenciaDestinoID)
			if err != nil {
				return err
			}
			// El jefe entrega solo a los empleados de su departamento
			if dep := security.DepartamentoScope(ctx); dep != nil && d.DepartamentoID != *dep {
				return apperr.Validation(map[string]string{"empleadoId": "El empleado " + d.Nombre + " no pertenece a su departamento"})
			}
			depID = &d.DepartamentoID
		}
		its, err := items(ctx, tx, id)
		if err != nil {
			return err
		}
		materiales := make([]actas.MaterialCantidad, len(its))
		for i, it := range its {
			materiales[i] = actas.MaterialCantidad{RepuestoID: it.RepuestoID, Cantidad: it.Cantidad}
		}
		obs := "Entrega de materiales " + e.Codigo + ". " + e.Motivo
		if in.Observaciones != "" {
			obs += ". Recepción: " + in.Observaciones
		}
		// Acta de entrega del encargado al empleado (o a la agencia), con los materiales recibidos
		actaID, actaNumero, err = actas.Crear(ctx, tx, actas.Nueva{Tipo: actas.TipoEntrega, EmpleadoID: empleadoID,
			AgenciaID: e.AgenciaDestinoID, DepartamentoID: depID, Observaciones: obs, Materiales: materiales})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE entregas_material SET estado = 'RECIBIDO', recibido_por = $2, fecha_recepcion = SYSDATETIMEOFFSET(),
			observaciones_recepcion = NULLIF($3, ''), empleado_id = $4, departamento_id = $5, acta_id = $6 WHERE id = $1`,
			id, security.UserID(ctx), in.Observaciones, empleadoID, depID, actaID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Recibir, Entidad: "entregas_material", EntidadID: &id,
			Descripcion: "Recepción de la entrega de materiales " + e.Codigo + " en " + e.AgenciaDestino})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return map[string]any{"actaId": actaID, "actaNumero": actaNumero}, nil
}

type rechazarInput struct {
	Motivo string `json:"motivo" validate:"required,max=500"`
}

func (h *Handler) rechazar(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in rechazarInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		e, err := find(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if !e.PuedeRechazar {
			if e.Estado != EstadoEnTransito {
				return apperr.Conflict("La entrega " + e.Codigo + " ya fue procesada")
			}
			return apperr.Forbidden("No puede rechazar esta entrega")
		}
		its, err := items(ctx, tx, id)
		if err != nil {
			return err
		}
		// El material vuelve a la bodega de proveeduría
		for _, it := range its {
			if _, _, err := bodega.RegistrarMovimiento(ctx, tx, bodega.NuevoMovimiento{RepuestoID: it.RepuestoID, Tipo: "ENTRADA",
				Cantidad: it.Cantidad, Motivo: "Devolución por rechazo de " + e.Codigo + ": " + in.Motivo, Documento: &e.Codigo}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE entregas_material SET estado = 'RECHAZADO', recibido_por = $2, fecha_recepcion = SYSDATETIMEOFFSET(),
			observaciones_recepcion = $3 WHERE id = $1`, id, security.UserID(ctx), in.Motivo); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Rechazar, Entidad: "entregas_material", EntidadID: &id,
			Descripcion: "Rechazo de la entrega de materiales " + e.Codigo + ": " + in.Motivo})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return httpx.MessageResult("Entrega rechazada; el material volvió a la bodega"), nil
}
