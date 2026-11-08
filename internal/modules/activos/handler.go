package activos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/middleware"
	"sgat-api/internal/modules/actas"
	"sgat-api/internal/modules/inventario"
	"sgat-api/internal/modules/movimientos"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

type Handler struct{ db *database.DB }

func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Get("/exportar", h.exportar)
	r.Post("/", httpx.HandleCreated(h.create))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Put("/{id}", httpx.Handle(h.update))
	r.Patch("/{id}/estado", httpx.Handle(h.cambiarEstado))
	r.Get("/{id}/historial", httpx.Handle(h.historialActivo))
	r.With(middleware.SuperAdminOnly).Post("/{id}/baja", httpx.Handle(h.darBaja))
	r.With(middleware.SuperAdminOnly).Delete("/{id}", httpx.Handle(h.delete))
}

// HistorialRoutes expone la línea de tiempo global de todos los activos.
func (h *Handler) HistorialRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.historialGlobal))
}

// ---- Consultas ----

var sortable = map[string]string{
	"codigo": "v.codigo", "categoria": "v.categoria", "marca": "v.marca", "modelo": "v.modelo", "numeroSerie": "v.numero_serie",
	"estado": "v.estado", "agencia": "v.agencia", "departamento": "v.departamento", "empleado": "v.empleado",
	"fechaCompra": "v.fecha_compra", "valorCompra": "v.valor_compra", "garantiaHasta": "v.garantia_hasta",
	"proximoMantenimiento": "v.proximo_mantenimiento", "createdAt": "v.created_at",
}

