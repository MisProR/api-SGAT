// Package reportes provee el panel (dashboard), resúmenes y exportaciones CSV.
package reportes

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

type Handler struct{ db *database.DB }

func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/dashboard", httpx.Handle(h.dashboard))
	r.Get("/agencias", httpx.Handle(h.resumenAgencias))
	r.Get("/costos", httpx.Handle(h.costos))
	r.Get("/fallas", httpx.Handle(h.fallas))
	r.Get("/exportar/{tipo}", h.exportar)
}

// ---- Dashboard ----

type KPIs struct {
	TotalActivos             int64   `json:"totalActivos" db:"total_activos"`
	Activos                  int64   `json:"activos" db:"activos"`
	EnMantenimiento          int64   `json:"enMantenimiento" db:"en_mantenimiento"`
	Danados                  int64   `json:"danados" db:"danados"`
	DadosDeBaja              int64   `json:"dadosDeBaja" db:"dados_de_baja"`
	Asignados                int64   `json:"asignados" db:"asignados"`
	SinAsignar               int64   `json:"sinAsignar" db:"sin_asignar"`
	ValorCompra              float64 `json:"valorCompra" db:"valor_compra"`
	ValorActual              float64 `json:"valorActual" db:"valor_actual"`
	Empleados                int64   `json:"empleados" db:"empleados"`
	Agencias                 int64   `json:"agencias" db:"agencias"`
	MantenimientosPendientes int64   `json:"mantenimientosPendientes" db:"mantenimientos_pendientes"`
	MantenimientosVencidos   int64   `json:"mantenimientosVencidos" db:"mantenimientos_vencidos"`
	AveriasAbiertas          int64   `json:"averiasAbiertas" db:"averias_abiertas"`
	RepuestosStockBajo       int64   `json:"repuestosStockBajo" db:"repuestos_stock_bajo"`
	GarantiasPorVencer       int64   `json:"garantiasPorVencer" db:"garantias_por_vencer"`
	CostoMantenimientoAnio   float64 `json:"costoMantenimientoAnio" db:"costo_mantenimiento_anio"`
}

type Conteo struct {
	Clave  string  `json:"clave" db:"clave"`
	Nombre string  `json:"nombre" db:"nombre"`
	Extra  *string `json:"extra" db:"extra"`
	Total  int64   `json:"total" db:"total"`
}

type MesMantenimiento struct {
	Mes         string  `json:"mes" db:"mes"`
	Preventivos int64   `json:"preventivos" db:"preventivos"`
	Correctivos int64   `json:"correctivos" db:"correctivos"`
	Costo       float64 `json:"costo" db:"costo"`
}

type ResumenAgencia struct {
	AgenciaID       int64   `json:"agenciaId" db:"agencia_id"`
	Codigo          string  `json:"codigo" db:"codigo"`
	Agencia         string  `json:"agencia" db:"agencia"`
	Tipo            string  `json:"tipo" db:"tipo"`
	Total           int64   `json:"total" db:"total"`
	Activos         int64   `json:"activos" db:"activos"`
	EnMantenimiento int64   `json:"enMantenimiento" db:"en_mantenimiento"`
	Danados         int64   `json:"danados" db:"danados"`
	DadosDeBaja     int64   `json:"dadosDeBaja" db:"dados_de_baja"`
	Asignados       int64   `json:"asignados" db:"asignados"`
	ValorTotal      float64 `json:"valorTotal" db:"valor_total"`
	Empleados       int64   `json:"empleados" db:"empleados"`
}

type ProximoMantenimiento struct {
	ID              int64      `json:"id" db:"id"`
	Codigo          string     `json:"codigo" db:"codigo"`
	ActivoID        int64      `json:"activoId" db:"activo_id"`
	ActivoCodigo    string     `json:"activoCodigo" db:"activo_codigo"`
	Categoria       string     `json:"categoria" db:"categoria"`
	Agencia         string     `json:"agencia" db:"agencia"`
	Tecnico         string     `json:"tecnico" db:"tecnico"`
	Tipo            string     `json:"tipo" db:"tipo"`
	Estado          string     `json:"estado" db:"estado"`
	FechaProgramada types.Date `json:"fechaProgramada" db:"fecha_programada"`
	Vencido         bool       `json:"vencido" db:"vencido"`
}

