// Package sistema expone la configuración general y la consulta de auditoría.
package sistema

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/middleware"
	"sgat-api/internal/security"
)

type Parametro struct {
	Clave       string    `json:"clave" db:"clave"`
	Valor       string    `json:"valor" db:"valor"`
	Descripcion *string   `json:"descripcion" db:"descripcion"`
	Grupo       string    `json:"grupo" db:"grupo"`
	UpdatedBy   *int64    `json:"-" db:"updated_by"`
	UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
}

type Auditoria struct {
	ID              int64           `json:"id" db:"id"`
	UsuarioID       *int64          `json:"usuarioId" db:"usuario_id"`
	UsuarioNombre   *string         `json:"usuarioNombre" db:"usuario_nombre"`
	Accion          string          `json:"accion" db:"accion"`
	Entidad         string          `json:"entidad" db:"entidad"`
	EntidadID       *int64          `json:"entidadId" db:"entidad_id"`
	Descripcion     *string         `json:"descripcion" db:"descripcion"`
	DatosAnteriores json.RawMessage `json:"datosAnteriores" db:"datos_anteriores"`
	DatosNuevos     json.RawMessage `json:"datosNuevos" db:"datos_nuevos"`
	IP              *string         `json:"ip" db:"ip"`
	UserAgent       *string         `json:"userAgent" db:"user_agent"`
	CreatedAt       time.Time       `json:"createdAt" db:"created_at"`
}

type Handler struct{ db *database.DB }

func NewHandler(db *database.DB) *Handler { return &Handler{db: db} }

func (h *Handler) ConfiguracionRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listConfig))
	r.With(middleware.SuperAdminOnly).Put("/", httpx.Handle(h.updateConfig))
}

// AuditoriaRoutes debe montarse con el middleware SuperAdminOnly.
func (h *Handler) AuditoriaRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.listAuditoria))
	r.Get("/{id}", httpx.Handle(h.getAuditoria))
}

func (h *Handler) listConfig(r *http.Request) (any, error) {
	return database.List[Parametro](r.Context(), h.db, "SELECT * FROM configuracion ORDER BY grupo, clave")
}

type configInput struct {
	Valores map[string]string `json:"valores" validate:"required,min=1,max=50,dive,keys,max=60,endkeys,max=2000"`
}

func (h *Handler) updateConfig(r *http.Request) (any, error) {
	var in configInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	err := database.WithTx(ctx, h.db, func(tx database.Tx) error {
		antes := map[string]string{}
		for clave, valor := range in.Valores {
			var previo string
			// OUTPUT ... INTO: la tabla tiene trigger de updated_at (SQL Server no admite OUTPUT directo)
			err := tx.QueryRow(ctx, `DECLARE @previo TABLE (valor NVARCHAR(MAX));
				UPDATE configuracion SET valor = $2, updated_by = $3 OUTPUT DELETED.valor INTO @previo WHERE clave = $1;
				SELECT valor FROM @previo`, clave, valor, security.UserID(ctx)).Scan(&previo)
			if err != nil {
				return apperr.FromDB(err, "Parámetro desconocido: "+clave)
			}
			antes[clave] = previo
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Actualizar, Entidad: "configuracion",
			Descripcion: "Actualización de parámetros", Antes: antes, Despues: in.Valores})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "")
	}
	return h.listConfig(r)
}

const auditoriaSelect = `SELECT a.id, a.usuario_id, a.usuario_nombre, a.accion, a.entidad, a.entidad_id, a.descripcion,
	CAST(NULL AS NVARCHAR(MAX)) AS datos_anteriores, CAST(NULL AS NVARCHAR(MAX)) AS datos_nuevos, a.ip, a.user_agent, a.created_at`

func (h *Handler) listAuditoria(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("coalesce(a.usuario_nombre, '') + ' ' + a.entidad + ' ' + coalesce(a.descripcion, '')", lq.Search)
	database.Eq(w, "a.accion", httpx.QueryString(r, "accion"))
	database.Eq(w, "a.entidad", httpx.QueryString(r, "entidad"))
	database.Eq(w, "a.usuario_id", httpx.QueryInt64(r, "usuarioId"))
	if v := httpx.QueryString(r, "desde"); v != nil {
		w.Add("CAST(a.created_at AS DATE) >= CAST(? AS DATE)", *v)
	}
	if v := httpx.QueryString(r, "hasta"); v != nil {
		w.Add("CAST(a.created_at AS DATE) <= CAST(? AS DATE)", *v)
	}
	// El detalle (JSON antes/después) se obtiene por id para no cargar el listado
	return database.Paginate[Auditoria](r.Context(), h.db, auditoriaSelect, "FROM auditoria a", w,
		lq.OrderBy(map[string]string{"createdAt": "a.created_at", "accion": "a.accion", "entidad": "a.entidad"}, "a.id DESC"), lq)
}

func (h *Handler) getAuditoria(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	a, err := database.Get[Auditoria](r.Context(), h.db, "SELECT * FROM auditoria WHERE id = $1", id)
	return a, apperr.FromDB(err, "Registro de auditoría no encontrado")
}