// filtros arma el WHERE común del listado y la exportación.
func filtros(r *http.Request, lq httpx.ListQuery) *database.Where {
	w := &database.Where{}
	w.Search("v.codigo + ' ' + coalesce(v.numero_serie, '') + ' ' + coalesce(v.modelo, '') + ' ' + coalesce(v.marca, '')"+
		" + ' ' + coalesce(v.hostname, '') + ' ' + coalesce(v.codigo_contable, '') + ' ' + coalesce(v.empleado, '')", lq.Search)
	database.Eq(w, "v.categoria_id", httpx.QueryInt64(r, "categoriaId"))
	database.Eq(w, "v.categoria_tipo", httpx.QueryString(r, "tipo"))
	database.Eq(w, "v.estado", httpx.QueryString(r, "estado"))
	database.Eq(w, "v.condicion", httpx.QueryString(r, "condicion"))
	database.Eq(w, "v.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	database.Eq(w, "v.departamento_id", httpx.QueryInt64(r, "departamentoId"))
	database.Eq(w, "v.empleado_id", httpx.QueryInt64(r, "empleadoId"))
	database.Eq(w, "v.marca_id", httpx.QueryInt64(r, "marcaId"))
	database.Eq(w, "v.activo_padre_id", httpx.QueryInt64(r, "padreId"))
	if v := httpx.QueryBool(r, "soloPrincipales"); v != nil && *v {
		w.Add("v.activo_padre_id IS NULL")
	}
	if v := httpx.QueryBool(r, "sinAsignar"); v != nil && *v {
		w.Add("v.empleado_id IS NULL AND v.activo_padre_id IS NULL AND v.estado <> 'DADO_DE_BAJA'")
	}
	if v := httpx.QueryBool(r, "disponibleInstalar"); v != nil && *v {
		w.Add("v.categoria_tipo = 'COMPONENTE' AND v.activo_padre_id IS NULL AND v.estado = 'ACTIVO'")
	}
	if v := httpx.QueryBool(r, "incluirBajas"); v == nil || !*v {
		if httpx.QueryString(r, "estado") == nil {
			w.Add("v.estado <> 'DADO_DE_BAJA'")
		}
	}
	if ag := security.AgenciaScope(r.Context()); ag != nil {
		w.Add("v.agencia_id = ?", *ag)
	}
	if cond := inventario.DepartamentoSQL(r.Context(), "v.departamento_id"); cond != "" {
		w.Add(cond, *security.DepartamentoScope(r.Context()))
	}
	if cond := inventario.AreaSQL(r.Context(), "v.categoria_id"); cond != "" {
		w.Add(cond, *security.AreaScope(r.Context()))
	}
	if g := httpx.QueryString(r, "garantia"); g != nil {
		switch *g {
		case "vigente":
			w.Add("v.garantia_hasta >= CAST(GETDATE() AS DATE)")
		case "vencida":
			w.Add("v.garantia_hasta < CAST(GETDATE() AS DATE)")
		case "por_vencer":
			w.Add("v.garantia_hasta BETWEEN CAST(GETDATE() AS DATE) AND DATEADD(day, 60, CAST(GETDATE() AS DATE))")
		}
	}
	return w
}

// envioPendienteSQL: código del envío EN_TRANSITO que contiene al activo o a su equipo padre.
const envioPendienteSQL = `(SELECT TOP 1 e.codigo FROM traslados t JOIN envios e ON e.id = t.envio_id
	WHERE e.estado = 'EN_TRANSITO' AND t.activo_id IN (v.id, v.activo_padre_id)) AS envio_pendiente, ` + ultimoResponsableSQL

// ultimoResponsableSQL: para un activo sin responsable, quién lo tuvo por última vez y cuándo lo devolvió.
const ultimoResponsableSQL = `
	(SELECT TOP 1 em.nombres + ' ' + em.apellidos FROM asignaciones s JOIN empleados em ON em.id = s.empleado_id
	 WHERE v.empleado_id IS NULL AND s.activo_id = v.id ORDER BY s.fecha_asignacion DESC, s.id DESC) AS ultimo_responsable,
	(SELECT TOP 1 s.fecha_devolucion FROM asignaciones s
	 WHERE v.empleado_id IS NULL AND s.activo_id = v.id ORDER BY s.fecha_asignacion DESC, s.id DESC) AS fecha_devolucion`

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := filtros(r, lq)
	if v := httpx.QueryBool(r, "enTransito"); v != nil && *v {
		w.Add(`EXISTS (SELECT 1 FROM traslados t JOIN envios e ON e.id = t.envio_id
			WHERE e.estado = 'EN_TRANSITO' AND t.activo_id IN (v.id, v.activo_padre_id))`)
	}
	return database.Paginate[Activo](r.Context(), h.db, "SELECT v.*, "+envioPendienteSQL, "FROM v_activos v", w,
		lq.OrderBy(sortable, "v.id DESC"), lq)
}

func find(ctx context.Context, q database.Querier, id int64) (Activo, error) {
	a, err := database.Get[Activo](ctx, q, "SELECT v.*, "+envioPendienteSQL+" FROM v_activos v WHERE v.id = $1", id)
	return a, apperr.FromDB(err, "Activo no encontrado")
}

// verificarAcceso: el admin solo ve los activos de su área; el encargado solo los de su agencia o los que vienen
// en un envío hacia ella.
func verificarAcceso(ctx context.Context, q database.Querier, a Activo) error {
	// El administrador solo ve los activos de su área (seguridad física)
	if area := security.AreaScope(ctx); area != nil {
		var catArea string
		if err := q.QueryRow(ctx, `SELECT area FROM categorias WHERE id = $1`, a.CategoriaID).Scan(&catArea); err != nil {
			return apperr.Internal(err)
		}
		if catArea != *area {
			return apperr.NotFound("Activo no encontrado")
		}
	}
	ag := security.AgenciaScope(ctx)
	dep := security.DepartamentoScope(ctx)
	if ag == nil || (a.AgenciaID == *ag && (dep == nil || (a.DepartamentoID != nil && *a.DepartamentoID == *dep))) {
		return nil
	}
	// Lo que viene en camino hacia su agencia (y, para el jefe, hacia su departamento) también se puede consultar
	var entrante bool
	err := q.QueryRow(ctx, `SELECT CAST(CASE WHEN EXISTS (SELECT 1 FROM traslados t JOIN envios e ON e.id = t.envio_id
		WHERE e.estado = 'EN_TRANSITO' AND e.agencia_destino_id = $2
		  AND (CAST($3 AS BIGINT) IS NULL OR e.departamento_destino_id = CAST($3 AS BIGINT))
		  AND t.activo_id IN ($1, (SELECT activo_padre_id FROM activos WHERE id = $1))) THEN 1 ELSE 0 END AS BIT)`, a.ID, *ag, dep).Scan(&entrante)
	if err != nil {
		return apperr.Internal(err)
	}
	if !entrante {
		return apperr.NotFound("Activo no encontrado")
	}
	return nil
}