type GarantiaPorVencer struct {
	ID            int64      `json:"id" db:"id"`
	Codigo        string     `json:"codigo" db:"codigo"`
	Categoria     string     `json:"categoria" db:"categoria"`
	Marca         *string    `json:"marca" db:"marca"`
	Modelo        *string    `json:"modelo" db:"modelo"`
	Agencia       string     `json:"agencia" db:"agencia"`
	GarantiaHasta types.Date `json:"garantiaHasta" db:"garantia_hasta"`
	Dias          int        `json:"dias" db:"dias"`
}

type StockBajo struct {
	ID          int64  `json:"id" db:"id"`
	Codigo      string `json:"codigo" db:"codigo"`
	Nombre      string `json:"nombre" db:"nombre"`
	StockActual int    `json:"stockActual" db:"stock_actual"`
	StockMinimo int    `json:"stockMinimo" db:"stock_minimo"`
}

type Actividad struct {
	ID           int64     `json:"id" db:"id"`
	ActivoID     int64     `json:"activoId" db:"activo_id"`
	ActivoCodigo string    `json:"activoCodigo" db:"activo_codigo"`
	TipoEvento   string    `json:"tipoEvento" db:"tipo_evento"`
	Descripcion  string    `json:"descripcion" db:"descripcion"`
	Usuario      *string   `json:"usuario" db:"usuario"`
	CreatedAt    time.Time `json:"createdAt" db:"created_at"`
}

type Dashboard struct {
	KPIs                   KPIs                   `json:"kpis"`
	PorEstado              []Conteo               `json:"porEstado"`
	PorCategoria           []Conteo               `json:"porCategoria"`
	PorTipo                []Conteo               `json:"porTipo"`
	PorAgencia             []ResumenAgencia       `json:"porAgencia"`
	MantenimientosPorMes   []MesMantenimiento     `json:"mantenimientosPorMes"`
	ProximosMantenimientos []ProximoMantenimiento `json:"proximosMantenimientos"`
	GarantiasPorVencer     []GarantiaPorVencer    `json:"garantiasPorVencer"`
	StockBajo              []StockBajo            `json:"stockBajo"`
	ActividadReciente      []Actividad            `json:"actividadReciente"`
}

// filtroAgencia permite ver el panel de una sola agencia (?agenciaId=).
// filtroAgencia filtra por la agencia pedida; el encargado siempre ve solo la suya.
func filtroAgencia(r *http.Request, col string) (string, []any) {
	if ag := security.AgenciaScope(r.Context()); ag != nil {
		return " AND " + col + " = $1", []any{*ag}
	}
	if id := httpx.QueryInt64(r, "agenciaId"); id != nil {
		return " AND " + col + " = $1", []any{*id}
	}
	return "", nil
}

// filtroArea limita los conteos de activos al área del usuario (el admin ve solo seguridad física).
// El área sale de una constante del código, no de la petición, por eso va literal en el SQL.
func filtroArea(r *http.Request, col string) string {
	if area := security.AreaScope(r.Context()); area != nil {
		return " AND " + col + " IN (SELECT id FROM categorias WHERE area = '" + *area + "')"
	}
	return ""
}

