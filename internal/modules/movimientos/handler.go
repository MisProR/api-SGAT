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

type Handler struct{ db *database.DB }

func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

func (h *Handler) AsignacionRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listAsignaciones))
	r.Post("/", httpx.HandleCreated(h.asignar))
	r.Post("/devolucion", httpx.Handle(h.devolver))
	r.Post("/salida", httpx.Handle(h.registrarSalida))
}

func (h *Handler) TrasladoRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listTraslados))
	r.Post("/", httpx.HandleCreated(h.trasladar))
}

// Resultado de una operación de movimiento.
type Resultado struct {
	Procesados int       `json:"procesados"`
	Actas      []ActaRef `json:"actas"`
	// Envíos creados cuando los activos cambian de agencia (quedan en tránsito hasta su recepción)
	Envios []EnvioRef `json:"envios"`
	// Salida de empleado registrada por un encargado: falta que un admin la confirme
	SalidaPendiente bool `json:"salidaPendiente,omitempty"`
}

type ActaRef struct {
	ID     int64  `json:"id"`
	Numero string `json:"numero"`
}

func fechaOHoy(f *types.Date) types.Date {
	if f == nil || f.IsZero() {
		return types.Today()
	}
	return *f
}

// ---- Asignación ----

type asignarInput struct {
	EmpleadoID    int64       `json:"empleadoId" validate:"required,gt=0"`
	ActivoIDs     []int64     `json:"activoIds" validate:"required,min=1,max=100,dive,gt=0"`
	Fecha         *types.Date `json:"fecha"`
	Observaciones string      `json:"observaciones" validate:"max=500"`
	GenerarActa   bool        `json:"generarActa"`
}

func (h *Handler) asignar(r *http.Request) (any, error) {
	var in asignarInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	fecha := fechaOHoy(in.Fecha)
	res := Resultado{Actas: []ActaRef{}, Envios: []EnvioRef{}}
	agenciaEncargado := security.AgenciaScope(ctx)
	if agenciaEncargado != nil {
		// La entrega del encargado siempre queda respaldada por un acta que el empleado firma
		in.GenerarActa = true
	}

	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		emp, err := findEmpleado(ctx, tx, in.EmpleadoID)
		if err != nil {
			return err
		}
		if agenciaEncargado != nil && emp.AgenciaID != *agenciaEncargado {
			return apperr.Forbidden("El empleado " + emp.Nombre + " no pertenece a su agencia")
		}
		if dep := security.DepartamentoScope(ctx); dep != nil && emp.DepartamentoID != *dep {
			return apperr.Forbidden("El empleado " + emp.Nombre + " no pertenece a su departamento")
		}
		ids := unique(in.ActivoIDs)
		for _, id := range ids {
			// Entre agencias el activo debe viajar en un envío y recibirse en el destino
			var agenciaID int64
			var codigo string
			if err := tx.QueryRow(ctx, `SELECT agencia_id, codigo FROM activos WHERE id = $1`, id).Scan(&agenciaID, &codigo); err != nil {
				return apperr.FromDB(err, fmt.Sprintf("El activo %d no existe", id))
			}
			if agenciaID != emp.AgenciaID {
				return apperr.Conflict("El activo " + codigo + " está en otra agencia: envíelo con un traslado indicando a " +
					emp.Nombre + " como responsable; se le asignará cuando lo reciban")
			}
			if _, err := asignar(ctx, tx, id, emp, fecha, in.Observaciones); err != nil {
				return err
			}
		}
		res.Procesados = len(ids)
		if in.GenerarActa {
			acta, err := crearActa(ctx, tx, actas.TipoEntrega, fecha, &emp.ID, emp.AgenciaID, &emp.DepartamentoID,
				in.Observaciones, conComponentes(ctx, tx, ids))
			if err != nil {
				return err
			}
			res.Actas = append(res.Actas, acta)
		}
		return nil
	})
	return res, apperr.FromDB(err, "")
}

// ---- Devolución ----

type devolverInput struct {
	ActivoIDs        []int64     `json:"activoIds" validate:"required,min=1,max=100,dive,gt=0"`
	Fecha            *types.Date `json:"fecha"`
	Motivo           string      `json:"motivo" validate:"required,max=300"`
	Condicion        *string     `json:"condicion" validate:"omitempty,oneof=NUEVO BUENO REGULAR MALO"`
	AgenciaDestinoID *int64      `json:"agenciaDestinoId" validate:"omitempty,gt=0"`
	GenerarActa      bool        `json:"generarActa"`
}