func (h *Handler) get(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	a, err := find(ctx, h.db, id)
	if err != nil {
		return nil, err
	}
	if err := verificarAcceso(ctx, h.db, a); err != nil {
		return nil, err
	}
	comps, err := database.List[Activo](ctx, h.db, "SELECT * FROM v_activos WHERE activo_padre_id = $1 ORDER BY categoria, codigo", id)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return Detalle{Activo: a, Componentes: comps}, nil
}

// ---- Validaciones comunes ----

type categoriaInfo struct {
	Nombre        string
	Tipo          string
	RequiereSerie bool
	Habilitado    bool
}

func validar(ctx context.Context, tx database.Tx, in Input, selfID int64) (categoriaInfo, *inventario.Activo, error) {
	var c categoriaInfo
	err := tx.QueryRow(ctx, `SELECT nombre, tipo, requiere_serie, habilitado FROM categorias WHERE id = $1`, in.CategoriaID).
		Scan(&c.Nombre, &c.Tipo, &c.RequiereSerie, &c.Habilitado)
	if err != nil {
		return c, nil, apperr.FromDB(err, "La categoría no existe")
	}
	if !c.Habilitado {
		return c, nil, apperr.Validation(map[string]string{"categoriaId": "La categoría está deshabilitada"})
	}
	if err := inventario.VerificarArea(ctx, tx, in.CategoriaID); err != nil {
		return c, nil, err
	}
	if c.RequiereSerie && (in.NumeroSerie == nil || strings.TrimSpace(*in.NumeroSerie) == "") {
		return c, nil, apperr.Validation(map[string]string{"numeroSerie": "Es obligatorio para la categoría " + c.Nombre})
	}

	if in.ActivoPadreID == nil {
		return c, nil, nil
	}
	if c.Tipo != "COMPONENTE" {
		return c, nil, apperr.Validation(map[string]string{"activoPadreId": "Solo los componentes pueden instalarse en un equipo"})
	}
	if *in.ActivoPadreID == selfID {
		return c, nil, apperr.Validation(map[string]string{"activoPadreId": "Un activo no puede instalarse en sí mismo"})
	}
	padre, err := inventario.LockOperable(ctx, tx, *in.ActivoPadreID)
	if err != nil {
		return c, nil, err
	}
	if padre.EsComponente() || padre.PadreID != nil {
		return c, nil, apperr.Validation(map[string]string{"activoPadreId": "El equipo seleccionado no admite componentes"})
	}
	return c, &padre, nil
}