func (h *Handler) dashboard(r *http.Request) (any, error) {
	ctx := r.Context()
	fa, args := filtroAgencia(r, "a.agencia_id")
	fa += filtroArea(r, "a.categoria_id")
	// Stock bajo: solo la bodega del área del usuario (repuestos o proveeduría)
	fr := ""
	if area := security.AreaScope(ctx); area != nil {
		fr = " AND area = '" + *area + "'"
	}
	fm, _ := filtroAgencia(r, "m.agencia_id")
	fe, _ := filtroAgencia(r, "e.agencia_id")
	fag, _ := filtroAgencia(r, "ag.id")
	// El jefe ve solo su departamento (el id viene del token, no de la petición)
	if dep := security.DepartamentoScope(ctx); dep != nil {
		fa += fmt.Sprintf(" AND a.departamento_id = %d", *dep)
		fe += fmt.Sprintf(" AND e.departamento_id = %d", *dep)
	}
	var d Dashboard

	g, gctx := errgroup.WithContext(ctx)
	run := func(fn func(ctx context.Context) error) { g.Go(func() error { return fn(gctx) }) }

	run(func(ctx context.Context) (err error) {
		d.KPIs, err = database.Get[KPIs](ctx, h.db, `
			SELECT
			  count(CASE WHEN a.estado <> 'DADO_DE_BAJA' THEN 1 END)                                  AS total_activos,
			  count(CASE WHEN a.estado = 'ACTIVO' THEN 1 END)                                         AS activos,
			  count(CASE WHEN a.estado = 'EN_MANTENIMIENTO' THEN 1 END)                               AS en_mantenimiento,
			  count(CASE WHEN a.estado = 'DANADO' THEN 1 END)                                         AS danados,
			  count(CASE WHEN a.estado = 'DADO_DE_BAJA' THEN 1 END)                                   AS dados_de_baja,
			  count(CASE WHEN a.empleado_id IS NOT NULL AND a.activo_padre_id IS NULL AND a.estado <> 'DADO_DE_BAJA' THEN 1 END) AS asignados,
			  count(CASE WHEN a.empleado_id IS NULL AND a.activo_padre_id IS NULL AND a.estado <> 'DADO_DE_BAJA' THEN 1 END)     AS sin_asignar,
			  coalesce(sum(CASE WHEN a.estado <> 'DADO_DE_BAJA' THEN a.valor_compra END), 0)          AS valor_compra,
			  coalesce(sum(CASE WHEN a.estado <> 'DADO_DE_BAJA' THEN a.valor_actual END), 0)          AS valor_actual,
			  count(CASE WHEN a.estado <> 'DADO_DE_BAJA' AND a.garantia_hasta BETWEEN CAST(GETDATE() AS DATE)
			         AND DATEADD(day, cfg.dias_garantia, CAST(GETDATE() AS DATE)) THEN 1 END)          AS garantias_por_vencer,
			  (SELECT count(*) FROM empleados e WHERE e.habilitado = 1`+fe+`)                         AS empleados,
			  (SELECT count(*) FROM agencias ag WHERE ag.habilitado = 1`+fag+`)                       AS agencias,
			  (SELECT count(*) FROM v_mantenimientos m WHERE m.estado IN ('PROGRAMADO', 'EN_PROCESO')`+fm+`) AS mantenimientos_pendientes,
			  (SELECT count(*) FROM v_mantenimientos m WHERE m.vencido = 1`+fm+`)                     AS mantenimientos_vencidos,
			  (SELECT count(*) FROM averias av JOIN activos x ON x.id = av.activo_id
			    WHERE av.estado IN ('REPORTADA', 'EN_REVISION')`+strings.ReplaceAll(fa, "a.", "x.")+`) AS averias_abiertas,
			  (SELECT count(*) FROM v_repuestos WHERE stock_bajo = 1 AND habilitado = 1`+fr+`)        AS repuestos_stock_bajo,
			  (SELECT coalesce(sum(m.costo), 0) FROM v_mantenimientos m WHERE m.estado = 'FINALIZADO'
			    AND m.fecha_fin >= DATEFROMPARTS(YEAR(GETDATE()), 1, 1)`+fm+`)                        AS costo_mantenimiento_anio
			FROM v_activos a
			-- SQL Server no admite subconsultas dentro de un agregado: el parámetro se une como columna
			CROSS JOIN (SELECT max(TRY_CAST(valor AS INT)) AS dias_garantia FROM configuracion
			            WHERE clave = 'alertas.dias_garantia') cfg
			WHERE 1 = 1`+fa, args...)
		return
	})
	run(func(ctx context.Context) (err error) {
		d.PorEstado, err = database.List[Conteo](ctx, h.db, `
			SELECT a.estado AS clave, a.estado AS nombre, CAST(NULL AS NVARCHAR(MAX)) AS extra, count(*) AS total
			FROM activos a WHERE 1 = 1`+fa+` GROUP BY a.estado ORDER BY total DESC`, args...)
		return
	})
	run(func(ctx context.Context) (err error) {
		d.PorCategoria, err = database.List[Conteo](ctx, h.db, `
			SELECT c.codigo AS clave, c.nombre, c.icono AS extra, count(a.id) AS total
			FROM categorias c JOIN activos a ON a.categoria_id = c.id AND a.estado <> 'DADO_DE_BAJA'`+fa+`
			GROUP BY c.codigo, c.nombre, c.icono ORDER BY total DESC`, args...)
		return
	})
	run(func(ctx context.Context) (err error) {
		d.PorTipo, err = database.List[Conteo](ctx, h.db, `
			SELECT c.tipo AS clave, c.tipo AS nombre, CAST(NULL AS NVARCHAR(MAX)) AS extra, count(a.id) AS total
			FROM categorias c JOIN activos a ON a.categoria_id = c.id AND a.estado <> 'DADO_DE_BAJA'`+fa+`
			GROUP BY c.tipo ORDER BY total DESC`, args...)
		return
	})
	run(func(ctx context.Context) (err error) {
		d.PorAgencia, err = database.List[ResumenAgencia](ctx, h.db, `
			SELECT * FROM v_resumen_agencias ag WHERE (ag.tipo <> 'BODEGA' OR ag.total > 0)`+strings.ReplaceAll(fag, "ag.id", "ag.agencia_id")+`
			ORDER BY ag.total DESC`, args...)
		return
	})
	run(func(ctx context.Context) (err error) {
		d.MantenimientosPorMes, err = database.List[MesMantenimiento](ctx, h.db, `
			SELECT CONVERT(CHAR(7), s.mes, 120) AS mes,
			       count(CASE WHEN m.tipo = 'PREVENTIVO' THEN m.id END) AS preventivos,
			       count(CASE WHEN m.tipo = 'CORRECTIVO' THEN m.id END) AS correctivos,
			       coalesce(sum(m.costo), 0) AS costo
			FROM (SELECT DATEADD(month, -n.n, DATEFROMPARTS(YEAR(GETDATE()), MONTH(GETDATE()), 1)) AS mes
			      FROM (VALUES (0), (1), (2), (3), (4), (5), (6), (7), (8), (9), (10), (11)) AS n(n)) AS s
			LEFT JOIN v_mantenimientos m ON m.estado = 'FINALIZADO'
			     AND DATEFROMPARTS(YEAR(m.fecha_fin), MONTH(m.fecha_fin), 1) = s.mes`+fm+`
			GROUP BY s.mes ORDER BY s.mes`, args...)
		return
	})
	run(func(ctx context.Context) (err error) {
		d.ProximosMantenimientos, err = database.List[ProximoMantenimiento](ctx, h.db, `
			SELECT TOP 8 m.id, m.codigo, m.activo_id, m.activo_codigo, m.categoria, m.agencia, m.tecnico, m.tipo, m.estado,
			       m.fecha_programada, m.vencido
			FROM v_mantenimientos m WHERE m.estado IN ('PROGRAMADO', 'EN_PROCESO')`+fm+`
			ORDER BY m.fecha_programada`, args...)
		return
	})
	run(func(ctx context.Context) (err error) {
		d.GarantiasPorVencer, err = database.List[GarantiaPorVencer](ctx, h.db, `
			SELECT TOP 8 a.id, a.codigo, a.categoria, a.marca, a.modelo, a.agencia, a.garantia_hasta,
			       DATEDIFF(day, CAST(GETDATE() AS DATE), a.garantia_hasta) AS dias
			FROM v_activos a
			WHERE a.estado <> 'DADO_DE_BAJA'
			  AND a.garantia_hasta BETWEEN CAST(GETDATE() AS DATE) AND DATEADD(day, 60, CAST(GETDATE() AS DATE))`+fa+`
			ORDER BY a.garantia_hasta`, args...)
		return
	})
	run(func(ctx context.Context) (err error) {
		d.StockBajo, err = database.List[StockBajo](ctx, h.db, `
			SELECT TOP 8 id, codigo, nombre, stock_actual, stock_minimo FROM v_repuestos
			WHERE stock_bajo = 1 AND habilitado = 1`+fr+` ORDER BY stock_actual - stock_minimo, nombre`)
		return
	})
	run(func(ctx context.Context) (err error) {
		d.ActividadReciente, err = database.List[Actividad](ctx, h.db, `
			SELECT TOP 10 hi.id, hi.activo_id, a.codigo AS activo_codigo, hi.tipo_evento, hi.descripcion,
			       u.nombres + ' ' + u.apellidos AS usuario, hi.created_at
			FROM historial_activos hi JOIN activos a ON a.id = hi.activo_id LEFT JOIN usuarios u ON u.id = hi.usuario_id
			WHERE 1 = 1`+fa+` ORDER BY hi.created_at DESC, hi.id DESC`, args...)
		return
	})

	if err := g.Wait(); err != nil {
		return nil, apperr.Internal(err)
	}
	return d, nil
}

