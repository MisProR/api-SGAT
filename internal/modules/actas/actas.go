// Package actas gestiona las actas de entrega-recepción, devolución, traslado y baja.
package actas

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/middleware"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

const (
	TipoEntrega    = "ENTREGA"
	TipoDevolucion = "DEVOLUCION"
	TipoTraslado   = "TRASLADO"
	TipoBaja       = "BAJA"
	// TipoVerificacion: acta de constatación física de la verificación anual
	TipoVerificacion = "VERIFICACION"
)

type Acta struct {
	ID             int64      `json:"id" db:"id"`
	Numero         string     `json:"numero" db:"numero"`
	Tipo           string     `json:"tipo" db:"tipo"`
	Fecha          types.Date `json:"fecha" db:"fecha"`
	EmpleadoID     *int64     `json:"empleadoId" db:"empleado_id"`
	Empleado       *string    `json:"empleado" db:"empleado"`
	EmpleadoCedula *string    `json:"empleadoCedula" db:"empleado_cedula"`
	EmpleadoCargo  *string    `json:"empleadoCargo" db:"empleado_cargo"`
	AgenciaID      int64      `json:"agenciaId" db:"agencia_id"`
	Agencia        string     `json:"agencia" db:"agencia"`
	DepartamentoID *int64     `json:"departamentoId" db:"departamento_id"`
	Departamento   *string    `json:"departamento" db:"departamento"`
	EntregadoPor   *string    `json:"entregadoPor" db:"entregado_por"`
	RecibidoPor    *string    `json:"recibidoPor" db:"recibido_por"`
	Observaciones  *string    `json:"observaciones" db:"observaciones"`
	Anulada        bool       `json:"anulada" db:"anulada"`
	TotalActivos   int64      `json:"totalActivos" db:"total_activos"`
	UsuarioID      *int64     `json:"-" db:"usuario_id"`
	Usuario        *string    `json:"usuario" db:"usuario"`
	// Rol de quien emitió el acta (y su agencia, si es encargado): es el cargo con el que firma
	UsuarioCargo *string `json:"usuarioCargo" db:"usuario_cargo"`
	CreatedAt      time.Time  `json:"createdAt" db:"created_at"`
	// Envío entre agencias al que pertenece (actas con recepción) y firmas digitales de ambas partes
	EnvioID                 *int64            `json:"envioId" db:"envio_id"`
	EnvioCodigo             *string           `json:"envioCodigo" db:"envio_codigo"`
	EnvioEstado             *string           `json:"envioEstado" db:"envio_estado"`
	FirmaEntregaUsuarioID   *int64            `json:"-" db:"firma_entrega_usuario_id"`
	FirmaEntregaUsuario     *string           `json:"firmaEntregaUsuario" db:"firma_entrega_usuario"`
	FirmaEntregaFecha       *time.Time        `json:"firmaEntregaFecha" db:"firma_entrega_fecha"`
	FirmaRecepcionUsuarioID *int64            `json:"-" db:"firma_recepcion_usuario_id"`
	FirmaRecepcionUsuario   *string           `json:"firmaRecepcionUsuario" db:"firma_recepcion_usuario"`
	FirmaRecepcionFecha     *time.Time        `json:"firmaRecepcionFecha" db:"firma_recepcion_fecha"`
	TotalArchivos           int64             `json:"totalArchivos" db:"total_archivos"`
	// Verificación anual a la que pertenece (actas de verificación)
	VerificacionID          *int64            `json:"verificacionId" db:"verificacion_id"`
	// Unidades de materiales de proveeduría incluidas (actas de entrega de materiales)
	TotalMateriales         int64             `json:"totalMateriales" db:"total_materiales"`
	Detalle                 []Detalle         `json:"detalle,omitempty" db:"-"`
	Materiales              []MaterialDetalle `json:"materiales,omitempty" db:"-"`
	Archivos                []Archivo         `json:"archivos,omitempty" db:"-"`
	Institucion             map[string]string `json:"institucion,omitempty" db:"-"`
}

