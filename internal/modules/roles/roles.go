// Package roles permite al superadministrador crear, editar y eliminar los roles de acceso.
//
// Cada rol tiene un PERFIL (SUPERADMIN, ADMIN, ENCARGADO, JEFE, PROVEEDURIA, VERIFICADOR) que define lo que
// pueden hacer sus usuarios: al iniciar sesión el token lleva el perfil del rol, así que un rol nuevo funciona
// igual que el rol del sistema de su perfil, con su propio nombre. Los 6 roles del sistema (es_sistema = 1) no
// se pueden eliminar ni cambiar de código o perfil.
package roles

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/crud"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
)

type Rol struct {
	ID            int16     `json:"id" db:"id"`
	Codigo        string    `json:"codigo" db:"codigo"`
	Nombre        string    `json:"nombre" db:"nombre"`
	Descripcion   *string   `json:"descripcion" db:"descripcion"`
	Perfil        string    `json:"perfil" db:"perfil"`
	EsSistema     bool      `json:"esSistema" db:"es_sistema"`
	Habilitado    bool      `json:"habilitado" db:"habilitado"`
	TotalUsuarios int64     `json:"totalUsuarios" db:"total_usuarios"`
	CreatedAt     time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt     time.Time `json:"updatedAt" db:"updated_at"`
}

type input struct {
	Codigo      string  `json:"codigo" validate:"required,min=2,max=20"`
	Nombre      string  `json:"nombre" validate:"required,max=60"`
	Descripcion *string `json:"descripcion" validate:"omitempty,max=250"`
	Perfil      string  `json:"perfil" validate:"required,oneof=SUPERADMIN ADMIN ENCARGADO JEFE PROVEEDURIA VERIFICADOR"`
	Habilitado  *bool   `json:"habilitado"`
}

var codigoRe = regexp.MustCompile(`^[A-Z0-9_]+$`)

// normalizar deja el código en mayúsculas con guion bajo en lugar de espacios y lo valida.
func (in *input) normalizar() error {
	in.Codigo = strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(in.Codigo)), " ", "_")
	in.Nombre = strings.TrimSpace(in.Nombre)
	if !codigoRe.MatchString(in.Codigo) {
		return apperr.Validation(map[string]string{"codigo": "Solo letras mayúsculas sin tildes, números y guion bajo"})
	}
	return nil
}

type Handler struct {
	db  *database.DB
	res crud.Resource[Rol]
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{db: db, res: crud.Resource[Rol]{Table: "roles", NotFound: "Rol no encontrado", Find: find,
		Label: func(r Rol) string { return "Rol " + r.Nombre }}}
}

// Routes debe montarse con el middleware SuperAdminOnly.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.HandleCreated(h.create))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Put("/{id}", httpx.Handle(h.update))
	r.Delete("/{id}", httpx.Handle(h.delete))
}

const selectSQL = `SELECT r.id, r.codigo, r.nombre, r.descripcion, r.perfil, r.es_sistema, r.habilitado,
	(SELECT COUNT_BIG(*) FROM usuarios u WHERE u.rol_id = r.id) AS total_usuarios, r.created_at, r.updated_at`

const fromSQL = `FROM roles r`

func find(ctx context.Context, q database.Querier, id int64) (Rol, error) {
	rol, err := database.Get[Rol](ctx, q, selectSQL+" "+fromSQL+" WHERE r.id = $1", id)
	return rol, apperr.FromDB(err, "Rol no encontrado")
}

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("r.codigo + ' ' + r.nombre + ' ' + coalesce(r.descripcion, '')", lq.Search)
	database.Eq(w, "r.perfil", httpx.QueryString(r, "perfil"))
	database.Eq(w, "r.habilitado", httpx.QueryBool(r, "habilitado"))
	return database.Paginate[Rol](r.Context(), h.db, selectSQL, fromSQL, w,
		lq.OrderBy(map[string]string{"codigo": "r.codigo", "nombre": "r.nombre", "perfil": "r.perfil"}, "r.id"), lq)
}

func (h *Handler) get(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return find(r.Context(), h.db, id)
}

func (h *Handler) create(r *http.Request) (any, error) {
	var in input
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	if err := in.normalizar(); err != nil {
		return nil, err
	}
	return h.res.Create(r.Context(), h.db, func(tx database.Tx) (id int64, err error) {
		err = tx.QueryRow(r.Context(), `INSERT INTO roles (codigo, nombre, descripcion, perfil, habilitado, es_sistema)
			VALUES ($1, $2, $3, $4, coalesce($5, 1), 0);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`, in.Codigo, in.Nombre, in.Descripcion, in.Perfil, in.Habilitado).Scan(&id)
		return
	})
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
	if err := in.normalizar(); err != nil {
		return nil, err
	}
	ctx := r.Context()
	return h.res.Update(ctx, h.db, id, func(tx database.Tx, before Rol) error {
		// Los roles del sistema conservan su código y su perfil (el código lo usan los scripts y las restricciones)
		if before.EsSistema && (in.Codigo != before.Codigo || in.Perfil != before.Perfil) {
			return apperr.Conflict("Es un rol del sistema: solo puede cambiar su nombre, descripción y estado")
		}
		habilitado := before.Habilitado
		if in.Habilitado != nil {
			habilitado = *in.Habilitado
		}
		if _, err := tx.Exec(ctx, `UPDATE roles SET codigo = $2, nombre = $3, descripcion = $4, perfil = $5, habilitado = $6
			WHERE id = $1`, id, in.Codigo, in.Nombre, in.Descripcion, in.Perfil, habilitado); err != nil {
			return err
		}
		if err := ensureSuperAdmin(ctx, tx); err != nil {
			return err
		}
		// Si cambian los permisos del rol (perfil) o se deshabilita, sus usuarios deben volver a iniciar sesión
		if in.Perfil != before.Perfil || (before.Habilitado && !habilitado) {
			_, err := tx.Exec(ctx, `UPDATE s SET revocada_en = SYSDATETIMEOFFSET() FROM sesiones s
				JOIN usuarios u ON u.id = s.usuario_id WHERE u.rol_id = $1 AND s.revocada_en IS NULL`, id)
			return err
		}
		return nil
	})
}

func (h *Handler) delete(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	rol, err := find(r.Context(), h.db, id)
	if err != nil {
		return nil, err
	}
	if rol.EsSistema {
		return nil, apperr.Conflict("Los roles del sistema no se pueden eliminar (puede deshabilitarlos)")
	}
	if rol.TotalUsuarios > 0 {
		return nil, apperr.Conflict("El rol tiene usuarios asignados: cámbielos de rol antes de eliminarlo")
	}
	if err := h.res.Delete(r.Context(), h.db, id); err != nil {
		return nil, err
	}
	return httpx.MessageResult("Rol eliminado"), nil
}

// ensureSuperAdmin evita dejar el sistema sin ningún superadministrador habilitado que pueda ingresar.
func ensureSuperAdmin(ctx context.Context, tx database.Tx) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM usuarios u JOIN roles r ON r.id = u.rol_id
		WHERE r.perfil = 'SUPERADMIN' AND r.habilitado = 1 AND u.habilitado = 1`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return apperr.Conflict("Debe quedar al menos un superadministrador habilitado")
	}
	return nil
}
