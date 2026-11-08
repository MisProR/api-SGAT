// Package verificaciones gestiona la verificación física anual del inventario (toma física): el verificador
// recorre empleado por empleado y marca cada activo como encontrado, no encontrado o con diferencias.
//
// Mientras la verificación está ABIERTA, lo que se debe verificar se calcula en vivo (activos vigentes de su
// alcance); al CERRARLA, lo pendiente se guarda como NO_VERIFICADO y el resultado queda fijo.
package verificaciones

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/middleware"
	"sgat-api/internal/modules/actas"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

const (
	EstadoAbierta = "ABIERTA"
	EstadoCerrada = "CERRADA"

	Encontrado   = "ENCONTRADO"
	NoEncontrado = "NO_ENCONTRADO"
	Diferencia   = "DIFERENCIA"
	NoVerificado = "NO_VERIFICADO"
)

var resultadoNombre = map[string]string{
	Encontrado: "Encontrado", NoEncontrado: "No encontrado", Diferencia: "Con diferencia", NoVerificado: "No verificado",
}

type Verificacion struct {
	ID               int64      `json:"id" db:"id"`
	Codigo           string     `json:"codigo" db:"codigo"`
	Nombre           string     `json:"nombre" db:"nombre"`
	Anio             int16      `json:"anio" db:"anio"`
	AgenciaID        *int64     `json:"agenciaId" db:"agencia_id"`
	Agencia          *string    `json:"agencia" db:"agencia"`
	Estado           string     `json:"estado" db:"estado"`
	Observaciones    *string    `json:"observaciones" db:"observaciones"`
	FechaInicio      types.Date `json:"fechaInicio" db:"fecha_inicio"`
	FechaCierre      *time.Time `json:"fechaCierre" db:"fecha_cierre"`
	CreadoPorID      *int64     `json:"-" db:"creado_por"`
	CreadoPor        *string    `json:"creadoPor" db:"creado_por_nombre"`
	CerradoPorID     *int64     `json:"-" db:"cerrado_por"`
	CerradoPor       *string    `json:"cerradoPor" db:"cerrado_por_nombre"`
	CreatedAt        time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt        time.Time  `json:"updatedAt" db:"updated_at"`
	TotalEsperado    int64      `json:"totalEsperado" db:"total_esperado"`
	Encontrados      int64      `json:"encontrados" db:"encontrados"`
	NoEncontrados    int64      `json:"noEncontrados" db:"no_encontrados"`
	Diferencias      int64      `json:"diferencias" db:"diferencias"`
	NoVerificados    int64      `json:"noVerificados" db:"no_verificados"`
	Agencias         []ResumenAgencia `json:"agencias,omitempty" db:"-"`
}

// ResumenAgencia: avance y resultado de la verificación en una agencia.
type ResumenAgencia struct {
	AgenciaID     int64   `json:"agenciaId" db:"agencia_id"`
	Agencia       string  `json:"agencia" db:"agencia"`
	Empleados     int64   `json:"empleados" db:"empleados"`
	Total         int64   `json:"total" db:"total"`
	Encontrados   int64   `json:"encontrados" db:"encontrados"`
	NoEncontrados int64   `json:"noEncontrados" db:"no_encontrados"`
	Diferencias   int64   `json:"diferencias" db:"diferencias"`
	NoVerificados int64   `json:"noVerificados" db:"no_verificados"`
	Pendientes    int64   `json:"pendientes" db:"pendientes"`
	ActaID        *int64  `json:"actaId" db:"acta_id"`
	ActaNumero    *string `json:"actaNumero" db:"acta_numero"`
}

// Responsable: un empleado (o los equipos sin responsable de una agencia) con su avance.
type Responsable struct {
	EmpleadoID    *int64  `json:"empleadoId" db:"empleado_id"`
	Empleado      string  `json:"empleado" db:"empleado"`
	Cedula        *string `json:"cedula" db:"cedula"`
	Cargo         *string `json:"cargo" db:"cargo"`
	AgenciaID     int64   `json:"agenciaId" db:"agencia_id"`
	Agencia       string  `json:"agencia" db:"agencia"`
	Total         int64   `json:"total" db:"total"`
	Encontrados   int64   `json:"encontrados" db:"encontrados"`
	NoEncontrados int64   `json:"noEncontrados" db:"no_encontrados"`
	Diferencias   int64   `json:"diferencias" db:"diferencias"`
	Pendientes    int64   `json:"pendientes" db:"pendientes"`
}