func especificaciones(m map[string]any) []byte {
	if m == nil {
		return []byte("{}")
	}
	// Elimina claves vacías para mantener el JSON limpio
	for k, v := range m {
		if v == nil || v == "" {
			delete(m, k)
		}
	}
	b, _ := json.Marshal(m)
	return b
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// ---- Alta ----

func (h *Handler) create(r *http.Request) (any, error) {
	var in CreateInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var out Activo
	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		cat, padre, err := validar(ctx, tx, in.Input, 0)
		if err != nil {
			return err
		}
		if len(in.Incluye) > 0 && (cat.Tipo != "EQUIPO" || padre != nil) {
			return apperr.Validation(map[string]string{"incluye": "Solo un equipo (computadora, laptop...) puede registrarse con sus componentes"})
		}
		ubic := inventario.Ubicacion{AgenciaID: in.AgenciaID, DepartamentoID: in.DepartamentoID}
		if padre != nil {
			ubic = inventario.Ubicacion{AgenciaID: padre.AgenciaID, DepartamentoID: padre.DepartamentoID, EmpleadoID: padre.EmpleadoID}
		}

		var id int64
		err = tx.QueryRow(ctx, `
			DECLARE @codigo NVARCHAR(20);
			EXEC SGAT.sp_generar_codigo_activo $1, @codigo OUTPUT;
			INSERT INTO activos (codigo, categoria_id, marca_id, modelo, numero_serie, codigo_contable, descripcion, especificaciones,
			                     condicion, activo_padre_id, agencia_id, departamento_id, empleado_id, proveedor_id, fecha_compra,
			                     valor_compra, numero_factura, garantia_hasta, hostname, direccion_ip, direccion_mac,
			                     observaciones, created_by, updated_by)
			VALUES (@codigo, $1, $2, $3, upper($4), $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, upper($20), $21, $22, $22);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			in.CategoriaID, in.MarcaID, trimPtr(in.Modelo), trimPtr(in.NumeroSerie), trimPtr(in.CodigoContable),
			trimPtr(in.Descripcion), especificaciones(in.Especificaciones), in.Condicion, in.ActivoPadreID, ubic.AgenciaID,
			ubic.DepartamentoID, ubic.EmpleadoID, in.ProveedorID, in.FechaCompra, in.ValorCompra, trimPtr(in.NumeroFactura),
			in.GarantiaHasta, trimPtr(in.Hostname), trimPtr(in.DireccionIP), trimPtr(in.DireccionMAC), trimPtr(in.Observaciones),
			security.UserID(ctx)).Scan(&id)
		if err != nil {
			return err
		}
		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		desc := "Registro inicial del activo en " + out.Agencia
		if padre != nil {
			desc = "Registro e instalación en el equipo " + padre.Codigo
		}
		if err := audit.History(ctx, tx, audit.Event{ActivoID: id, Tipo: audit.EvRegistro, Descripcion: desc}); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, audit.Entry{Accion: audit.Crear, Entidad: "activos", EntidadID: &id,
			Descripcion: "Activo " + out.Codigo + " - " + out.Categoria, Despues: out}); err != nil {
			return err
		}

		// Computadora completa: componentes instalados en el equipo y periféricos en la misma ubicación
		perifericos, err := registrarIncluidos(ctx, tx, in, id, out)
		if err != nil {
			return err
		}

		// Asignación inmediata al responsable (solo equipos/periféricos). En las agencias con encargado la
		// entrega la hace él (con su acta); aquí solo se permite donde no hay encargado, p. ej. la matriz.
		if in.EmpleadoID != nil && padre == nil {
			var agenciaEmp int64
			var agencia string
			var conEncargado bool
			if err := tx.QueryRow(ctx, `SELECT e.agencia_id, ag.nombre,
				CAST(CASE WHEN EXISTS (SELECT 1 FROM usuarios u JOIN roles ro ON ro.id = u.rol_id
					WHERE ro.perfil = 'ENCARGADO' AND ro.habilitado = 1 AND u.habilitado = 1 AND u.agencia_id = e.agencia_id)
				     THEN 1 ELSE 0 END AS BIT)
				FROM empleados e JOIN agencias ag ON ag.id = e.agencia_id WHERE e.id = $1`, *in.EmpleadoID).
				Scan(&agenciaEmp, &agencia, &conEncargado); err != nil {
				return apperr.FromDB(err, "Empleado no encontrado")
			}
			if conEncargado {
				return apperr.Validation(map[string]string{"empleadoId": "La agencia " + agencia +
					" tiene encargado: registre el activo sin responsable y él lo asignará con su acta de entrega"})
			}
			// El equipo (con sus componentes) y sus periféricos se entregan juntos, en una sola acta
			for _, aid := range append([]int64{id}, perifericos...) {
				if err := movimientos.AsignarActivo(ctx, tx, aid, *in.EmpleadoID, types.Today(), "Asignación en el registro"); err != nil {
					return err
				}
			}
			if out, err = find(ctx, tx, id); err != nil {
				return err
			}
			componentes, err := inventario.Componentes(ctx, tx, id)
			if err != nil {
				return err
			}
			// Toda entrega queda respaldada por su acta
			ids := append(append([]int64{id}, componentes...), perifericos...)
			_, _, err = actas.Crear(ctx, tx, actas.Nueva{Tipo: actas.TipoEntrega, EmpleadoID: in.EmpleadoID, AgenciaID: out.AgenciaID,
				DepartamentoID: out.DepartamentoID, Observaciones: "Entrega en el registro del activo", ActivoIDs: ids})
		}
		return err
	})
	return out, apperr.FromDB(err, "")
}

// registrarIncluidos da de alta los componentes (instalados en el equipo) y periféricos (en su misma ubicación)
// que llegan con él, con sus mismos datos de compra. Devuelve los ids de los periféricos.
func registrarIncluidos(ctx context.Context, tx database.Tx, in CreateInput, equipoID int64, equipo Activo) ([]int64, error) {
	var perifericos []int64
	for i, inc := range in.Incluye {
		var nombre, tipo string
		var requiereSerie, habilitado bool
		if err := tx.QueryRow(ctx, `SELECT nombre, tipo, requiere_serie, habilitado FROM categorias WHERE id = $1`, inc.CategoriaID).
			Scan(&nombre, &tipo, &requiereSerie, &habilitado); err != nil {
			return nil, apperr.FromDB(err, "La categoría no existe")
		}
		campo := fmt.Sprintf("incluye.%d", i)
		if !habilitado || (tipo != "COMPONENTE" && tipo != "PERIFERICO") {
			return nil, apperr.Validation(map[string]string{campo: nombre + ": solo se pueden incluir componentes y periféricos habilitados"})
		}
		if err := inventario.VerificarArea(ctx, tx, inc.CategoriaID); err != nil {
			return nil, err
		}
		if requiereSerie && (inc.NumeroSerie == nil || strings.TrimSpace(*inc.NumeroSerie) == "") {
			return nil, apperr.Validation(map[string]string{campo: "El número de serie es obligatorio para " + nombre})
		}
		var padreID *int64
		if tipo == "COMPONENTE" {
			padreID = &equipoID
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			DECLARE @codigo NVARCHAR(20);
			EXEC SGAT.sp_generar_codigo_activo $1, @codigo OUTPUT;
			INSERT INTO activos (codigo, categoria_id, marca_id, modelo, numero_serie, condicion, activo_padre_id, agencia_id, departamento_id,
			                     proveedor_id, fecha_compra, numero_factura, garantia_hasta, created_by, updated_by)
			VALUES (@codigo, $1, $2, $3, upper($4), $5, $6, $7, $8, $9, $10, $11, $12, $13, $13);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			inc.CategoriaID, inc.MarcaID, trimPtr(inc.Modelo), trimPtr(inc.NumeroSerie), in.Condicion, padreID, equipo.AgenciaID,
			equipo.DepartamentoID, in.ProveedorID, in.FechaCompra, trimPtr(in.NumeroFactura), in.GarantiaHasta,
			security.UserID(ctx)).Scan(&id); err != nil {
			return nil, err
		}
		desc := "Registrado junto con el equipo " + equipo.Codigo
		if padreID != nil {
			desc = "Registrado e instalado en el equipo " + equipo.Codigo
		} else {
			perifericos = append(perifericos, id)
		}
		if err := audit.History(ctx, tx, audit.Event{ActivoID: id, Tipo: audit.EvRegistro, Descripcion: desc}); err != nil {
			return nil, err
		}
	}
	return perifericos, nil
}

// ---- Edición ----

func (h *Handler) update(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in Input
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var out Activo
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		actual, err := inventario.LockOperable(ctx, tx, id)
		if err != nil {
			return err
		}
		before, err := find(ctx, tx, id)
		if err != nil {
			return err
		}
		_, padre, err := validar(ctx, tx, in, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE activos SET categoria_id = $2, marca_id = $3, modelo = $4, numero_serie = upper($5), codigo_contable = $6,
			       descripcion = $7, especificaciones = $8, condicion = $9, activo_padre_id = $10, proveedor_id = $11,
			       fecha_compra = $12, valor_compra = $13, numero_factura = $14, garantia_hasta = $15, hostname = $16,
			       direccion_ip = $17, direccion_mac = upper($18), observaciones = $19, updated_by = $20
			WHERE id = $1`,
			id, in.CategoriaID, in.MarcaID, trimPtr(in.Modelo), trimPtr(in.NumeroSerie), trimPtr(in.CodigoContable),
			trimPtr(in.Descripcion), especificaciones(in.Especificaciones), in.Condicion, in.ActivoPadreID, in.ProveedorID,
			in.FechaCompra, in.ValorCompra, trimPtr(in.NumeroFactura), in.GarantiaHasta, trimPtr(in.Hostname),
			trimPtr(in.DireccionIP), trimPtr(in.DireccionMAC), trimPtr(in.Observaciones), security.UserID(ctx)); err != nil {
			return err
		}

		// Cambio de instalación del componente
		cambioPadre := !eqPtr(actual.PadreID, in.ActivoPadreID)
		if cambioPadre && padre != nil {
			if err := inventario.Mover(ctx, tx, id, inventario.Ubicacion{AgenciaID: padre.AgenciaID,
				DepartamentoID: padre.DepartamentoID, EmpleadoID: padre.EmpleadoID}); err != nil {
				return err
			}
		} else if cambioPadre {
			if _, err := tx.Exec(ctx, `UPDATE activos SET empleado_id = NULL WHERE id = $1`, id); err != nil {
				return err
			}
		}

		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		if cambios := diff(before, out); len(cambios) > 0 {
			if err := audit.History(ctx, tx, audit.Event{ActivoID: id, Tipo: audit.EvActualizacion,
				Descripcion: "Actualización de: " + strings.Join(cambios, ", ")}); err != nil {
				return err
			}
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "activos", EntidadID: &id,
			Descripcion: "Activo " + out.Codigo, Antes: before, Despues: out})
	})
	return out, apperr.FromDB(err, "Activo no encontrado")
}