type Detalle struct {
	ActivoID       int64   `json:"activoId" db:"activo_id"`
	Codigo         string  `json:"codigo" db:"codigo"`
	Categoria      string  `json:"categoria" db:"categoria"`
	Marca          *string `json:"marca" db:"marca"`
	Modelo         *string `json:"modelo" db:"modelo"`
	NumeroSerie    *string `json:"numeroSerie" db:"numero_serie"`
	CodigoContable *string `json:"codigoContable" db:"codigo_contable"`
	EstadoActivo   string  `json:"estadoActivo" db:"estado_activo"`
	Condicion      string  `json:"condicion" db:"condicion"`
	Observacion    *string `json:"observacion" db:"observacion"`
}

// MaterialDetalle es un material de proveeduría incluido en el acta.
type MaterialDetalle struct {
	RepuestoID int64   `json:"repuestoId" db:"repuesto_id"`
	Codigo     string  `json:"codigo" db:"codigo"`
	Nombre     string  `json:"nombre" db:"nombre"`
	Marca      *string `json:"marca" db:"marca"`
	Unidad     string  `json:"unidad" db:"unidad"`
	Cantidad   int     `json:"cantidad" db:"cantidad"`
}

// MaterialCantidad es un material y la cantidad a incluir en un acta nueva.
type MaterialCantidad struct {
	RepuestoID int64
	Cantidad   int
}

// Nueva son los datos para generar un acta desde otros módulos.
type Nueva struct {
	Tipo           string
	Fecha          types.Date
	EmpleadoID     *int64
	AgenciaID      int64
	DepartamentoID *int64
	Observaciones  string
	ActivoIDs      []int64
	// EnvioID vincula el acta a un envío entre agencias; en ese caso queda firmada la entrega
	// (usuario actual) y la recepción se firma al recibir el envío (ver FirmarRecepcion).
	EnvioID *int64
	// Materiales de proveeduría entregados (actas de entrega de materiales)
	Materiales []MaterialCantidad
	// VerificacionID vincula el acta de constatación a la verificación anual
	VerificacionID *int64
}

// Crear inserta el acta con el estado actual de cada activo (y los materiales entregados) y deja constancia
// en el historial de los activos.
func Crear(ctx context.Context, tx database.Tx, n Nueva) (id int64, numero string, err error) {
	if len(n.ActivoIDs) == 0 && len(n.Materiales) == 0 {
		return 0, "", apperr.BadRequest("El acta debe incluir al menos un activo o material")
	}
	fecha := n.Fecha
	if fecha.IsZero() {
		fecha = types.Today()
	}
	entregadoPor := ""
	if p, ok := security.PrincipalFrom(ctx); ok {
		entregadoPor = p.Nombre
	}
	// El número ACT-AAAA-NNNNNN se genera con SGAT.sp_generar_codigo_anual (antes trigger trg_actas_numero)
	err = tx.QueryRow(ctx, `
		DECLARE @numero NVARCHAR(25);
		EXEC SGAT.sp_generar_codigo_anual 'ACT', @numero OUTPUT;
		INSERT INTO actas (numero, tipo, fecha, empleado_id, agencia_id, departamento_id, entregado_por, recibido_por, observaciones, usuario_id,
		                   envio_id, firma_entrega_usuario_id, firma_entrega_fecha, verificacion_id)
		SELECT @numero, $1, $2, $3, $4, $5, NULLIF($6, ''),
		       (SELECT nombres + ' ' + apellidos FROM empleados WHERE id = $3), NULLIF($7, ''), $8,
		       $9, CASE WHEN CAST($9 AS BIGINT) IS NOT NULL THEN CAST($8 AS BIGINT) END,
		       CASE WHEN CAST($9 AS BIGINT) IS NOT NULL THEN SYSDATETIMEOFFSET() END, $10;
		SELECT CAST(SCOPE_IDENTITY() AS BIGINT), @numero`,
		n.Tipo, fecha, n.EmpleadoID, n.AgenciaID, n.DepartamentoID, entregadoPor, n.Observaciones, security.UserID(ctx),
		n.EnvioID, n.VerificacionID).
		Scan(&id, &numero)
	if err != nil {
		return 0, "", err
	}
	if len(n.ActivoIDs) > 0 {
		if _, err = tx.Exec(ctx, `
			INSERT INTO actas_detalle (acta_id, activo_id, estado_activo, condicion)
			SELECT $1, a.id, a.estado, a.condicion FROM activos a WHERE a.id IN (SELECT CAST(value AS BIGINT) FROM OPENJSON($2))`, id, n.ActivoIDs); err != nil {
			return 0, "", err
		}
	}
	for _, m := range n.Materiales {
		if _, err = tx.Exec(ctx, `INSERT INTO actas_materiales (acta_id, repuesto_id, cantidad) VALUES ($1, $2, $3)`,
			id, m.RepuestoID, m.Cantidad); err != nil {
			return 0, "", err
		}
	}
	for _, activoID := range n.ActivoIDs {
		if err = audit.History(ctx, tx, audit.Event{ActivoID: activoID, Tipo: audit.EvActa,
			Descripcion: "Incluido en acta de " + tipoNombre[n.Tipo] + " " + numero, Referencia: "actas", ReferenciaID: &id}); err != nil {
			return 0, "", err
		}
	}
	err = audit.Log(ctx, tx, audit.Entry{Accion: audit.Crear, Entidad: "actas", EntidadID: &id,
		Descripcion: "Acta de " + tipoNombre[n.Tipo] + " " + numero})
	return id, numero, err
}