// Item: un activo dentro de la verificación con sus datos y su resultado (nil = pendiente).
type Item struct {
	ActivoID       int64      `json:"activoId" db:"activo_id"`
	Codigo         string     `json:"codigo" db:"codigo"`
	Categoria      string     `json:"categoria" db:"categoria"`
	CategoriaIcono *string    `json:"categoriaIcono" db:"categoria_icono"`
	Marca          *string    `json:"marca" db:"marca"`
	Modelo         *string    `json:"modelo" db:"modelo"`
	NumeroSerie    *string    `json:"numeroSerie" db:"numero_serie"`
	EstadoActivo   string     `json:"estadoActivo" db:"estado_activo"`
	Condicion      string     `json:"condicion" db:"condicion"`
	Componentes    *string    `json:"componentes" db:"componentes"`
	EmpleadoID     *int64     `json:"empleadoId" db:"empleado_id"`
	Empleado       *string    `json:"empleado" db:"empleado"`
	AgenciaID      int64      `json:"agenciaId" db:"agencia_id"`
	Agencia        string     `json:"agencia" db:"agencia"`
	Resultado      *string    `json:"resultado" db:"resultado"`
	Observacion    *string    `json:"observacion" db:"observacion"`
	VerificadoEn   *time.Time `json:"verificadoEn" db:"verificado_en"`
	VerificadoPor  *string    `json:"verificadoPor" db:"verificado_por"`
	// Inventario valorizado: lo invertido (valor de compra) y lo que vale hoy (depreciado)
	EmpleadoCedula *string     `json:"empleadoCedula" db:"empleado_cedula"`
	Departamento   *string     `json:"departamento" db:"departamento"`
	CodigoContable *string     `json:"codigoContable" db:"codigo_contable"`
	FechaCompra    *types.Date `json:"fechaCompra" db:"fecha_compra"`
	ValorCompra    *float64    `json:"valorCompra" db:"valor_compra"`
	ValorActual    *float64    `json:"valorActual" db:"valor_actual"`
}

type Handler struct{ db *database.DB }

func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Get("/{id}/empleados", httpx.Handle(h.responsables))
	r.Get("/{id}/activos", httpx.Handle(h.activos))
	r.Get("/{id}/hallazgos", httpx.Handle(h.hallazgos))
	r.Get("/{id}/inventario", httpx.Handle(h.inventarioValorizado))
	r.Get("/{id}/exportar", h.exportar)
	// Abrir, registrar, cerrar y emitir actas: el verificador o el superadmin
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireRoles(security.RoleSuperAdmin, security.RoleVerificador))
		r.Post("/", httpx.HandleCreated(h.crear))
		r.Post("/{id}/registrar", httpx.Handle(h.registrar))
		r.Post("/{id}/acta", httpx.HandleCreated(h.acta))
		r.Post("/{id}/cerrar", httpx.Handle(h.cerrar))
	})
}

// alcanceSQL: activos que una verificación abierta debe revisar (vigentes, equipos principales, de su agencia).
const alcanceSQL = `a.estado <> 'DADO_DE_BAJA' AND a.activo_padre_id IS NULL AND (v.agencia_id IS NULL OR a.agencia_id = v.agencia_id)`

const selectSQL = `SELECT v.*, ag.nombre AS agencia,
	uc.nombres + ' ' + uc.apellidos AS creado_por_nombre, ux.nombres + ' ' + ux.apellidos AS cerrado_por_nombre,
	CASE WHEN v.estado = 'ABIERTA'
	     THEN (SELECT count(*) FROM activos a WHERE ` + alcanceSQL + `) + c.fuera_de_alcance
	     ELSE c.total END AS total_esperado,
	c.encontrados, c.no_encontrados, c.diferencias, c.no_verificados`