func (h *Handler) resumenAgencias(r *http.Request) (any, error) {
	return database.List[ResumenAgencia](r.Context(), h.db, `SELECT * FROM v_resumen_agencias ORDER BY CASE WHEN tipo = 'MATRIZ' THEN 0 ELSE 1 END, agencia`)
}

type Costo struct {
	Grupo          string  `json:"grupo" db:"grupo"`
	Mantenimientos int64   `json:"mantenimientos" db:"mantenimientos"`
	Preventivos    int64   `json:"preventivos" db:"preventivos"`
	Correctivos    int64   `json:"correctivos" db:"correctivos"`
	Costo          float64 `json:"costo" db:"costo"`
}

// costos agrupa los mantenimientos finalizados por agencia, técnico o categoría (?por=agencia|tecnico|categoria).
func (h *Handler) costos(r *http.Request) (any, error) {
	col := map[string]string{"agencia": "m.agencia", "tecnico": "m.tecnico", "categoria": "m.categoria"}
	por := "agencia"
	if v := httpx.QueryString(r, "por"); v != nil {
		por = *v
	}
	grupo, ok := col[por]
	if !ok {
		return nil, apperr.BadRequest("Agrupación inválida, use agencia, tecnico o categoria")
	}
	w := (&database.Where{}).Add("m.estado = 'FINALIZADO'")
	if v := httpx.QueryString(r, "desde"); v != nil {
		w.Add("m.fecha_fin >= CAST(? AS DATE)", *v)
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		w.Add("m.fecha_fin <= CAST(? AS DATE)", *v)
	}
	return database.List[Costo](r.Context(), h.db, `
		SELECT `+grupo+` AS grupo, count(*) AS mantenimientos,
		       count(CASE WHEN m.tipo = 'PREVENTIVO' THEN 1 END) AS preventivos,
		       count(CASE WHEN m.tipo = 'CORRECTIVO' THEN 1 END) AS correctivos,
		       coalesce(sum(m.costo), 0) AS costo
		FROM v_mantenimientos m`+w.SQL()+` GROUP BY `+grupo+` ORDER BY costo DESC`, w.Args()...)
}

