package movimientos

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
	"sgat-api/internal/modules/inventario"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

// Un ENVÍO agrupa los activos que pasan de una agencia a otra. Los activos quedan EN_TRANSITO
// (en la agencia de origen y bloqueados) hasta que en el destino se RECIBEN o se RECHAZAN.
//
// Quién recibe:
//   - ENCARGADO: los envíos que llegan a su agencia.
//   - ADMIN: los envíos a agencias sin encargado habilitado (matriz, bodega...).
//   - SUPERADMIN: cualquiera (para destrabar casos excepcionales).

const (
	EnvioEnTransito = "EN_TRANSITO"
	EnvioRecibido   = "RECIBIDO"
	EnvioRechazado  = "RECHAZADO"
)

func (h *Handler) EnvioRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listEnvios))
	r.Get("/por-recibir/total", httpx.Handle(h.totalPorRecibir))
	r.Get("/{id}", httpx.Handle(h.getEnvio))
	r.Post("/{id}/recibir", httpx.Handle(h.recibir))
	r.Post("/{id}/rechazar", httpx.Handle(h.rechazar))
}

type EnvioRef struct {
	ID     int64  `json:"id"`
	Codigo string `json:"codigo"`
}

type Envio struct {
	ID                     int64         `json:"id" db:"id"`
	Codigo                 string        `json:"codigo" db:"codigo"`
	AgenciaOrigenID        int64         `json:"agenciaOrigenId" db:"agencia_origen_id"`
	AgenciaOrigen          string        `json:"agenciaOrigen" db:"agencia_origen"`
	AgenciaDestinoID       int64         `json:"agenciaDestinoId" db:"agencia_destino_id"`
	AgenciaDestino         string        `json:"agenciaDestino" db:"agencia_destino"`
	DepartamentoDestinoID  *int64        `json:"departamentoDestinoId" db:"departamento_destino_id"`
	DepartamentoDestino    *string       `json:"departamentoDestino" db:"departamento_destino"`
	EmpleadoDestinoID      *int64        `json:"empleadoDestinoId" db:"empleado_destino_id"`
	EmpleadoDestino        *string       `json:"empleadoDestino" db:"empleado_destino"`
	Motivo                 string        `json:"motivo" db:"motivo"`
	FechaEnvio             types.Date    `json:"fechaEnvio" db:"fecha_envio"`
	Estado                 string        `json:"estado" db:"estado"`
	EnviadoPorID           *int64        `json:"-" db:"enviado_por"`
	EnviadoPor             *string       `json:"enviadoPor" db:"enviado_por_nombre"`
	RecibidoPorID          *int64        `json:"-" db:"recibido_por"`
	RecibidoPor            *string       `json:"recibidoPor" db:"recibido_por_nombre"`
	FechaRecepcion         *time.Time    `json:"fechaRecepcion" db:"fecha_recepcion"`
	ObservacionesRecepcion *string       `json:"observacionesRecepcion" db:"observaciones_recepcion"`
	TotalActivos           int64         `json:"totalActivos" db:"total_activos"`
	ActaID                 *int64        `json:"actaId" db:"acta_id"`
	ActaNumero             *string       `json:"actaNumero" db:"acta_numero"`
	DestinoConEncargado    bool          `json:"-" db:"destino_con_encargado"`
	CreatedAt              time.Time     `json:"createdAt" db:"created_at"`
	UpdatedAt              time.Time     `json:"-" db:"updated_at"`
	PuedeRecibir           bool          `json:"puedeRecibir" db:"-"`
	Activos                []EnvioActivo `json:"activos,omitempty" db:"-"`
}

type EnvioActivo struct {
	ActivoID    int64   `json:"activoId" db:"activo_id"`
	Codigo      string  `json:"codigo" db:"codigo"`
	Categoria   string  `json:"categoria" db:"categoria"`
	Icono       *string `json:"categoriaIcono" db:"categoria_icono"`
	Marca       *string `json:"marca" db:"marca"`
	Modelo      *string `json:"modelo" db:"modelo"`
	NumeroSerie *string `json:"numeroSerie" db:"numero_serie"`
	Estado      string  `json:"estado" db:"estado"`
	Condicion   string  `json:"condicion" db:"condicion"`
}