func (h *Handler) devolver(r *http.Request) (any, error) {
	var in devolverInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	fecha := fechaOHoy(in.Fecha)
	res := Resultado{Actas: []ActaRef{}, Envios: []EnvioRef{}}
	// El encargado registra la devolución en su agencia; para mandarlo a otra agencia hace luego un envío
	if ag := security.AgenciaScope(ctx); ag != nil && in.AgenciaDestinoID != nil && *in.AgenciaDestinoID != *ag {
		return nil, apperr.Validation(map[string]string{"agenciaDestinoId": "Registre la devolución en su agencia y luego envíe el activo"})
	}

	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		var envios grupoEnvios
		// Agrupa por empleado para generar un acta por cada responsable
		type grupo struct {
			agenciaID int64
			depID     *int64
			activos   []int64
		}
		grupos := map[int64]*grupo{}
		var orden []int64

		for _, id := range unique(in.ActivoIDs) {
			a, err := inventario.LockOperable(ctx, tx, id)
			if err != nil {
				return err
			}
			if a.PadreID != nil {
				return apperr.Conflict("El componente " + a.Codigo + " está instalado en otro equipo; devuelva el equipo principal")
			}
			if a.EmpleadoID == nil {
				return apperr.Conflict("El activo " + a.Codigo + " no tiene un responsable asignado")
			}
			if err := cerrarAsignacion(ctx, tx, a, fecha, in.Motivo); err != nil {
				return err
			}
			if in.Condicion != nil {
				if _, err := tx.Exec(ctx, `UPDATE activos SET condicion = $2 WHERE id = $1`, a.ID, *in.Condicion); err != nil {
					return err
				}
			}
			// Queda sin responsable en su agencia; si va a otra agencia, sale en un envío pendiente de recepción
			if err := inventario.Mover(ctx, tx, a.ID, inventario.Ubicacion{AgenciaID: a.AgenciaID, DepartamentoID: a.DepartamentoID}); err != nil {
				return err
			}
			if in.AgenciaDestinoID != nil && *in.AgenciaDestinoID != a.AgenciaID {
				enviado := a // ya sin responsable (la copia conserva a.EmpleadoID para agrupar el acta)
				enviado.EmpleadoID = nil
				envios.add(enviado, nuevoEnvio{destino: *in.AgenciaDestinoID, motivo: "Devolución: " + in.Motivo, fecha: fecha})
			}
			if err := audit.Log(ctx, tx, audit.Entry{Accion: audit.Devolver, Entidad: "activos", EntidadID: &a.ID,
				Descripcion: "Devolución del activo " + a.Codigo + ": " + in.Motivo}); err != nil {
				return err
			}

			g, ok := grupos[*a.EmpleadoID]
			if !ok {
				g = &grupo{agenciaID: a.AgenciaID, depID: a.DepartamentoID}
				grupos[*a.EmpleadoID] = g
				orden = append(orden, *a.EmpleadoID)
			}
			g.activos = append(g.activos, a.ID)
			res.Procesados++
		}

		if in.GenerarActa {
			for _, empID := range orden {
				g := grupos[empID]
				acta, err := crearActa(ctx, tx, actas.TipoDevolucion, fecha, &empID, g.agenciaID, g.depID, in.Motivo,
					conComponentes(ctx, tx, g.activos))
				if err != nil {
					return err
				}
				res.Actas = append(res.Actas, acta)
			}
		}
		return envios.crear(ctx, tx, &res)
	})
	return res, apperr.FromDB(err, "")
}

// ---- Salida de un empleado ----

type salidaInput struct {
	EmpleadoID int64       `json:"empleadoId" validate:"required,gt=0"`
	Fecha      *types.Date `json:"fecha"`
	Motivo     string      `json:"motivo" validate:"required,max=300"`
}