// FirmarRecepcion registra la firma de quien recibe el envío en su acta (si el envío tiene acta).
func FirmarRecepcion(ctx context.Context, tx database.Tx, envioID int64) error {
	p, _ := security.PrincipalFrom(ctx)
	var id int64
	var numero string
	err := tx.QueryRow(ctx, `UPDATE actas SET firma_recepcion_usuario_id = $2, firma_recepcion_fecha = SYSDATETIMEOFFSET(), recibido_por = $3
		OUTPUT INSERTED.id, INSERTED.numero
		WHERE envio_id = $1 AND anulada = 0 AND firma_recepcion_usuario_id IS NULL`,
		envioID, p.ID, p.Nombre).Scan(&id, &numero)
	if errors.Is(err, database.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return audit.Log(ctx, tx, audit.Entry{Accion: audit.Firmar, Entidad: "actas", EntidadID: &id,
		Descripcion: "Firma de recepción del acta " + numero})
}

// AnularDeEnvio anula el acta de un envío rechazado (los activos no cambiaron de agencia).
func AnularDeEnvio(ctx context.Context, tx database.Tx, envioID int64) error {
	_, err := tx.Exec(ctx, `UPDATE actas SET anulada = 1 WHERE envio_id = $1`, envioID)
	return err
}

var tipoNombre = map[string]string{
	TipoEntrega: "entrega", TipoDevolucion: "devolución", TipoTraslado: "traslado", TipoBaja: "baja",
	TipoVerificacion: "verificación",
}

// ---- Endpoints ----

type Handler struct {
	db      *database.DB
	uploads string // carpeta donde se guardan las actas escaneadas
}

func NewHandler(db *database.DB, uploadsDir string) *Handler {
	return &Handler{db: db, uploads: uploadsDir}
}

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.HandleCreated(h.create))
	r.Get("/{id}", httpx.Handle(h.get))
	r.With(middleware.SuperAdminOnly).Post("/{id}/anular", httpx.Handle(h.anular))
	r.Get("/{id}/archivos", httpx.Handle(h.listArchivos))
	r.Post("/{id}/archivos", httpx.HandleCreated(h.subirArchivo))
	r.Get("/{id}/archivos/{archivoId}", h.descargarArchivo)
	r.With(middleware.SuperAdminOnly).Delete("/{id}/archivos/{archivoId}", httpx.Handle(h.eliminarArchivo))
}