const fromSQL = `FROM verificaciones v
	LEFT JOIN agencias ag ON ag.id = v.agencia_id
	LEFT JOIN usuarios uc ON uc.id = v.creado_por
	LEFT JOIN usuarios ux ON ux.id = v.cerrado_por
	CROSS APPLY (
		-- SQL Server no admite columnas externas (v.*) mezcladas con internas dentro de un agregado:
		-- la condición de alcance se calcula por fila en la subconsulta "x" y luego solo se cuenta.
		SELECT count(*) AS total,
		       count(CASE WHEN x.resultado = 'ENCONTRADO' THEN 1 END) AS encontrados,
		       count(CASE WHEN x.resultado = 'NO_ENCONTRADO' THEN 1 END) AS no_encontrados,
		       count(CASE WHEN x.resultado = 'DIFERENCIA' THEN 1 END) AS diferencias,
		       count(CASE WHEN x.resultado = 'NO_VERIFICADO' THEN 1 END) AS no_verificados,
		       count(CASE WHEN x.fuera_de_alcance = 1 THEN 1 END) AS fuera_de_alcance
		FROM (SELECT i.resultado,
		             CASE WHEN NOT (` + alcanceSQL + `) THEN 1 ELSE 0 END AS fuera_de_alcance
		      FROM verificacion_items i JOIN activos a ON a.id = i.activo_id
		      WHERE i.verificacion_id = v.id) x) c`

// universo devuelve la subconsulta "u" con los activos de la verificación y su resultado (NULL = pendiente).
// Los ya verificados conservan el responsable y la agencia que tenían al verificarse.
func universo(w *database.Where, v Verificacion) string {
	id := w.Arg(v.ID)
	abierta := w.Arg(v.Estado == EstadoAbierta)
	ag := w.Arg(v.AgenciaID)
	return `(SELECT a.id AS activo_id,
	               CASE WHEN i.id IS NULL THEN a.empleado_id ELSE i.empleado_id END AS empleado_id,
	               CASE WHEN i.id IS NULL THEN a.agencia_id ELSE i.agencia_id END AS agencia_id,
	               i.resultado, i.observacion, i.verificado_en, i.verificado_por
	        FROM activos a
	        LEFT JOIN verificacion_items i ON i.activo_id = a.id AND i.verificacion_id = ` + id + `
	        WHERE i.id IS NOT NULL
	           OR (CAST(` + abierta + ` AS BIT) = 1 AND a.estado <> 'DADO_DE_BAJA' AND a.activo_padre_id IS NULL
	               AND (CAST(` + ag + ` AS BIGINT) IS NULL OR a.agencia_id = CAST(` + ag + ` AS BIGINT)))) u`
}

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("v.codigo + ' ' + v.nombre", lq.Search)
	database.Eq(w, "v.estado", httpx.QueryString(r, "estado"))
	database.Eq(w, "v.anio", httpx.QueryInt64(r, "anio"))
	return database.Paginate[Verificacion](r.Context(), h.db, selectSQL, fromSQL, w,
		lq.OrderBy(map[string]string{"codigo": "v.codigo", "anio": "v.anio", "estado": "v.estado"},
			"CASE WHEN v.estado = 'ABIERTA' THEN 0 ELSE 1 END, v.id DESC"), lq)
}

// find carga la verificación; con forUpdate bloquea antes su fila (evita registrar mientras otro la cierra).
func find(ctx context.Context, q database.Querier, id int64, forUpdate bool) (Verificacion, error) {
	if forUpdate {
		var x int64
		if err := q.QueryRow(ctx, `SELECT id FROM verificaciones WITH (UPDLOCK, ROWLOCK) WHERE id = $1`, id).Scan(&x); err != nil {
			return Verificacion{}, apperr.FromDB(err, "Verificación no encontrada")
		}
	}
	v, err := database.Get[Verificacion](ctx, q, selectSQL+" "+fromSQL+" WHERE v.id = $1", id)
	return v, apperr.FromDB(err, "Verificación no encontrada")
}

func (h *Handler) cargar(r *http.Request) (Verificacion, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return Verificacion{}, err
	}
	return find(r.Context(), h.db, id, false)
}