// registrarSalida devuelve todos los activos del empleado (quedan sin responsable en su agencia, listos para
// quien ocupe el puesto), genera el acta de devolución y marca la salida. Si la registra un encargado queda
// pendiente hasta que un admin la confirme; si la registra un admin, el empleado queda deshabilitado.
func (h *Handler) registrarSalida(r *http.Request) (any, error) {
	var in salidaInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	fecha := fechaOHoy(in.Fecha)
	agenciaEncargado := security.AgenciaScope(ctx)
	res := Resultado{Actas: []ActaRef{}, Envios: []EnvioRef{}, SalidaPendiente: agenciaEncargado != nil}
	motivo := "Salida del empleado: " + in.Motivo

	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		var nombre string
		var agenciaID, depID int64
		var habilitado, pendiente bool
		err := tx.QueryRow(ctx, `SELECT nombres + ' ' + apellidos, agencia_id, departamento_id, habilitado, salida_pendiente
			FROM empleados WITH (UPDLOCK, ROWLOCK) WHERE id = $1`, in.EmpleadoID).Scan(&nombre, &agenciaID, &depID, &habilitado, &pendiente)
		if err != nil {
			return apperr.FromDB(err, "Empleado no encontrado")
		}
		if agenciaEncargado != nil && agenciaID != *agenciaEncargado {
			return apperr.NotFound("Empleado no encontrado")
		}
		if dep := security.DepartamentoScope(ctx); dep != nil && depID != *dep {
			return apperr.NotFound("Empleado no encontrado")
		}
		if !habilitado {
			return apperr.Conflict("El empleado " + nombre + " ya está deshabilitado")
		}
		if pendiente {
			return apperr.Conflict("La salida de " + nombre + " ya fue registrada y espera la confirmación de un administrador")
		}

		rows, err := tx.Query(ctx, `SELECT id FROM activos WHERE empleado_id = $1 AND activo_padre_id IS NULL
			AND estado <> 'DADO_DE_BAJA' ORDER BY id`, in.EmpleadoID)
		if err != nil {
			return err
		}
		ids, err := database.Column[int64](rows)
		if err != nil {
			return err
		}
		for _, id := range ids {
			a, err := inventario.LockOperable(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := cerrarAsignacion(ctx, tx, a, fecha, motivo); err != nil {
				return err
			}
			if err := inventario.Mover(ctx, tx, a.ID, inventario.Ubicacion{AgenciaID: a.AgenciaID, DepartamentoID: a.DepartamentoID}); err != nil {
				return err
			}
			if err := audit.Log(ctx, tx, audit.Entry{Accion: audit.Devolver, Entidad: "activos", EntidadID: &a.ID,
				Descripcion: "Devolución del activo " + a.Codigo + " por salida de " + nombre}); err != nil {
				return err
			}
		}
		res.Procesados = len(ids)
		if len(ids) > 0 {
			acta, err := crearActa(ctx, tx, actas.TipoDevolucion, fecha, &in.EmpleadoID, agenciaID, &depID, motivo,
				conComponentes(ctx, tx, ids))
			if err != nil {
				return err
			}
			res.Actas = append(res.Actas, acta)
		}

		if _, err := tx.Exec(ctx, `UPDATE empleados SET fecha_salida = $2, motivo_salida = $3, salida_pendiente = $4,
			habilitado = CASE WHEN habilitado = 1 AND $4 = 1 THEN 1 ELSE 0 END WHERE id = $1`, in.EmpleadoID, fecha, in.Motivo, res.SalidaPendiente); err != nil {
			return err
		}
		desc := "Salida del empleado " + nombre + ": " + in.Motivo
		if res.SalidaPendiente {
			desc += " (pendiente de confirmación)"
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "empleados", EntidadID: &in.EmpleadoID,
			Descripcion: desc})
	})
	return res, apperr.FromDB(err, "")
}

// ---- Traslado ----

type trasladoInput struct {
	ActivoIDs             []int64     `json:"activoIds" validate:"required,min=1,max=100,dive,gt=0"`
	AgenciaDestinoID      int64       `json:"agenciaDestinoId" validate:"required,gt=0"`
	DepartamentoDestinoID *int64      `json:"departamentoDestinoId" validate:"omitempty,gt=0"`
	EmpleadoDestinoID     *int64      `json:"empleadoDestinoId" validate:"omitempty,gt=0"`
	Motivo                string      `json:"motivo" validate:"required,max=300"`
	Fecha                 *types.Date `json:"fecha"`
	GenerarActa           bool        `json:"generarActa"`
}

