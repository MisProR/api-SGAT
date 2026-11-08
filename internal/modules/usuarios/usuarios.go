// Package usuarios permite al superadministrador gestionar los usuarios del sistema.
package usuarios

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/crud"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/security"
)

type Usuario struct {
	ID                  int64      `json:"id" db:"id"`
	Username            string     `json:"username" db:"username"`
	Email               string     `json:"email" db:"email"`
	Nombres             string     `json:"nombres" db:"nombres"`
	Apellidos           string     `json:"apellidos" db:"apellidos"`
	RolID               int16      `json:"rolId" db:"rol_id"`
	Rol                 string     `json:"rol" db:"rol"`
	RolNombre           string     `json:"rolNombre" db:"rol_nombre"`
	AgenciaID           *int64     `json:"agenciaId" db:"agencia_id"`
	Agencia             *string    `json:"agencia" db:"agencia"`
	DepartamentoID      *int64     `json:"departamentoId" db:"departamento_id"`
	Departamento        *string    `json:"departamento" db:"departamento"`
	Habilitado          bool       `json:"habilitado" db:"habilitado"`
	DebeCambiarPassword bool       `json:"debeCambiarPassword" db:"debe_cambiar_password"`
	Bloqueado           bool       `json:"bloqueado" db:"bloqueado"`
	UltimoAcceso        *time.Time `json:"ultimoAcceso" db:"ultimo_acceso"`
	CreatedAt           time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt           time.Time  `json:"updatedAt" db:"updated_at"`
}

// Rol: opción del selector de roles al crear o editar un usuario (solo roles habilitados).
type Rol struct {
	ID          int16   `json:"id" db:"id"`
	Codigo      string  `json:"codigo" db:"codigo"`
	Nombre      string  `json:"nombre" db:"nombre"`
	Descripcion *string `json:"descripcion" db:"descripcion"`
	Perfil      string  `json:"perfil" db:"perfil"`
}

type input struct {
	Username   string  `json:"username" validate:"required,min=3,max=50"`
	Email      string  `json:"email" validate:"required,email,max=150"`
	Nombres    string  `json:"nombres" validate:"required,max=100"`
	Apellidos  string  `json:"apellidos" validate:"required,max=100"`
	RolID      int16   `json:"rolId" validate:"required,gt=0"`
	AgenciaID  *int64  `json:"agenciaId" validate:"omitempty,gt=0"`
	// Solo para los roles con perfil de jefe de departamento
	DepartamentoID *int64 `json:"departamentoId" validate:"omitempty,gt=0"`
	Habilitado *bool   `json:"habilitado"`
	Password   *string `json:"password" validate:"omitempty,password"`
}

type resetInput struct {
	Password string `json:"password" validate:"required,password"`
}

type Handler struct {
	db  *database.DB
	res crud.Resource[Usuario]
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{db: db, res: crud.Resource[Usuario]{Table: "usuarios", NotFound: "Usuario no encontrado", Find: find,
		Label: func(u Usuario) string { return "Usuario " + u.Username }}}
}

// Routes debe montarse con el middleware SuperAdminOnly.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Get("/roles", httpx.Handle(h.roles))
	r.Post("/", httpx.HandleCreated(h.create))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Put("/{id}", httpx.Handle(h.update))
	r.Post("/{id}/reset-password", httpx.Handle(h.resetPassword))
	r.Post("/{id}/desbloquear", httpx.Handle(h.desbloquear))
}

// "rol" es el perfil del rol (define los permisos); "rol_nombre", el nombre que le dio el superadmin.
const selectSQL = `SELECT u.id, u.username, u.email, u.nombres, u.apellidos, u.rol_id, r.perfil AS rol, r.nombre AS rol_nombre,
	u.agencia_id, a.nombre AS agencia, u.departamento_id, d.nombre AS departamento, u.habilitado, u.debe_cambiar_password,
	CAST(CASE WHEN u.bloqueado_hasta > SYSDATETIMEOFFSET() THEN 1 ELSE 0 END AS BIT) AS bloqueado, u.ultimo_acceso, u.created_at, u.updated_at`

const fromSQL = `FROM usuarios u JOIN roles r ON r.id = u.rol_id LEFT JOIN agencias a ON a.id = u.agencia_id
	LEFT JOIN departamentos d ON d.id = u.departamento_id`

func find(ctx context.Context, q database.Querier, id int64) (Usuario, error) {
	u, err := database.Get[Usuario](ctx, q, selectSQL+" "+fromSQL+" WHERE u.id = $1", id)
	return u, apperr.FromDB(err, "Usuario no encontrado")
}

func (h *Handler) list(r *http.Request) (any, error) {
	lq := httpx.ParseListQuery(r)
	w := &database.Where{}
	w.Search("u.username + ' ' + u.email + ' ' + u.nombres + ' ' + u.apellidos", lq.Search)
	database.Eq(w, "u.rol_id", httpx.QueryInt64(r, "rolId"))
	database.Eq(w, "r.perfil", httpx.QueryString(r, "rol"))
	database.Eq(w, "u.habilitado", httpx.QueryBool(r, "habilitado"))
	return database.Paginate[Usuario](r.Context(), h.db, selectSQL, fromSQL, w,
		lq.OrderBy(map[string]string{"username": "u.username", "nombres": "u.nombres", "rol": "r.nombre",
			"ultimoAcceso": "u.ultimo_acceso"}, "u.username"), lq)
}

// roles: opciones para el selector de rol del formulario de usuarios (solo los habilitados).
func (h *Handler) roles(r *http.Request) (any, error) {
	return database.List[Rol](r.Context(), h.db,
		"SELECT id, codigo, nombre, descripcion, perfil FROM roles WHERE habilitado = 1 ORDER BY id")
}