// scope limita al encargado a las actas de su agencia o de los envíos donde participa; al admin, a las actas
// con activos de seguridad física, y al encargado de proveeduría, a las actas de entrega de materiales.
func scope(ctx context.Context, w *database.Where) {
	if ag := security.AgenciaScope(ctx); ag != nil {
		w.Add("(ac.agencia_id = ? OR env.agencia_origen_id = ? OR env.agencia_destino_id = ?)", *ag)
	}
	// El jefe: actas de su departamento o de los envíos que llegan a él
	if dep := security.DepartamentoScope(ctx); dep != nil {
		w.Add("(ac.departamento_id = ? OR env.departamento_destino_id = ?)", *dep)
	}
	if area := security.AreaScope(ctx); area != nil {
		w.Add(`(EXISTS (SELECT 1 FROM actas_detalle x JOIN activos y ON y.id = x.activo_id JOIN categorias c ON c.id = y.categoria_id
			WHERE x.acta_id = ac.id AND c.area = ?)
			OR EXISTS (SELECT 1 FROM actas_materiales x JOIN repuestos y ON y.id = x.repuesto_id WHERE x.acta_id = ac.id AND y.area = ?))`,
			*area)
	}
}

const selectSQL = `SELECT ac.*, e.nombres + ' ' + e.apellidos AS empleado, e.cedula AS empleado_cedula, e.cargo AS empleado_cargo,
	ag.nombre AS agencia, d.nombre AS departamento, u.nombres + ' ' + u.apellidos AS usuario,
	(SELECT ro.nombre + CASE WHEN ro.perfil = 'ENCARGADO' THEN coalesce(N' · ' + ux.nombre, '') ELSE '' END
	   FROM roles ro LEFT JOIN agencias ux ON ux.id = u.agencia_id WHERE ro.id = u.rol_id) AS usuario_cargo,
	(SELECT count(*) FROM actas_detalle x WHERE x.acta_id = ac.id) AS total_activos,
	env.codigo AS envio_codigo, env.estado AS envio_estado,
	fe.nombres + ' ' + fe.apellidos AS firma_entrega_usuario,
	fr.nombres + ' ' + fr.apellidos AS firma_recepcion_usuario,
	(SELECT count(*) FROM actas_archivos x WHERE x.acta_id = ac.id) AS total_archivos,
	(SELECT coalesce(sum(x.cantidad), 0) FROM actas_materiales x WHERE x.acta_id = ac.id) AS total_materiales`

const fromSQL = `FROM actas ac
	JOIN agencias ag ON ag.id = ac.agencia_id
	LEFT JOIN empleados e ON e.id = ac.empleado_id
	LEFT JOIN departamentos d ON d.id = ac.departamento_id
	LEFT JOIN usuarios u ON u.id = ac.usuario_id
	LEFT JOIN envios env ON env.id = ac.envio_id
	LEFT JOIN usuarios fe ON fe.id = ac.firma_entrega_usuario_id
	LEFT JOIN usuarios fr ON fr.id = ac.firma_recepcion_usuario_id`

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("ac.numero + ' ' + coalesce(e.nombres + ' ' + e.apellidos, '') + ' ' + coalesce(e.cedula, '')", lq.Search)
	database.Eq(w, "ac.tipo", httpx.QueryString(r, "tipo"))
	database.Eq(w, "ac.empleado_id", httpx.QueryInt64(r, "empleadoId"))
	database.Eq(w, "ac.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	if v := httpx.QueryString(r, "desde"); v != nil {
		w.Add("ac.fecha >= CAST(? AS DATE)", *v)
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		w.Add("ac.fecha <= CAST(? AS DATE)", *v)
	}
	if v := httpx.QueryInt64(r, "activoId"); v != nil {
		w.Add("EXISTS (SELECT 1 FROM actas_detalle x WHERE x.acta_id = ac.id AND x.activo_id = ?)", *v)
	}
	database.Eq(w, "ac.envio_id", httpx.QueryInt64(r, "envioId"))
	database.Eq(w, "ac.verificacion_id", httpx.QueryInt64(r, "verificacionId"))
	if v := httpx.QueryBool(r, "sinArchivo"); v != nil && *v {
		w.Add("ac.anulada = 0 AND NOT EXISTS (SELECT 1 FROM actas_archivos x WHERE x.acta_id = ac.id)")
	}
	scope(r.Context(), w)
	return database.Paginate[Acta](r.Context(), h.db, selectSQL, fromSQL, w,
		lq.OrderBy(map[string]string{"numero": "ac.numero", "fecha": "ac.fecha", "tipo": "ac.tipo"}, "ac.id DESC"), lq)
}