func resumenAgencias(ctx context.Context, q database.Querier, v Verificacion) ([]ResumenAgencia, error) {
	w := &database.Where{}
	u := universo(w, v)
	id := w.Arg(v.ID)
	return database.List[ResumenAgencia](ctx, q, `
		SELECT u.agencia_id, ag.nombre AS agencia, count(DISTINCT u.empleado_id) AS empleados, count(*) AS total,
		       count(CASE WHEN u.resultado = 'ENCONTRADO' THEN 1 END) AS encontrados,
		       count(CASE WHEN u.resultado = 'NO_ENCONTRADO' THEN 1 END) AS no_encontrados,
		       count(CASE WHEN u.resultado = 'DIFERENCIA' THEN 1 END) AS diferencias,
		       count(CASE WHEN u.resultado = 'NO_VERIFICADO' THEN 1 END) AS no_verificados,
		       count(CASE WHEN u.resultado IS NULL THEN 1 END) AS pendientes,
		       ac.id AS acta_id, ac.numero AS acta_numero
		FROM `+u+`
		JOIN agencias ag ON ag.id = u.agencia_id
		OUTER APPLY (SELECT TOP 1 x.id, x.numero FROM actas x WHERE x.verificacion_id = `+id+` AND x.agencia_id = u.agencia_id
		             AND x.anulada = 0 ORDER BY x.id DESC) ac
		GROUP BY u.agencia_id, ag.nombre, ac.id, ac.numero
		ORDER BY ag.nombre`, w.Args()...)
}

func (h *Handler) get(r *http.Request) (any, error) {
	v, err := h.cargar(r)
	if err != nil {
		return nil, err
	}
	if v.Agencias, err = resumenAgencias(r.Context(), h.db, v); err != nil {
		return nil, apperr.Internal(err)
	}
	return v, nil
}

// responsables: empleados (y "sin responsable" por agencia) con el avance de su verificación.
func (h *Handler) responsables(r *http.Request) (any, error) {
	v, err := h.cargar(r)
	if err != nil {
		return nil, err
	}
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	from := `FROM (
		SELECT u.empleado_id, coalesce(e.nombres + ' ' + e.apellidos, 'Sin responsable (equipos de la agencia)') AS empleado,
		       e.cedula, e.cargo, u.agencia_id, ag.nombre AS agencia, count(*) AS total,
		       count(CASE WHEN u.resultado = 'ENCONTRADO' THEN 1 END) AS encontrados,
		       count(CASE WHEN u.resultado = 'NO_ENCONTRADO' THEN 1 END) AS no_encontrados,
		       count(CASE WHEN u.resultado = 'DIFERENCIA' THEN 1 END) AS diferencias,
		       count(CASE WHEN u.resultado IS NULL THEN 1 END) AS pendientes
		FROM ` + universo(w, v) + `
		LEFT JOIN empleados e ON e.id = u.empleado_id
		JOIN agencias ag ON ag.id = u.agencia_id
		GROUP BY u.empleado_id, e.nombres, e.apellidos, e.cedula, e.cargo, u.agencia_id, ag.nombre) x`
	w.Search("x.empleado + ' ' + coalesce(x.cedula, '')", lq.Search)
	database.Eq(w, "x.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	if p := httpx.QueryString(r, "estado"); p != nil {
		switch *p {
		case "pendiente":
			w.Add("x.pendientes > 0")
		case "verificado":
			w.Add("x.pendientes = 0")
		case "hallazgos":
			w.Add("(x.no_encontrados > 0 OR x.diferencias > 0)")
		}
	}
	return database.Paginate[Responsable](r.Context(), h.db, "SELECT x.*", from, w,
		lq.OrderBy(map[string]string{"empleado": "x.empleado", "agencia": "x.agencia", "pendientes": "x.pendientes"},
			"x.agencia, CASE WHEN x.empleado_id IS NULL THEN 1 ELSE 0 END, x.empleado"), lq)
}

// itemSelect: datos del activo (con sus componentes instalados como especificación) y su resultado.
const itemSelect = `SELECT u.activo_id, a.codigo, c.nombre AS categoria, c.icono AS categoria_icono, m.nombre AS marca, a.modelo,
	a.numero_serie, a.estado AS estado_activo, a.condicion,
	(SELECT STRING_AGG(CAST(c2.nombre + coalesce(' ' + x.modelo, '') AS NVARCHAR(MAX)), ', ') WITHIN GROUP (ORDER BY c2.nombre) FROM activos x
	  JOIN categorias c2 ON c2.id = x.categoria_id WHERE x.activo_padre_id = a.id AND x.estado <> 'DADO_DE_BAJA') AS componentes,
	u.empleado_id, e.nombres + ' ' + e.apellidos AS empleado, u.agencia_id, ag.nombre AS agencia,
	u.resultado, u.observacion, u.verificado_en, uv.nombres + ' ' + uv.apellidos AS verificado_por,
	e.cedula AS empleado_cedula, va.departamento, va.codigo_contable, va.fecha_compra, va.valor_compra, va.valor_actual`