const envioSelect = `SELECT e.*, ao.nombre AS agencia_origen, ad.nombre AS agencia_destino, d.nombre AS departamento_destino,
	em.nombres + ' ' + em.apellidos AS empleado_destino,
	ue.nombres + ' ' + ue.apellidos AS enviado_por_nombre, ur.nombres + ' ' + ur.apellidos AS recibido_por_nombre,
	(SELECT count(*) FROM traslados t WHERE t.envio_id = e.id) AS total_activos,
	ac.id AS acta_id, ac.numero AS acta_numero,
	CAST(CASE WHEN ` + responsableDestinoSQL + ` THEN 1 ELSE 0 END AS BIT) AS destino_con_encargado`

// responsableDestinoSQL: el destino tiene quien reciba: el encargado de la agencia o el jefe del departamento destino.
const responsableDestinoSQL = `EXISTS (SELECT 1 FROM usuarios x JOIN roles xr ON xr.id = x.rol_id
	  WHERE x.habilitado = 1 AND xr.habilitado = 1 AND x.agencia_id = e.agencia_destino_id
	  AND (xr.perfil = 'ENCARGADO' OR (xr.perfil = 'JEFE' AND x.departamento_id = e.departamento_destino_id)))`

const envioFrom = `FROM envios e` + envioJoins

// envioFromLock bloquea la fila del envío (equivalente a FOR UPDATE OF e).
const envioFromLock = `FROM envios e WITH (UPDLOCK, ROWLOCK)` + envioJoins

const envioJoins = `
	JOIN agencias ao ON ao.id = e.agencia_origen_id
	JOIN agencias ad ON ad.id = e.agencia_destino_id
	LEFT JOIN departamentos d ON d.id = e.departamento_destino_id
	LEFT JOIN empleados em ON em.id = e.empleado_destino_id
	LEFT JOIN usuarios ue ON ue.id = e.enviado_por
	LEFT JOIN usuarios ur ON ur.id = e.recibido_por
	LEFT JOIN actas ac ON ac.envio_id = e.id`

// sinEncargadoSQL: el destino no tiene encargado ni jefe que reciba (lo recibe un ADMIN o proveeduría).
const sinEncargadoSQL = `NOT ` + responsableDestinoSQL

func puedeRecibir(ctx context.Context, e Envio) bool {
	p, ok := security.PrincipalFrom(ctx)
	if !ok || e.Estado != EnvioEnTransito {
		return false
	}
	switch p.Rol {
	case security.RoleSuperAdmin:
		return true
	case security.RoleAdmin, security.RoleProveeduria:
		// Si el destino no tiene encargado, confirma la recepción el responsable del área (sus envíos)
		return !e.DestinoConEncargado
	case security.RoleEncargado:
		return p.AgenciaID != nil && *p.AgenciaID == e.AgenciaDestinoID
	case security.RoleJefe:
		// El jefe recibe lo que llega a su departamento
		return p.AgenciaID != nil && *p.AgenciaID == e.AgenciaDestinoID &&
			p.DepartamentoID != nil && e.DepartamentoDestinoID != nil && *p.DepartamentoID == *e.DepartamentoDestinoID
	}
	return false
}

// filtroPorRecibir agrega la condición de "envíos que me toca recibir".
func filtroPorRecibir(ctx context.Context, w *database.Where) {
	w.Add("e.estado = 'EN_TRANSITO'")
	if ag := security.AgenciaScope(ctx); ag != nil {
		w.Add("e.agencia_destino_id = ?", *ag)
		if dep := security.DepartamentoScope(ctx); dep != nil {
			w.Add("e.departamento_destino_id = ?", *dep)
		}
	} else {
		w.Add(sinEncargadoSQL)
	}
	filtroArea(ctx, w)
}

// scopeEnvios limita al encargado a los envíos que salen o llegan a su agencia (al jefe, a su departamento),
// y al admin a los de su área.
func scopeEnvios(ctx context.Context, w *database.Where) {
	if ag := security.AgenciaScope(ctx); ag != nil {
		w.Add("(e.agencia_origen_id = ? OR e.agencia_destino_id = ?)", *ag)
	}
	if dep := security.DepartamentoScope(ctx); dep != nil {
		w.Add(`(e.departamento_destino_id = ? OR EXISTS (SELECT 1 FROM traslados t WHERE t.envio_id = e.id
			AND t.departamento_origen_id = ?))`, *dep)
	}
	filtroArea(ctx, w)
}