// ---- Análisis de fallas (qué marca, modelo o tipo de equipo falla más) ----

type Falla struct {
	Grupo       string  `json:"grupo" db:"grupo"`
	Activos     int64   `json:"activos" db:"activos"`
	ConFallas   int64   `json:"conFallas" db:"con_fallas"`
	Averias     int64   `json:"averias" db:"averias"`
	Correctivos int64   `json:"correctivos" db:"correctivos"`
	Reemplazos  int64   `json:"reemplazos" db:"reemplazos"`
	Costo       float64 `json:"costo" db:"costo"`
}

// fallas agrupa averías, mantenimientos correctivos y componentes reemplazados por marca, modelo o categoría,
// junto con cuántos activos hay de cada grupo, para comparar qué conviene volver a comprar.
// Un componente reemplazado cuenta como falla del componente retirado (no del equipo que lo tenía).
func (h *Handler) fallas(r *http.Request) (any, error) {
	claves := map[string]string{
		"marca":     "coalesce(m.nombre, 'Sin marca')",
		"modelo":    "coalesce(m.nombre + ' ', '') + coalesce(a.modelo, '(sin modelo)') + N' · ' + c.nombre",
		"categoria": "c.nombre",
	}
	por := "marca"
	if v := httpx.QueryString(r, "por"); v != nil {
		por = *v
	}
	clave, ok := claves[por]
	if !ok {
		return nil, apperr.BadRequest("Agrupación inválida, use marca, modelo o categoria")
	}
	hasta := types.Today().Time
	desde := hasta.AddDate(-1, 0, 0)
	if v := httpx.QueryString(r, "desde"); v != nil {
		if t, err := time.Parse(time.DateOnly, *v); err == nil {
			desde = t
		}
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		if t, err := time.Parse(time.DateOnly, *v); err == nil {
			hasta = t
		}
	}
	filtros := filtroArea(r, "a.categoria_id")
	args := []any{desde, hasta}
	if v := httpx.QueryInt64(r, "categoriaId"); v != nil {
		args = append(args, *v)
		filtros += " AND a.categoria_id = $3"
	}
	return database.List[Falla](r.Context(), h.db, `
		WITH ev AS (
			SELECT av.activo_id, 1 AS averias, 0 AS correctivos, 0 AS reemplazos, CAST(0 AS DECIMAL(12,2)) AS costo
			FROM averias av WHERE CAST(av.fecha_reporte AS DATE) BETWEEN CAST($1 AS DATE) AND CAST($2 AS DATE)
			UNION ALL
			SELECT m.activo_id, 0, 1, 0, m.costo
			FROM mantenimientos m WHERE m.tipo = 'CORRECTIVO' AND m.estado <> 'CANCELADO'
			  AND coalesce(m.fecha_fin, m.fecha_programada) BETWEEN CAST($1 AS DATE) AND CAST($2 AS DATE)
			UNION ALL
			SELECT coalesce(re.componente_retirado_id, re.activo_id), 0, 0, 1, re.costo
			FROM reemplazos re WHERE re.fecha BETWEEN CAST($1 AS DATE) AND CAST($2 AS DATE)
		), g AS (
			SELECT a.id, `+clave+` AS grupo
			FROM activos a JOIN categorias c ON c.id = a.categoria_id LEFT JOIN marcas m ON m.id = a.marca_id
			WHERE 1 = 1`+filtros+`
		)
		SELECT TOP 50 g.grupo, count(DISTINCT g.id) AS activos, count(DISTINCT ev.activo_id) AS con_fallas,
		       coalesce(sum(ev.averias), 0) AS averias, coalesce(sum(ev.correctivos), 0) AS correctivos,
		       coalesce(sum(ev.reemplazos), 0) AS reemplazos, coalesce(sum(ev.costo), 0) AS costo
		FROM g LEFT JOIN ev ON ev.activo_id = g.id
		GROUP BY g.grupo
		HAVING coalesce(sum(ev.averias + ev.correctivos + ev.reemplazos), 0) > 0
		ORDER BY coalesce(sum(ev.averias + ev.correctivos + ev.reemplazos), 0) DESC, coalesce(sum(ev.costo), 0) DESC`, args...)
}