func itemFrom(u string) string {
	return `FROM ` + u + `
		JOIN activos a ON a.id = u.activo_id
		JOIN categorias c ON c.id = a.categoria_id
		LEFT JOIN marcas m ON m.id = a.marca_id
		LEFT JOIN empleados e ON e.id = u.empleado_id
		JOIN agencias ag ON ag.id = u.agencia_id
		LEFT JOIN usuarios uv ON uv.id = u.verificado_por
		LEFT JOIN v_activos va ON va.id = u.activo_id`
}

// activos: lo que tiene a cargo un empleado (?empleadoId=) o los equipos sin responsable de una agencia
// (?agenciaId= sin empleadoId), con el resultado de la verificación.
func (h *Handler) activos(r *http.Request) (any, error) {
	v, err := h.cargar(r)
	if err != nil {
		return nil, err
	}
	w := &database.Where{}
	sql := itemSelect + " " + itemFrom(universo(w, v))
	if emp := httpx.QueryInt64(r, "empleadoId"); emp != nil {
		w.Add("u.empleado_id = ?", *emp)
	} else if ag := httpx.QueryInt64(r, "agenciaId"); ag != nil {
		w.Add("u.empleado_id IS NULL")
		w.Add("u.agencia_id = ?", *ag)
	} else {
		return nil, apperr.BadRequest("Indique el empleado o la agencia")
	}
	return database.List[Item](r.Context(), h.db, sql+w.SQL()+" ORDER BY c.tipo, a.codigo", w.Args()...)
}