// diff devuelve los nombres legibles de los campos modificados.
func diff(a, b Activo) []string {
	var out []string
	check := func(name string, changed bool) {
		if changed {
			out = append(out, name)
		}
	}
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	check("categoría", a.CategoriaID != b.CategoriaID)
	check("marca", !eqPtr(a.MarcaID, b.MarcaID))
	check("modelo", s(a.Modelo) != s(b.Modelo))
	check("número de serie", s(a.NumeroSerie) != s(b.NumeroSerie))
	check("código contable", s(a.CodigoContable) != s(b.CodigoContable))
	check("descripción", s(a.Descripcion) != s(b.Descripcion))
	check("especificaciones", string(a.Especificaciones) != string(b.Especificaciones))
	check("condición", a.Condicion != b.Condicion)
	check("equipo contenedor", !eqPtr(a.ActivoPadreID, b.ActivoPadreID))
	check("proveedor", !eqPtr(a.ProveedorID, b.ProveedorID))
	check("datos de compra", fmt.Sprint(a.FechaCompra, a.ValorCompra, s(a.NumeroFactura)) !=
		fmt.Sprint(b.FechaCompra, b.ValorCompra, s(b.NumeroFactura)))
	check("garantía", fmt.Sprint(a.GarantiaHasta) != fmt.Sprint(b.GarantiaHasta))
	check("datos de red", s(a.Hostname)+s(a.DireccionIP)+s(a.DireccionMAC) != s(b.Hostname)+s(b.DireccionIP)+s(b.DireccionMAC))
	check("observaciones", s(a.Observaciones) != s(b.Observaciones))
	return out
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ---- Estado y baja ----

func (h *Handler) cambiarEstado(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in EstadoInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	var out Activo
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		a, err := inventario.LockOperable(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.Estado == in.Estado {
			return apperr.Conflict("El activo ya se encuentra en ese estado")
		}
		if err := inventario.CambiarEstado(ctx, tx, a, in.Estado, in.Motivo, "", nil); err != nil {
			return err
		}
		if out, err = find(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "activos", EntidadID: &id,
			Descripcion: fmt.Sprintf("Cambio de estado de %s a %s: %s", a.Codigo, in.Estado, in.Motivo)})
	})
	return out, apperr.FromDB(err, "")
}