// trasladar mueve activos de ubicación:
//   - Dentro de la misma agencia (cambio de departamento o de responsable): se aplica de inmediato.
//   - A otra agencia: se crea un envío por agencia de origen; los activos quedan en tránsito
//     hasta que en el destino se reciban (ver envios.go). El acta del envío se genera siempre.
//
// El ENCARGADO solo puede enviar activos de su agencia a otra agencia, sin asignarlos a un empleado.
func (h *Handler) trasladar(r *http.Request) (any, error) {
	var in trasladoInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	fecha := fechaOHoy(in.Fecha)
	res := Resultado{Actas: []ActaRef{}, Envios: []EnvioRef{}}

	if ag := security.AgenciaScope(ctx); ag != nil {
		if in.AgenciaDestinoID == *ag {
			return nil, apperr.Validation(map[string]string{"agenciaDestinoId": "Seleccione una agencia distinta a la suya"})
		}
		if in.EmpleadoDestinoID != nil {
			return nil, apperr.Validation(map[string]string{"empleadoDestinoId": "El encargado no asigna activos a empleados; envíelo sin responsable"})
		}
	}

	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		agenciaID, depID := in.AgenciaDestinoID, in.DepartamentoDestinoID
		var emp *empleadoInfo
		if in.EmpleadoDestinoID != nil {
			e, err := findEmpleado(ctx, tx, *in.EmpleadoDestinoID)
			if err != nil {
				return err
			}
			if e.AgenciaID != in.AgenciaDestinoID {
				return apperr.Validation(map[string]string{"empleadoDestinoId": "El empleado no pertenece a la agencia destino"})
			}
			emp, depID = &e, &e.DepartamentoID
		}
		var empID *int64
		if emp != nil {
			empID = &emp.ID
		}

		var envios grupoEnvios
		var inmediatos []int64
		for _, id := range unique(in.ActivoIDs) {
			a, err := inventario.LockOperable(ctx, tx, id)
			if err != nil {
				return err
			}
			if a.PadreID != nil {
				return apperr.Conflict("El componente " + a.Codigo + " está instalado en otro equipo; traslade el equipo principal")
			}
			if a.AgenciaID == agenciaID && emp == nil && ptrEq(a.DepartamentoID, depID) {
				return apperr.Conflict("El activo " + a.Codigo + " ya se encuentra en la ubicación destino")
			}
			if emp != nil && a.EmpleadoID != nil && *a.EmpleadoID == emp.ID {
				return apperr.Conflict("El activo " + a.Codigo + " ya está a cargo de " + emp.Nombre)
			}

			if a.AgenciaID != agenciaID {
				// Sale de la agencia: deja de estar a cargo de su responsable y viaja en un envío
				if a.EmpleadoID != nil {
					if err := cerrarAsignacion(ctx, tx, a, fecha, "Envío a otra agencia: "+in.Motivo); err != nil {
						return err
					}
					if err := inventario.Mover(ctx, tx, a.ID, inventario.Ubicacion{AgenciaID: a.AgenciaID, DepartamentoID: a.DepartamentoID}); err != nil {
						return err
					}
					a.EmpleadoID = nil
				}
				envios.add(a, nuevoEnvio{destino: agenciaID, depDestino: depID, empDestino: empID, motivo: in.Motivo, fecha: fecha})
			} else {
				if emp != nil {
					// asignar cierra la asignación previa y registra el cambio de departamento si corresponde
					if _, err := asignar(ctx, tx, a.ID, *emp, fecha, in.Motivo); err != nil {
						return err
					}
				} else {
					if a.EmpleadoID != nil {
						if err := cerrarAsignacion(ctx, tx, a, fecha, "Traslado: "+in.Motivo); err != nil {
							return err
						}
					}
					if err := registrarTraslado(ctx, tx, a, agenciaID, depID, nil, fecha, in.Motivo); err != nil {
						return err
					}
					if err := inventario.Mover(ctx, tx, a.ID, inventario.Ubicacion{AgenciaID: agenciaID, DepartamentoID: depID}); err != nil {
						return err
					}
				}
				if err := audit.Log(ctx, tx, audit.Entry{Accion: audit.Trasladar, Entidad: "activos", EntidadID: &a.ID,
					Descripcion: "Traslado del activo " + a.Codigo + ": " + in.Motivo}); err != nil {
					return err
				}
				inmediatos = append(inmediatos, a.ID)
			}
			res.Procesados++
		}

		if in.GenerarActa && len(inmediatos) > 0 {
			acta, err := crearActa(ctx, tx, actas.TipoTraslado, fecha, empID, agenciaID, depID, in.Motivo,
				conComponentes(ctx, tx, inmediatos))
			if err != nil {
				return err
			}
			res.Actas = append(res.Actas, acta)
		}
		return envios.crear(ctx, tx, &res)
	})
	return res, apperr.FromDB(err, "")
}