// hallazgos: activos no encontrados, con diferencias o sin verificar (y pendientes, si se piden).
func (h *Handler) hallazgos(r *http.Request) (any, error) {
	v, err := h.cargar(r)
	if err != nil {
		return nil, err
	}
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	u := universo(w, v)
	if res := httpx.QueryString(r, "resultado"); res != nil {
		if *res == "PENDIENTE" {
			w.Add("u.resultado IS NULL")
		} else {
			w.Add("u.resultado = ?", *res)
		}
	} else {
		w.Add("u.resultado IN ('NO_ENCONTRADO', 'DIFERENCIA', 'NO_VERIFICADO')")
	}
	database.Eq(w, "u.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	w.Search("a.codigo + ' ' + coalesce(a.numero_serie, '') + ' ' + coalesce(e.nombres + ' ' + e.apellidos, '')", lq.Search)
	return database.Paginate[Item](r.Context(), h.db, itemSelect, itemFrom(u), w,
		lq.OrderBy(map[string]string{"codigo": "a.codigo", "agencia": "ag.nombre", "empleado": "e.apellidos"},
			"ag.nombre, e.apellidos, a.codigo"), lq)
}

// ---- Inventario valorizado ----

// itemsInventario: los activos que SÍ se verificaron en esta revisión (encontrados, no encontrados o con
// diferencias; sin pendientes ni no verificados), opcionalmente de una agencia o un responsable, ordenados por
// agencia y responsable, con su valor invertido y su valor actual.
func itemsInventario(r *http.Request, q database.Querier, v Verificacion) ([]Item, error) {
	w := &database.Where{}
	sql := itemSelect + " " + itemFrom(universo(w, v))
	w.Add("u.resultado IN ('ENCONTRADO', 'NO_ENCONTRADO', 'DIFERENCIA')")
	database.Eq(w, "u.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	database.Eq(w, "u.empleado_id", httpx.QueryInt64(r, "empleadoId"))
	return database.List[Item](r.Context(), q, sql+w.SQL()+
		" ORDER BY ag.nombre, CASE WHEN e.apellidos IS NULL THEN 1 ELSE 0 END, e.apellidos, e.nombres, c.tipo, a.codigo", w.Args()...)
}

func (h *Handler) inventarioValorizado(r *http.Request) (any, error) {
	v, err := h.cargar(r)
	if err != nil {
		return nil, err
	}
	return itemsInventario(r, h.db, v)
}

// exportar descarga el inventario valorizado de la verificación en CSV (separador ";" y BOM para Excel).
func (h *Handler) exportar(w http.ResponseWriter, r *http.Request) {
	v, err := h.cargar(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	items, err := itemsInventario(r, h.db, v)
	if err != nil {
		httpx.Error(w, r, apperr.Internal(err))
		return
	}
	_ = audit.Log(r.Context(), h.db, audit.Entry{Accion: audit.Exportar, Entidad: "verificaciones", EntidadID: &v.ID,
		Descripcion: fmt.Sprintf("Exportación del inventario valorizado de %s (%d activos)", v.Codigo, len(items))})

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="inventario_%s.csv"`, v.Codigo))
	_, _ = w.Write([]byte("\xEF\xBB\xBF"))
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	_ = cw.Write([]string{"Agencia", "Departamento", "Responsable", "Cédula", "Código", "Categoría", "Marca", "Modelo", "Serie",
		"Código contable", "Componentes", "Condición", "Fecha compra", "Valor invertido", "Valor actual", "Resultado",
		"Observación", "Verificado por", "Fecha verificación"})
	var invertido, actual float64
	for _, it := range items {
		resultado := "Pendiente"
		if it.Resultado != nil {
			resultado = resultadoNombre[*it.Resultado]
		}
		responsable := "Sin responsable"
		if it.Empleado != nil {
			responsable = *it.Empleado
		}
		fecha := ""
		if it.VerificadoEn != nil {
			fecha = it.VerificadoEn.Format("02/01/2006 15:04")
		}
		compra := ""
		if it.FechaCompra != nil {
			compra = it.FechaCompra.Format("02/01/2006")
		}
		invertido += val(it.ValorCompra)
		actual += val(it.ValorActual)
		_ = cw.Write([]string{it.Agencia, txt(it.Departamento), responsable, txt(it.EmpleadoCedula), it.Codigo, it.Categoria,
			txt(it.Marca), txt(it.Modelo), txt(it.NumeroSerie), txt(it.CodigoContable), txt(it.Componentes), it.Condicion, compra,
			dinero(it.ValorCompra), dinero(it.ValorActual), resultado, txt(it.Observacion), txt(it.VerificadoPor), fecha})
	}
	_ = cw.Write([]string{"TOTAL", "", "", "", fmt.Sprintf("%d activos", len(items)), "", "", "", "", "", "", "", "",
		dinero(&invertido), dinero(&actual), "", "", "", ""})
	cw.Flush()
}

func txt(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func val(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// dinero usa coma decimal para que Excel en español lo reconozca como número.
func dinero(f *float64) string {
	if f == nil {
		return ""
	}
	return strings.Replace(fmt.Sprintf("%.2f", *f), ".", ",", 1)
}

// ---- Crear ----

type crearInput struct {
	Nombre        string `json:"nombre" validate:"required,max=150"`
	Anio          int16  `json:"anio" validate:"required,gte=2000,lte=2100"`
	AgenciaID     *int64 `json:"agenciaId" validate:"omitempty,gt=0"`
	Observaciones string `json:"observaciones" validate:"max=1000"`
}

func (h *Handler) crear(r *http.Request) (any, error) {
	var in crearInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var id int64
	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		var abierta bool
		if err := tx.QueryRow(ctx, `SELECT CAST(CASE WHEN EXISTS (SELECT 1 FROM verificaciones WHERE estado = 'ABIERTA'
			AND (agencia_id IS NULL OR CAST($1 AS BIGINT) IS NULL OR agencia_id = CAST($1 AS BIGINT))) THEN 1 ELSE 0 END AS BIT)`, in.AgenciaID).Scan(&abierta); err != nil {
			return err
		}
		if abierta {
			return apperr.Conflict("Ya hay una verificación abierta que cubre esta agencia; ciérrela antes de abrir otra")
		}
		var codigo string
		if err := tx.QueryRow(ctx, `DECLARE @codigo NVARCHAR(20);
			EXEC SGAT.sp_generar_codigo_anual 'VER', @codigo OUTPUT;
			INSERT INTO verificaciones (codigo, nombre, anio, agencia_id, observaciones, creado_por)
			VALUES (@codigo, $1, $2, $3, NULLIF($4, ''), $5);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT), @codigo`,
			in.Nombre, in.Anio, in.AgenciaID, in.Observaciones, security.UserID(ctx)).Scan(&id, &codigo); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Crear, Entidad: "verificaciones", EntidadID: &id,
			Descripcion: "Inicio de la verificación " + codigo + " " + in.Nombre})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return find(ctx, h.db, id, false)
}

// ---- Registrar resultados ----

type itemInput struct {
	ActivoID    int64  `json:"activoId" validate:"required,gt=0"`
	Resultado   string `json:"resultado" validate:"required,oneof=ENCONTRADO NO_ENCONTRADO DIFERENCIA"`
	Observacion string `json:"observacion" validate:"max=300"`
}

type registrarInput struct {
	Items []itemInput `json:"items" validate:"required,min=1,max=200,dive"`
}

func (h *Handler) registrar(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in registrarInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		v, err := find(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if v.Estado != EstadoAbierta {
			return apperr.Conflict("La verificación " + v.Codigo + " está cerrada")
		}
		for _, it := range in.Items {
			if it.Resultado == Diferencia && it.Observacion == "" {
				return apperr.Validation(map[string]string{"observacion": "Describa la diferencia encontrada"})
			}
			var codigo string
			var empleadoID *int64
			var agenciaID int64
			var enAlcance, yaVerificado bool
			err := tx.QueryRow(ctx, `SELECT a.codigo, a.empleado_id, a.agencia_id,
				CAST(CASE WHEN a.estado <> 'DADO_DE_BAJA' AND a.activo_padre_id IS NULL
				               AND (CAST($2 AS BIGINT) IS NULL OR a.agencia_id = CAST($2 AS BIGINT)) THEN 1 ELSE 0 END AS BIT),
				CAST(CASE WHEN EXISTS (SELECT 1 FROM verificacion_items i WHERE i.verificacion_id = $3 AND i.activo_id = a.id)
				     THEN 1 ELSE 0 END AS BIT)
				FROM activos a WHERE a.id = $1`, it.ActivoID, v.AgenciaID, v.ID).
				Scan(&codigo, &empleadoID, &agenciaID, &enAlcance, &yaVerificado)
			if err != nil {
				return apperr.FromDB(err, fmt.Sprintf("El activo %d no existe", it.ActivoID))
			}
			if !enAlcance && !yaVerificado {
				return apperr.Conflict("El activo " + codigo + " no forma parte de esta verificación")
			}
			// El responsable y la agencia se fijan la primera vez que se verifica
			// Upsert (equivalente a ON CONFLICT ... DO UPDATE); la verificación está bloqueada por find(..., true)
			if _, err := tx.Exec(ctx, `UPDATE verificacion_items SET resultado = $5, observacion = NULLIF($6, ''),
				  verificado_por = $7, verificado_en = SYSDATETIMEOFFSET()
				WHERE verificacion_id = $1 AND activo_id = $2;
				IF @@ROWCOUNT = 0
				  INSERT INTO verificacion_items (verificacion_id, activo_id, empleado_id, agencia_id, resultado,
				    observacion, verificado_por) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7)`,
				v.ID, it.ActivoID, empleadoID, agenciaID, it.Resultado, it.Observacion, security.UserID(ctx)); err != nil {
				return err
			}
			desc := "Verificación " + v.Codigo + ": " + resultadoNombre[it.Resultado]
			if it.Observacion != "" {
				desc += ". " + it.Observacion
			}
			if err := audit.History(ctx, tx, audit.Event{ActivoID: it.ActivoID, Tipo: audit.EvVerificacion, Descripcion: desc,
				Referencia: "verificaciones", ReferenciaID: &v.ID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return map[string]int{"procesados": len(in.Items)}, nil
}

// ---- Acta de verificación por agencia ----

type actaInput struct {
	AgenciaID int64 `json:"agenciaId" validate:"required,gt=0"`
}

func (h *Handler) acta(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in actaInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var actaID int64
	var numero string
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		v, err := find(ctx, tx, id, true)
		if err != nil {
			return err
		}
		w := &database.Where{}
		u := universo(w, v)
		ag := w.Arg(in.AgenciaID)
		type fila struct {
			ActivoID  int64   `db:"activo_id"`
			Resultado *string `db:"resultado"`
		}
		filas, err := database.List[fila](ctx, tx, "SELECT u.activo_id, u.resultado FROM "+u+" WHERE u.agencia_id = "+ag, w.Args()...)
		if err != nil {
			return err
		}
		if len(filas) == 0 {
			return apperr.Conflict("La agencia no tiene activos en esta verificación")
		}
		conteo := map[string]int{}
		ids := make([]int64, len(filas))
		for i, f := range filas {
			ids[i] = f.ActivoID
			res := "PENDIENTE"
			if f.Resultado != nil {
				res = *f.Resultado
			}
			conteo[res]++
		}
		// Una sola acta vigente por agencia: la anterior se anula al emitir una nueva
		if _, err := tx.Exec(ctx, `UPDATE actas SET anulada = 1 WHERE verificacion_id = $1 AND agencia_id = $2 AND anulada = 0`,
			v.ID, in.AgenciaID); err != nil {
			return err
		}
		obs := fmt.Sprintf("Verificación %s (%s). Activos: %d. Encontrados: %d. No encontrados: %d. Con diferencias: %d.",
			v.Codigo, v.Nombre, len(filas), conteo[Encontrado], conteo[NoEncontrado], conteo[Diferencia])
		if n := conteo[NoVerificado] + conteo["PENDIENTE"]; n > 0 {
			obs += fmt.Sprintf(" Sin verificar: %d.", n)
		}
		if actaID, numero, err = actas.Crear(ctx, tx, actas.Nueva{Tipo: actas.TipoVerificacion, AgenciaID: in.AgenciaID,
			Observaciones: obs, ActivoIDs: ids, VerificacionID: &v.ID}); err != nil {
			return err
		}
		// El resultado de cada activo va en la observación del detalle del acta
		_, err = tx.Exec(ctx, `UPDATE d SET observacion = LEFT(CASE i.resultado
			    WHEN 'ENCONTRADO' THEN 'Encontrado' WHEN 'NO_ENCONTRADO' THEN 'NO ENCONTRADO'
			    WHEN 'DIFERENCIA' THEN 'Diferencia' ELSE 'No verificado' END + coalesce(': ' + i.observacion, ''), 300)
			FROM actas_detalle d JOIN verificacion_items i ON i.activo_id = d.activo_id
			WHERE d.acta_id = $1 AND i.verificacion_id = $2`, actaID, v.ID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE actas_detalle SET observacion = 'Pendiente de verificar'
			WHERE acta_id = $1 AND observacion IS NULL`, actaID)
		return err
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return map[string]any{"id": actaID, "numero": numero}, nil
}