// filtroArea deja solo los envíos que llevan activos del área del usuario.
func filtroArea(ctx context.Context, w *database.Where) {
	if cond := inventario.AreaSQL(ctx, "x.categoria_id"); cond != "" {
		w.Add("EXISTS (SELECT 1 FROM traslados t JOIN activos x ON x.id = t.activo_id WHERE t.envio_id = e.id AND "+cond+")",
			*security.AreaScope(ctx))
	}
}

func (h *Handler) listEnvios(r *http.Request) (any, error) {
	ctx := r.Context()
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("e.codigo + ' ' + e.motivo + ' ' + ao.nombre + ' ' + ad.nombre", lq.Search)
	database.Eq(w, "e.estado", httpx.QueryString(r, "estado"))
	if v := httpx.QueryInt64(r, "agenciaId"); v != nil {
		w.Add("(e.agencia_origen_id = ? OR e.agencia_destino_id = ?)", *v)
	}
	if v := httpx.QueryBool(r, "porRecibir"); v != nil && *v {
		filtroPorRecibir(ctx, w)
	}
	if v := httpx.QueryInt64(r, "activoId"); v != nil {
		w.Add("EXISTS (SELECT 1 FROM traslados t WHERE t.envio_id = e.id AND t.activo_id = ?)", *v)
	}
	dateRange(w, r, "e.fecha_envio")
	scopeEnvios(ctx, w)
	page, err := database.Paginate[Envio](ctx, h.db, envioSelect, envioFrom, w,
		lq.OrderBy(map[string]string{"codigo": "e.codigo", "fechaEnvio": "e.fecha_envio", "estado": "e.estado"},
			"CASE WHEN e.estado = 'EN_TRANSITO' THEN 1 ELSE 0 END DESC, e.id DESC"), lq)
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		page.Items[i].PuedeRecibir = puedeRecibir(ctx, page.Items[i])
	}
	return page, nil
}

// totalPorRecibir alimenta el contador del menú.
func (h *Handler) totalPorRecibir(r *http.Request) (any, error) {
	w := &database.Where{}
	filtroPorRecibir(r.Context(), w)
	var total int64
	err := h.db.QueryRow(r.Context(), "SELECT count(*) FROM envios e"+w.SQL(), w.Args()...).Scan(&total)
	return map[string]int64{"total": total}, err
}

func findEnvio(ctx context.Context, q database.Querier, id int64, forUpdate bool) (Envio, error) {
	w := (&database.Where{}).Add("e.id = ?", id)
	scopeEnvios(ctx, w)
	from := envioFrom
	if forUpdate {
		from = envioFromLock
	}
	sql := envioSelect + " " + from + w.SQL()
	e, err := database.Get[Envio](ctx, q, sql, w.Args()...)
	if err != nil {
		return e, apperr.FromDB(err, "Envío no encontrado")
	}
	e.PuedeRecibir = puedeRecibir(ctx, e)
	return e, nil
}

func (h *Handler) getEnvio(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	e, err := findEnvio(ctx, h.db, id, false)
	if err != nil {
		return nil, err
	}
	e.Activos, err = database.List[EnvioActivo](ctx, h.db, `
		SELECT a.id AS activo_id, a.codigo, c.nombre AS categoria, c.icono AS categoria_icono, m.nombre AS marca, a.modelo,
		       a.numero_serie, a.estado, a.condicion
		FROM traslados t
		JOIN activos a ON a.id = t.activo_id
		JOIN categorias c ON c.id = a.categoria_id
		LEFT JOIN marcas m ON m.id = a.marca_id
		WHERE t.envio_id = $1 ORDER BY a.codigo`, id)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return e, nil
}

// ---- Creación (desde traslados y devoluciones) ----

type nuevoEnvio struct {
	origen, destino int64
	depDestino      *int64
	empDestino      *int64
	motivo          string
	fecha           types.Date
	activos         []inventario.Activo
}