// ---- Listados ----

type Asignacion struct {
	ID               int64       `json:"id" db:"id"`
	ActivoID         int64       `json:"activoId" db:"activo_id"`
	ActivoCodigo     string      `json:"activoCodigo" db:"activo_codigo"`
	Categoria        string      `json:"categoria" db:"categoria"`
	Marca            *string     `json:"marca" db:"marca"`
	Modelo           *string     `json:"modelo" db:"modelo"`
	NumeroSerie      *string     `json:"numeroSerie" db:"numero_serie"`
	EmpleadoID       int64       `json:"empleadoId" db:"empleado_id"`
	Empleado         string      `json:"empleado" db:"empleado"`
	EmpleadoCedula   string      `json:"empleadoCedula" db:"empleado_cedula"`
	EmpleadoCargo    *string     `json:"empleadoCargo" db:"empleado_cargo"`
	AgenciaID        int64       `json:"agenciaId" db:"agencia_id"`
	Agencia          string      `json:"agencia" db:"agencia"`
	DepartamentoID   int64       `json:"departamentoId" db:"departamento_id"`
	Departamento     string      `json:"departamento" db:"departamento"`
	FechaAsignacion  types.Date  `json:"fechaAsignacion" db:"fecha_asignacion"`
	FechaDevolucion  *types.Date `json:"fechaDevolucion" db:"fecha_devolucion"`
	Estado           string      `json:"estado" db:"estado"`
	Observaciones    *string     `json:"observaciones" db:"observaciones"`
	MotivoDevolucion *string     `json:"motivoDevolucion" db:"motivo_devolucion"`
	UsuarioID        *int64      `json:"-" db:"usuario_id"`
	Usuario          *string     `json:"usuario" db:"usuario"`
	CreatedAt        time.Time   `json:"createdAt" db:"created_at"`
}

func (h *Handler) listAsignaciones(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("v.activo_codigo + ' ' + v.empleado + ' ' + v.empleado_cedula + ' ' + coalesce(v.numero_serie, '')", lq.Search)
	database.Eq(w, "v.estado", httpx.QueryString(r, "estado"))
	database.Eq(w, "v.empleado_id", httpx.QueryInt64(r, "empleadoId"))
	if ag := security.AgenciaScope(r.Context()); ag != nil {
		w.Add("v.agencia_id = ?", *ag)
	}
	if dep := security.DepartamentoScope(r.Context()); dep != nil {
		w.Add("v.departamento_id = ?", *dep)
	}
	if cond := inventario.AreaSQL(r.Context(), "x.categoria_id"); cond != "" {
		w.Add("v.activo_id IN (SELECT x.id FROM activos x WHERE "+cond+")", *security.AreaScope(r.Context()))
	}
	database.Eq(w, "v.agencia_id", httpx.QueryInt64(r, "agenciaId"))
	database.Eq(w, "v.activo_id", httpx.QueryInt64(r, "activoId"))
	dateRange(w, r, "v.fecha_asignacion")
	return database.Paginate[Asignacion](r.Context(), h.db, "SELECT v.*", "FROM v_asignaciones v", w,
		lq.OrderBy(map[string]string{"fechaAsignacion": "v.fecha_asignacion", "empleado": "v.empleado",
			"activoCodigo": "v.activo_codigo", "agencia": "v.agencia", "estado": "v.estado"}, "v.id DESC"), lq)
}

type Traslado struct {
	ID                  int64      `json:"id" db:"id"`
	ActivoID            int64      `json:"activoId" db:"activo_id"`
	ActivoCodigo        string     `json:"activoCodigo" db:"activo_codigo"`
	Categoria           string     `json:"categoria" db:"categoria"`
	AgenciaOrigen       string     `json:"agenciaOrigen" db:"agencia_origen"`
	AgenciaDestino      string     `json:"agenciaDestino" db:"agencia_destino"`
	DepartamentoOrigen  *string    `json:"departamentoOrigen" db:"departamento_origen"`
	DepartamentoDestino *string    `json:"departamentoDestino" db:"departamento_destino"`
	EmpleadoOrigen      *string    `json:"empleadoOrigen" db:"empleado_origen"`
	EmpleadoDestino     *string    `json:"empleadoDestino" db:"empleado_destino"`
	Fecha               types.Date `json:"fecha" db:"fecha"`
	Motivo              string     `json:"motivo" db:"motivo"`
	Usuario             *string    `json:"usuario" db:"usuario"`
	CreatedAt           time.Time  `json:"createdAt" db:"created_at"`
	// Envío al que pertenece (nil = traslado inmediato); su estado indica si ya se recibió
	EnvioID     *int64  `json:"envioId" db:"envio_id"`
	EnvioCodigo *string `json:"envioCodigo" db:"envio_codigo"`
	EnvioEstado *string `json:"envioEstado" db:"envio_estado"`
}