// ---- Exportaciones CSV ----

type export struct {
	titulo  string
	headers []string
	sql     string
}

var exports = map[string]export{
	"mantenimientos": {"mantenimientos",
		[]string{"Código", "Activo", "Categoría", "Descripción", "Agencia", "Tipo", "Estado", "Técnico", "Empresa",
			"Fecha programada", "Fecha inicio", "Fecha fin", "Diagnóstico", "Trabajo realizado", "Costo", "Próxima fecha"},
		`SELECT codigo, activo_codigo, categoria, activo_descripcion, agencia, tipo, estado, tecnico, coalesce(tecnico_empresa, ''),
		        CONVERT(VARCHAR(10), fecha_programada, 103), coalesce(CONVERT(VARCHAR(10), fecha_inicio, 103), ''),
		        coalesce(CONVERT(VARCHAR(10), fecha_fin, 103), ''), coalesce(diagnostico, ''), coalesce(trabajo_realizado, ''),
		        replace(CAST(costo AS VARCHAR(20)), '.', ','), coalesce(CONVERT(VARCHAR(10), proxima_fecha, 103), '')
		 FROM v_mantenimientos WHERE coalesce(fecha_fin, fecha_programada) BETWEEN CAST($1 AS DATE) AND CAST($2 AS DATE)
		 ORDER BY fecha_programada DESC`},
	"asignaciones": {"asignaciones",
		[]string{"Activo", "Categoría", "Marca", "Modelo", "Serie", "Empleado", "Cédula", "Cargo", "Agencia", "Departamento",
			"Fecha asignación", "Fecha devolución", "Estado", "Observaciones"},
		`SELECT activo_codigo, categoria, coalesce(marca, ''), coalesce(modelo, ''), coalesce(numero_serie, ''), empleado,
		        empleado_cedula, coalesce(empleado_cargo, ''), agencia, departamento, CONVERT(VARCHAR(10), fecha_asignacion, 103),
		        coalesce(CONVERT(VARCHAR(10), fecha_devolucion, 103), ''), estado, coalesce(observaciones, '')
		 FROM v_asignaciones WHERE fecha_asignacion BETWEEN CAST($1 AS DATE) AND CAST($2 AS DATE)
		 ORDER BY agencia, empleado, fecha_asignacion DESC`},
	"repuestos": {"repuestos",
		[]string{"Código", "Nombre", "Categoría", "Marca", "Unidad", "Stock", "Stock mínimo", "Costo unitario", "Valor", "Bodega",
			"Ubicación", "Stock bajo"},
		`SELECT codigo, nombre, coalesce(categoria, ''), coalesce(marca, ''), unidad, CAST(stock_actual AS VARCHAR(12)),
		        CAST(stock_minimo AS VARCHAR(12)), replace(CAST(costo_unitario AS VARCHAR(20)), '.', ','),
		        replace(CAST(valor_inventario AS VARCHAR(20)), '.', ','), coalesce(agencia, ''),
		        coalesce(ubicacion, ''), CASE WHEN stock_bajo = 1 THEN 'SI' ELSE 'NO' END
		 FROM v_repuestos WHERE habilitado = 1 AND CAST($1 AS DATE) <= CAST($2 AS DATE) ORDER BY nombre`},
	"averias": {"averias",
		[]string{"Código", "Activo", "Categoría", "Agencia", "Fecha", "Severidad", "Estado", "Descripción", "Solución"},
		`SELECT av.codigo, a.codigo, c.nombre, ag.nombre,
		        CONVERT(VARCHAR(10), av.fecha_reporte, 103) + ' ' + CONVERT(VARCHAR(5), av.fecha_reporte, 108), av.severidad, av.estado,
		        av.descripcion, coalesce(av.solucion, '')
		 FROM averias av JOIN activos a ON a.id = av.activo_id JOIN categorias c ON c.id = a.categoria_id
		 JOIN agencias ag ON ag.id = a.agencia_id
		 WHERE CAST(av.fecha_reporte AS DATE) BETWEEN CAST($1 AS DATE) AND CAST($2 AS DATE) ORDER BY av.fecha_reporte DESC`},
}