func (h *Handler) darBaja(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in BajaInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	fecha := types.Today()
	if in.Fecha != nil {
		fecha = *in.Fecha
	}
	ctx := r.Context()
	result := map[string]any{}
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		a, err := inventario.LockOperable(ctx, tx, id)
		if err != nil {
			return err
		}
		comps, err := inventario.Componentes(ctx, tx, id)
		if err != nil {
			return err
		}
		ids := []int64{id}
		if in.IncluirComponentes {
			ids = append(ids, comps...)
		} else if len(comps) > 0 {
			// Los componentes se desinstalan y quedan disponibles en la misma agencia
			if _, err := tx.Exec(ctx, `UPDATE activos SET activo_padre_id = NULL, empleado_id = NULL WHERE activo_padre_id = $1`, id); err != nil {
				return err
			}
			for _, c := range comps {
				if err := audit.History(ctx, tx, audit.Event{ActivoID: c, Tipo: audit.EvReemplazo,
					Descripcion: "Desinstalado por baja del equipo " + a.Codigo}); err != nil {
					return err
				}
			}
		}

		// El acta se genera antes de la baja para conservar el responsable y estado previos
		if in.GenerarActa {
			acta, numero, err := actas.Crear(ctx, tx, actas.Nueva{Tipo: actas.TipoBaja, Fecha: fecha, EmpleadoID: a.EmpleadoID,
				AgenciaID: a.AgenciaID, DepartamentoID: a.DepartamentoID, Observaciones: in.Motivo, ActivoIDs: ids})
			if err != nil {
				return err
			}
			result["acta"] = map[string]any{"id": acta, "numero": numero}
		}

		for _, aid := range ids {
			if _, err := tx.Exec(ctx, `UPDATE asignaciones SET estado = 'DEVUELTA', fecha_devolucion = CASE WHEN CAST($2 AS DATE) > fecha_asignacion THEN CAST($2 AS DATE) ELSE fecha_asignacion END,
				motivo_devolucion = 'Baja del activo' WHERE activo_id = $1 AND estado = 'VIGENTE'`, aid, fecha); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE mantenimientos SET estado = 'CANCELADO',
				observaciones = trim(coalesce(observaciones + CHAR(10), '') + 'Cancelado por baja del activo')
				WHERE activo_id = $1 AND estado IN ('PROGRAMADO', 'EN_PROCESO')`, aid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE activos SET estado = 'DADO_DE_BAJA', fecha_baja = $2, motivo_baja = $3,
				empleado_id = NULL, updated_by = $4 WHERE id = $1`, aid, fecha, in.Motivo, security.UserID(ctx)); err != nil {
				return err
			}
			if err := audit.History(ctx, tx, audit.Event{ActivoID: aid, Tipo: audit.EvBaja,
				Descripcion: "Dado de baja: " + in.Motivo}); err != nil {
				return err
			}
			if err := audit.Log(ctx, tx, audit.Entry{Accion: audit.DarBaja, Entidad: "activos", EntidadID: &aid,
				Descripcion: "Baja del activo: " + in.Motivo}); err != nil {
				return err
			}
		}
		result["procesados"] = len(ids)
		return nil
	})
	return result, apperr.FromDB(err, "")
}