const trasladoSelect = `SELECT t.id, t.activo_id, a.codigo AS activo_codigo, c.nombre AS categoria,
	ao.nombre AS agencia_origen, ad.nombre AS agencia_destino, dor.nombre AS departamento_origen, dde.nombre AS departamento_destino,
	eo.nombres + ' ' + eo.apellidos AS empleado_origen, ede.nombres + ' ' + ede.apellidos AS empleado_destino,
	t.fecha, t.motivo, u.nombres + ' ' + u.apellidos AS usuario, t.created_at,
	t.envio_id, env.codigo AS envio_codigo, env.estado AS envio_estado`

const trasladoFrom = `FROM traslados t
	JOIN activos a ON a.id = t.activo_id
	JOIN categorias c ON c.id = a.categoria_id
	JOIN agencias ao ON ao.id = t.agencia_origen_id
	JOIN agencias ad ON ad.id = t.agencia_destino_id
	LEFT JOIN departamentos dor ON dor.id = t.departamento_origen_id
	LEFT JOIN departamentos dde ON dde.id = t.departamento_destino_id
	LEFT JOIN empleados eo ON eo.id = t.empleado_origen_id
	LEFT JOIN empleados ede ON ede.id = t.empleado_destino_id
	LEFT JOIN usuarios u ON u.id = t.usuario_id
	LEFT JOIN envios env ON env.id = t.envio_id`

func (h *Handler) listTraslados(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("a.codigo + ' ' + t.motivo + ' ' + ao.nombre + ' ' + ad.nombre", lq.Search)
	database.Eq(w, "t.activo_id", httpx.QueryInt64(r, "activoId"))
	if v := httpx.QueryInt64(r, "agenciaId"); v != nil {
		w.Add("(t.agencia_origen_id = ? OR t.agencia_destino_id = ?)", *v)
	}
	dateRange(w, r, "t.fecha")
	if ag := security.AgenciaScope(r.Context()); ag != nil {
		w.Add("(t.agencia_origen_id = ? OR t.agencia_destino_id = ?)", *ag)
	}
	if dep := security.DepartamentoScope(r.Context()); dep != nil {
		w.Add("(t.departamento_origen_id = ? OR t.departamento_destino_id = ?)", *dep)
	}
	if cond := inventario.AreaSQL(r.Context(), "a.categoria_id"); cond != "" {
		w.Add(cond, *security.AreaScope(r.Context()))
	}
	return database.Paginate[Traslado](r.Context(), h.db, trasladoSelect, trasladoFrom, w,
		lq.OrderBy(map[string]string{"fecha": "t.fecha", "activoCodigo": "a.codigo"}, "t.id DESC"), lq)
}

// ---- Utilidades ----

func dateRange(w *database.Where, r *http.Request, col string) {
	if v := httpx.QueryString(r, "desde"); v != nil {
		w.Add(col+" >= CAST(? AS DATE)", *v)
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		w.Add(col+" <= CAST(? AS DATE)", *v)
	}
}

func crearActa(ctx context.Context, tx database.Tx, tipo string, fecha types.Date, empID *int64, agenciaID int64, depID *int64,
	obs string, activoIDs []int64) (ActaRef, error) {
	id, numero, err := actas.Crear(ctx, tx, actas.Nueva{Tipo: tipo, Fecha: fecha, EmpleadoID: empID, AgenciaID: agenciaID,
		DepartamentoID: depID, Observaciones: obs, ActivoIDs: activoIDs})
	return ActaRef{ID: id, Numero: numero}, err
}

// conComponentes agrega al acta los componentes instalados en los equipos.
func conComponentes(ctx context.Context, tx database.Tx, ids []int64) []int64 {
	out := append([]int64{}, ids...)
	for _, id := range ids {
		if comps, err := inventario.Componentes(ctx, tx, id); err == nil {
			out = append(out, comps...)
		}
	}
	return out
}

func unique(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func ptrEq(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