// enviar crea el envío EN_TRANSITO con sus filas de traslado y el acta de traslado firmada por quien entrega.
// Los activos no cambian de ubicación hasta que se reciben.
func enviar(ctx context.Context, tx database.Tx, n nuevoEnvio) (EnvioRef, ActaRef, error) {
	var ref EnvioRef
	var origen, destino string
	err := tx.QueryRow(ctx, `
		DECLARE @codigo NVARCHAR(20);
		EXEC SGAT.sp_generar_codigo_anual 'ENV', @codigo OUTPUT;
		INSERT INTO envios (codigo, agencia_origen_id, agencia_destino_id, departamento_destino_id, empleado_destino_id, motivo, fecha_envio, enviado_por)
		VALUES (@codigo, $1, $2, $3, $4, $5, $6, $7);
		SELECT CAST(SCOPE_IDENTITY() AS BIGINT), @codigo, (SELECT nombre FROM agencias WHERE id = $1), (SELECT nombre FROM agencias WHERE id = $2)`,
		n.origen, n.destino, n.depDestino, n.empDestino, n.motivo, n.fecha, security.UserID(ctx)).
		Scan(&ref.ID, &ref.Codigo, &origen, &destino)
	if err != nil {
		return ref, ActaRef{}, err
	}

	ids := make([]int64, 0, len(n.activos))
	for _, a := range n.activos {
		var trasladoID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO traslados (activo_id, agencia_origen_id, agencia_destino_id, departamento_origen_id, departamento_destino_id,
			                       empleado_origen_id, empleado_destino_id, fecha, motivo, usuario_id, envio_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			a.ID, a.AgenciaID, n.destino, a.DepartamentoID, n.depDestino, a.EmpleadoID, n.empDestino, n.fecha, n.motivo,
			security.UserID(ctx), ref.ID).Scan(&trasladoID); err != nil {
			return ref, ActaRef{}, err
		}
		if err := audit.History(ctx, tx, audit.Event{ActivoID: a.ID, Tipo: audit.EvEnvio,
			Descripcion: fmt.Sprintf("Enviado de %s a %s (envío %s), pendiente de recepción. %s", origen, destino, ref.Codigo, n.motivo),
			Referencia:  "envios", ReferenciaID: &ref.ID,
			Datos: map[string]any{"agenciaOrigenId": n.origen, "agenciaDestinoId": n.destino}}); err != nil {
			return ref, ActaRef{}, err
		}
		ids = append(ids, a.ID)
	}

	actaID, numero, err := actas.Crear(ctx, tx, actas.Nueva{Tipo: actas.TipoTraslado, Fecha: n.fecha, EmpleadoID: n.empDestino,
		AgenciaID: n.destino, DepartamentoID: n.depDestino, Observaciones: n.motivo, ActivoIDs: conComponentes(ctx, tx, ids),
		EnvioID: &ref.ID})
	if err != nil {
		return ref, ActaRef{}, err
	}
	err = audit.Log(ctx, tx, audit.Entry{Accion: audit.Enviar, Entidad: "envios", EntidadID: &ref.ID,
		Descripcion: fmt.Sprintf("Envío %s de %s a %s (%d activos)", ref.Codigo, origen, destino, len(ids))})
	return ref, ActaRef{ID: actaID, Numero: numero}, err
}

// grupoEnvios acumula los activos por agencia de origen (un envío por cada origen).
type grupoEnvios struct {
	orden  []int64
	grupos map[int64]*nuevoEnvio
}

func (g *grupoEnvios) add(a inventario.Activo, base nuevoEnvio) {
	if g.grupos == nil {
		g.grupos = map[int64]*nuevoEnvio{}
	}
	n, ok := g.grupos[a.AgenciaID]
	if !ok {
		base.origen = a.AgenciaID
		n = &base
		g.grupos[a.AgenciaID] = n
		g.orden = append(g.orden, a.AgenciaID)
	}
	n.activos = append(n.activos, a)
}

func (g *grupoEnvios) crear(ctx context.Context, tx database.Tx, res *Resultado) error {
	for _, origen := range g.orden {
		ref, acta, err := enviar(ctx, tx, *g.grupos[origen])
		if err != nil {
			return err
		}
		res.Envios = append(res.Envios, ref)
		res.Actas = append(res.Actas, acta)
	}
	return nil
}

// ---- Recepción ----

type recibirInput struct {
	Observaciones string `json:"observaciones" validate:"max=500"`
}

type rechazarInput struct {
	Motivo string `json:"motivo" validate:"required,max=500"`
}