func (h *Handler) exportar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	exp, ok := exports[chi.URLParam(r, "tipo")]
	if !ok {
		httpx.Error(w, r, apperr.NotFound("Tipo de reporte no disponible"))
		return
	}
	desde, hasta := rango(r)
	rows, err := h.db.Query(ctx, exp.sql, desde, hasta)
	if err != nil {
		httpx.Error(w, r, apperr.Internal(err))
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s_%s.csv"`, exp.titulo, time.Now().Format("20060102_1504")))
	_, _ = w.Write([]byte("\xEF\xBB\xBF"))
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	_ = cw.Write(exp.headers)
	n := 0
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			break
		}
		rec := make([]string, len(vals))
		for i, v := range vals {
			if v != nil {
				rec[i] = fmt.Sprint(v)
			}
		}
		_ = cw.Write(rec)
		n++
	}
	cw.Flush()
	_ = audit.Log(ctx, h.db, audit.Entry{Accion: audit.Exportar, Entidad: exp.titulo,
		Descripcion: "Exportación de " + exp.titulo + " (" + strconv.Itoa(n) + " registros)"})
}

// rango lee ?desde=&hasta= (por defecto, los últimos 12 meses).
func rango(r *http.Request) (time.Time, time.Time) {
	hasta := time.Now()
	desde := hasta.AddDate(-1, 0, 0)
	if v := httpx.QueryString(r, "desde"); v != nil {
		if t, err := time.Parse(types.DateLayout, *v); err == nil {
			desde = t
		}
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		if t, err := time.Parse(types.DateLayout, *v); err == nil {
			hasta = t
		}
	}
	return desde, hasta
}