func (h *Handler) delete(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		before, err := find(ctx, tx, id)
		if err != nil {
			return err
		}
		var usado bool
		if err := tx.QueryRow(ctx, `SELECT CAST(CASE WHEN
			EXISTS (SELECT 1 FROM asignaciones WHERE activo_id = $1) OR
			EXISTS (SELECT 1 FROM mantenimientos WHERE activo_id = $1) OR
			EXISTS (SELECT 1 FROM reemplazos WHERE activo_id = $1 OR componente_retirado_id = $1 OR componente_instalado_id = $1) OR
			EXISTS (SELECT 1 FROM actas_detalle WHERE activo_id = $1) OR
			EXISTS (SELECT 1 FROM averias WHERE activo_id = $1) THEN 1 ELSE 0 END AS BIT)`, id).Scan(&usado); err != nil {
			return err
		}
		if usado {
			return apperr.Conflict("El activo tiene historial (asignaciones, mantenimientos, actas...). Use 'Dar de baja' en lugar de eliminar")
		}
		// fk_activos_padre no puede ser ON DELETE SET NULL en SQL Server (autorreferencia): se liberan los componentes antes
		if _, err := tx.Exec(ctx, `UPDATE activos SET activo_padre_id = NULL WHERE activo_padre_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM activos WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Eliminar, Entidad: "activos", EntidadID: &id,
			Descripcion: "Activo " + before.Codigo + " eliminado", Antes: before})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "Activo no encontrado")
	}
	return httpx.MessageResult("Activo eliminado"), nil
}

// ---- Historial ----

const historialSelect = `SELECT hi.id, hi.activo_id, a.codigo AS activo_codigo, hi.tipo_evento, hi.descripcion, hi.datos,
	hi.referencia, hi.referencia_id, u.nombres + ' ' + u.apellidos AS usuario, hi.created_at`

const historialFrom = `FROM historial_activos hi JOIN activos a ON a.id = hi.activo_id LEFT JOIN usuarios u ON u.id = hi.usuario_id`

func (h *Handler) historialActivo(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	a, err := find(r.Context(), h.db, id)
	if err != nil {
		return nil, err
	}
	if err := verificarAcceso(r.Context(), h.db, a); err != nil {
		return nil, err
	}
	lq := httpx.ParseListQuery(r)
	w := (&database.Where{}).Add("hi.activo_id = ?", id)
	database.Eq(w, "hi.tipo_evento", httpx.QueryString(r, "tipo"))
	return database.Paginate[Evento](r.Context(), h.db, historialSelect, historialFrom, w, "hi.created_at DESC, hi.id DESC", lq)
}

func (h *Handler) historialGlobal(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("a.codigo + ' ' + hi.descripcion", lq.Search)
	database.Eq(w, "hi.activo_id", httpx.QueryInt64(r, "activoId"))
	database.Eq(w, "hi.tipo_evento", httpx.QueryString(r, "tipo"))
	database.Eq(w, "a.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	if cond := inventario.AreaSQL(r.Context(), "a.categoria_id"); cond != "" {
		w.Add(cond, *security.AreaScope(r.Context()))
	}
	if v := httpx.QueryString(r, "desde"); v != nil {
		w.Add("CAST(hi.created_at AS DATE) >= CAST(? AS DATE)", *v)
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		w.Add("CAST(hi.created_at AS DATE) <= CAST(? AS DATE)", *v)
	}
	return database.Paginate[Evento](r.Context(), h.db, historialSelect, historialFrom, w, "hi.created_at DESC, hi.id DESC", lq)
}