func (h *Handler) get(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	w := (&database.Where{}).Add("ac.id = ?", id)
	scope(ctx, w)
	acta, err := database.Get[Acta](ctx, h.db, selectSQL+" "+fromSQL+w.SQL(), w.Args()...)
	if err != nil {
		return nil, apperr.FromDB(err, "Acta no encontrada")
	}
	if acta.Archivos, err = listarArchivos(ctx, h.db, id); err != nil {
		return nil, apperr.Internal(err)
	}
	if acta.Detalle, err = database.List[Detalle](ctx, h.db, `
		SELECT d.activo_id, a.codigo, c.nombre AS categoria, m.nombre AS marca, a.modelo, a.numero_serie, a.codigo_contable,
		       d.estado_activo, d.condicion, d.observacion
		FROM actas_detalle d
		JOIN activos a ON a.id = d.activo_id
		JOIN categorias c ON c.id = a.categoria_id
		LEFT JOIN marcas m ON m.id = a.marca_id
		WHERE d.acta_id = $1 ORDER BY a.activo_padre_id, c.tipo, a.codigo`, id); err != nil {
		return nil, apperr.Internal(err)
	}
	if acta.Materiales, err = database.List[MaterialDetalle](ctx, h.db, `
		SELECT x.repuesto_id, r.codigo, r.nombre, m.nombre AS marca, r.unidad, x.cantidad
		FROM actas_materiales x
		JOIN repuestos r ON r.id = x.repuesto_id
		LEFT JOIN marcas m ON m.id = r.marca_id
		WHERE x.acta_id = $1 ORDER BY r.nombre`, id); err != nil {
		return nil, apperr.Internal(err)
	}
	// Datos de la institución para la impresión
	rows, err := h.db.Query(ctx, `SELECT clave, valor FROM configuracion WHERE grupo IN ('INSTITUCION', 'ACTAS')`)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	acta.Institucion = map[string]string{}
	var k, v string
	err = database.ForEach(rows, []any{&k, &v}, func() error { acta.Institucion[k] = v; return nil })
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return acta, nil
}

type createInput struct {
	Tipo           string      `json:"tipo" validate:"required,oneof=ENTREGA DEVOLUCION TRASLADO BAJA"`
	Fecha          *types.Date `json:"fecha"`
	EmpleadoID     *int64      `json:"empleadoId" validate:"omitempty,gt=0"`
	AgenciaID      int64       `json:"agenciaId" validate:"required,gt=0"`
	DepartamentoID *int64      `json:"departamentoId" validate:"omitempty,gt=0"`
	Observaciones  string      `json:"observaciones" validate:"max=1000"`
	ActivoIDs      []int64     `json:"activoIds" validate:"required,min=1,max=200,dive,gt=0"`
}

func (h *Handler) create(r *http.Request) (any, error) {
	var in createInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	n := Nueva{Tipo: in.Tipo, EmpleadoID: in.EmpleadoID, AgenciaID: in.AgenciaID, DepartamentoID: in.DepartamentoID,
		Observaciones: in.Observaciones, ActivoIDs: in.ActivoIDs}
	if in.Fecha != nil {
		n.Fecha = *in.Fecha
	}
	var id int64
	var numero string
	err := database.WithTx(r.Context(), h.db, func(tx database.Tx) (err error) {
		id, numero, err = Crear(r.Context(), tx, n)
		return
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return map[string]any{"id": id, "numero": numero}, nil
}

func (h *Handler) anular(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		var numero string
		if err := tx.QueryRow(ctx, `UPDATE actas SET anulada = 1 OUTPUT INSERTED.numero WHERE id = $1 AND anulada = 0`, id).
			Scan(&numero); err != nil {
			return apperr.FromDB(err, "El acta no existe o ya está anulada")
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "actas", EntidadID: &id,
			Descripcion: "Anulación del acta " + numero})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "Acta no encontrada")
	}
	return httpx.MessageResult("Acta anulada"), nil
}