// lockParaRecepcion bloquea el envío y valida que esté en tránsito y que el usuario pueda recibirlo.
func lockParaRecepcion(ctx context.Context, tx database.Tx, id int64) (Envio, []int64, error) {
	e, err := findEnvio(ctx, tx, id, true)
	if err != nil {
		return e, nil, err
	}
	if e.Estado != EnvioEnTransito {
		return e, nil, apperr.Conflict("El envío " + e.Codigo + " ya fue " + map[string]string{
			EnvioRecibido: "recibido", EnvioRechazado: "rechazado"}[e.Estado])
	}
	if !e.PuedeRecibir {
		if e.DestinoConEncargado {
			return e, nil, apperr.Forbidden("Este envío lo recibe el encargado de " + e.AgenciaDestino)
		}
		return e, nil, apperr.Forbidden("Este envío lo recibe un administrador")
	}
	rows, err := tx.Query(ctx, `SELECT activo_id FROM traslados WHERE envio_id = $1 ORDER BY id`, id)
	if err != nil {
		return e, nil, err
	}
	ids, err := database.Column[int64](rows)
	return e, ids, err
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
	p, _ := security.PrincipalFrom(ctx)

	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		e, ids, err := lockParaRecepcion(ctx, tx, id)
		if err != nil {
			return err
		}
		// Primero se cierra el envío: así los activos dejan de estar "en tránsito" y se pueden mover/asignar
		if _, err := tx.Exec(ctx, `UPDATE envios SET estado = 'RECIBIDO', recibido_por = $2, fecha_recepcion = SYSDATETIMEOFFSET(),
			observaciones_recepcion = NULLIF($3, '') WHERE id = $1`, id, p.ID, in.Observaciones); err != nil {
			return err
		}
		var emp *empleadoInfo
		if e.EmpleadoDestinoID != nil {
			found, err := findEmpleado(ctx, tx, *e.EmpleadoDestinoID)
			if err != nil {
				return err
			}
			emp = &found
		}
		desc := fmt.Sprintf("Recibido en %s por %s (envío %s)", e.AgenciaDestino, p.Nombre, e.Codigo)
		if in.Observaciones != "" {
			desc += ". " + in.Observaciones
		}
		for _, activoID := range ids {
			a, err := inventario.Lock(ctx, tx, activoID)
			if err != nil {
				return err
			}
			if err := inventario.Mover(ctx, tx, a.ID, inventario.Ubicacion{AgenciaID: e.AgenciaDestinoID,
				DepartamentoID: e.DepartamentoDestinoID}); err != nil {
				return err
			}
			if err := audit.History(ctx, tx, audit.Event{ActivoID: a.ID, Tipo: audit.EvRecepcion, Descripcion: desc,
				Referencia: "envios", ReferenciaID: &e.ID,
				Datos: map[string]any{"agenciaOrigenId": e.AgenciaOrigenID, "agenciaDestinoId": e.AgenciaDestinoID}}); err != nil {
				return err
			}
			// Envío dirigido a un empleado: se le asigna al recibirlo (ya está en su agencia, no genera otro traslado)
			if emp != nil && a.Estado != inventario.EstadoBaja {
				if _, err := asignar(ctx, tx, a.ID, *emp, types.Today(), "Recepción del envío "+e.Codigo); err != nil {
					return err
				}
			}
		}
		if err := actas.FirmarRecepcion(ctx, tx, e.ID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Recibir, Entidad: "envios", EntidadID: &e.ID,
			Descripcion: fmt.Sprintf("Recepción del envío %s en %s (%d activos)", e.Codigo, e.AgenciaDestino, len(ids))})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return findEnvio(ctx, h.db, id, false)
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
	p, _ := security.PrincipalFrom(ctx)

	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		e, ids, err := lockParaRecepcion(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE envios SET estado = 'RECHAZADO', recibido_por = $2, fecha_recepcion = SYSDATETIMEOFFSET(),
			observaciones_recepcion = $3 WHERE id = $1`, id, p.ID, in.Motivo); err != nil {
			return err
		}
		desc := fmt.Sprintf("Envío %s rechazado en %s por %s: %s. El activo permanece en %s",
			e.Codigo, e.AgenciaDestino, p.Nombre, in.Motivo, e.AgenciaOrigen)
		for _, activoID := range ids {
			if err := audit.History(ctx, tx, audit.Event{ActivoID: activoID, Tipo: audit.EvRechazo, Descripcion: desc,
				Referencia: "envios", ReferenciaID: &e.ID}); err != nil {
				return err
			}
		}
		if err := actas.AnularDeEnvio(ctx, tx, e.ID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Rechazar, Entidad: "envios", EntidadID: &e.ID,
			Descripcion: fmt.Sprintf("Rechazo del envío %s: %s", e.Codigo, in.Motivo)})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return findEnvio(ctx, h.db, id, false)
}