// ---- Cerrar ----

func (h *Handler) cerrar(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		v, err := find(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if v.Estado != EstadoAbierta {
			return apperr.Conflict("La verificación " + v.Codigo + " ya está cerrada")
		}
		// Lo que no se revisó queda registrado como NO_VERIFICADO con su responsable actual
		if _, err := tx.Exec(ctx, `INSERT INTO verificacion_items (verificacion_id, activo_id, empleado_id, agencia_id, resultado,
			verificado_por)
			SELECT $1, a.id, a.empleado_id, a.agencia_id, 'NO_VERIFICADO', $3 FROM activos a
			WHERE a.estado <> 'DADO_DE_BAJA' AND a.activo_padre_id IS NULL AND (CAST($2 AS BIGINT) IS NULL OR a.agencia_id = CAST($2 AS BIGINT))
			  AND NOT EXISTS (SELECT 1 FROM verificacion_items x WHERE x.verificacion_id = $1 AND x.activo_id = a.id)`, v.ID, v.AgenciaID, security.UserID(ctx)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE verificaciones SET estado = 'CERRADA', fecha_cierre = SYSDATETIMEOFFSET(), cerrado_por = $2
			WHERE id = $1`, v.ID, security.UserID(ctx)); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "verificaciones", EntidadID: &v.ID,
			Descripcion: "Cierre de la verificación " + v.Codigo})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return find(ctx, h.db, id, false)
}