func (h *Handler) get(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return find(r.Context(), h.db, id)
}

// validarAgencia: los roles con perfil de encargado operan una agencia y los de jefe un departamento de una
// agencia, así que deben tenerlos asignados. Los demás perfiles no usan departamento.
// Al editar, se permite conservar un rol que se deshabilitó después (actualRolID).
func validarAgencia(ctx context.Context, q database.Querier, in *input, actualRolID int16) error {
	var perfil string
	var habilitado bool
	err := q.QueryRow(ctx, `SELECT perfil, habilitado FROM roles WHERE id = $1`, in.RolID).Scan(&perfil, &habilitado)
	if errors.Is(err, database.ErrNoRows) || (err == nil && !habilitado && in.RolID != actualRolID) {
		return apperr.Validation(map[string]string{"rolId": "Seleccione un rol válido y habilitado"})
	}
	if err != nil {
		return err
	}
	esJefe := perfil == security.RoleJefe
	if (perfil == security.RoleEncargado || esJefe) && in.AgenciaID == nil {
		return apperr.Validation(map[string]string{"agenciaId": "Seleccione la agencia a su cargo"})
	}
	if esJefe && in.DepartamentoID == nil {
		return apperr.Validation(map[string]string{"departamentoId": "Seleccione el departamento a cargo del jefe"})
	}
	if !esJefe {
		in.DepartamentoID = nil
	}
	return nil
}

func (h *Handler) create(r *http.Request) (any, error) {
	var in input
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	if err := validarAgencia(r.Context(), h.db, &in, 0); err != nil {
		return nil, err
	}
	if in.Password == nil {
		return nil, apperr.Validation(map[string]string{"password": "Es obligatorio"})
	}
	hash, err := security.HashPassword(*in.Password)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return h.res.Create(r.Context(), h.db, func(tx database.Tx) (id int64, err error) {
		err = tx.QueryRow(r.Context(), `INSERT INTO usuarios (rol_id, username, email, nombres, apellidos, password_hash,
			agencia_id, habilitado, debe_cambiar_password, departamento_id)
			VALUES ($1, lower($2), lower($3), $4, $5, $6, $7, coalesce($8, 1), 1, $9);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`, in.RolID, in.Username, in.Email, in.Nombres, in.Apellidos, hash, in.AgenciaID, in.Habilitado,
			in.DepartamentoID).Scan(&id)
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
	me, _ := security.PrincipalFrom(r.Context())
	return h.res.Update(r.Context(), h.db, id, func(tx database.Tx, before Usuario) error {
		if err := validarAgencia(r.Context(), tx, &in, before.RolID); err != nil {
			return err
		}
		if id == me.ID && (in.RolID != before.RolID || (in.Habilitado != nil && !*in.Habilitado)) {
			return apperr.Conflict("No puede cambiar su propio rol ni deshabilitar su propio usuario")
		}
		if before.Rol == security.RoleSuperAdmin && (in.RolID != before.RolID || (in.Habilitado != nil && !*in.Habilitado)) {
			if err := ensureOtherSuperAdmin(r.Context(), tx, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(r.Context(), `UPDATE usuarios SET username = lower($2), email = lower($3), nombres = $4, apellidos = $5,
			rol_id = $6, agencia_id = $7, habilitado = coalesce($8, habilitado), departamento_id = $9 WHERE id = $1`,
			id, in.Username, in.Email, in.Nombres, in.Apellidos, in.RolID, in.AgenciaID, in.Habilitado, in.DepartamentoID)
		if err != nil {
			return err
		}
		// Al deshabilitar, se cierran sus sesiones
		if in.Habilitado != nil && !*in.Habilitado {
			_, err = tx.Exec(r.Context(), `UPDATE sesiones SET revocada_en = SYSDATETIMEOFFSET() WHERE usuario_id = $1 AND revocada_en IS NULL`, id)
		}
		return err
	})
}

// ensureOtherSuperAdmin evita dejar el sistema sin superadministradores habilitados.
func ensureOtherSuperAdmin(ctx context.Context, tx database.Tx, exceptID int64) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM usuarios u JOIN roles r ON r.id = u.rol_id
		WHERE r.perfil = 'SUPERADMIN' AND r.habilitado = 1 AND u.habilitado = 1 AND u.id <> $1`, exceptID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return apperr.Conflict("Debe existir al menos otro superadministrador habilitado")
	}
	return nil
}

func (h *Handler) resetPassword(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	var in resetInput
	if err := httpx.Bind(r, &in); err != nil {
		return nil, err
	}
	hash, err := security.HashPassword(in.Password)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	ctx := r.Context()
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		u, err := find(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE usuarios SET password_hash = $2, debe_cambiar_password = 1, intentos_fallidos = 0,
			bloqueado_hasta = NULL WHERE id = $1`, id, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sesiones SET revocada_en = SYSDATETIMEOFFSET() WHERE usuario_id = $1 AND revocada_en IS NULL`, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.CambioPassword, Entidad: "usuarios", EntidadID: &id,
			Descripcion: "Restablecimiento de contraseña de " + u.Username})
	})
	if err != nil {
		return nil, apperr.FromDB(err, "Usuario no encontrado")
	}
	return httpx.MessageResult("Contraseña restablecida. El usuario deberá cambiarla al ingresar"), nil
}

func (h *Handler) desbloquear(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	return h.res.Update(r.Context(), h.db, id, func(tx database.Tx, _ Usuario) error {
		_, err := tx.Exec(r.Context(), `UPDATE usuarios SET intentos_fallidos = 0, bloqueado_hasta = NULL WHERE id = $1`, id)
		return err
	})
}
