// Package auth implementa inicio de sesión, renovación de sesión (refresh token
// rotativo en cookie httpOnly), cierre de sesión y cambio de contraseña.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/security"
)

const refreshCookie = "sgat_refresh"

type Options struct {
	RefreshTTL   time.Duration
	CookieSecure bool
	CookieDomain string
	MaxFails     int
	Lockout      time.Duration
}

type Handler struct {
	db     *database.DB
	tokens *security.TokenManager
	opt    Options
}

func NewHandler(db *database.DB, tokens *security.TokenManager, opt Options) *Handler {
	return &Handler{db: db, tokens: tokens, opt: opt}
}

// Routes registra /login, /refresh y /logout (públicas, con límite de intentos)
// y /me y /change-password (requieren access token).
func (h *Handler) Routes(r chi.Router, loginLimiter, authenticate func(http.Handler) http.Handler) {
	r.With(loginLimiter).Post("/login", h.login)
	r.With(loginLimiter).Post("/refresh", h.refresh)
	r.Post("/logout", h.logout)
	r.Group(func(r chi.Router) {
		r.Use(authenticate)
		r.Get("/me", h.me)
		r.Post("/change-password", h.changePassword)
	})
}

// ---- DTOs ----

type loginRequest struct {
	Username string `json:"username" validate:"required,max=150"`
	Password string `json:"password" validate:"required,max=72"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword" validate:"required"`
	NewPassword     string `json:"newPassword" validate:"required,password,nefield=CurrentPassword"`
}

type User struct {
	ID                  int64   `json:"id"`
	Username            string  `json:"username"`
	Email               string  `json:"email"`
	Nombres             string  `json:"nombres"`
	Apellidos           string  `json:"apellidos"`
	Rol                 string  `json:"rol"`
	RolNombre           string  `json:"rolNombre"`
	AgenciaID           *int64  `json:"agenciaId"`
	Agencia             *string `json:"agencia"`
	DepartamentoID      *int64  `json:"departamentoId"`
	Departamento        *string `json:"departamento"`
	DebeCambiarPassword bool    `json:"debeCambiarPassword"`
}

func (u User) principal() security.Principal {
	return security.Principal{ID: u.ID, Username: u.Username, Nombre: u.Nombres + " " + u.Apellidos, Rol: u.Rol,
		AgenciaID: u.AgenciaID, DepartamentoID: u.DepartamentoID}
}

type sessionResponse struct {
	AccessToken string    `json:"accessToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
	User        User      `json:"user"`
}

type userRecord struct {
	User
	PasswordHash   string
	Habilitado     bool
	Intentos       int
	BloqueadoHasta *time.Time
}

// "rol" es el PERFIL del rol (SUPERADMIN, ADMIN, ENCARGADO...): es lo que usan los permisos del backend y del
// frontend, así un rol creado por el superadmin funciona igual que el rol del sistema de su perfil.
// Un usuario cuyo rol está deshabilitado no puede ingresar.
const userSelect = `
	SELECT u.id, u.username, u.email, u.nombres, u.apellidos, r.perfil, r.nombre, u.agencia_id, a.nombre,
	       u.departamento_id, d.nombre,
	       u.debe_cambiar_password, u.password_hash,
	       CAST(CASE WHEN u.habilitado = 1 AND r.habilitado = 1 THEN 1 ELSE 0 END AS BIT),
	       u.intentos_fallidos, u.bloqueado_hasta
	FROM usuarios u
	JOIN roles r ON r.id = u.rol_id
	LEFT JOIN agencias a ON a.id = u.agencia_id
	LEFT JOIN departamentos d ON d.id = u.departamento_id`

func scanUser(row *database.Row) (userRecord, error) {
	var u userRecord
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Nombres, &u.Apellidos, &u.Rol, &u.RolNombre, &u.AgenciaID,
		&u.Agencia, &u.DepartamentoID, &u.Departamento, &u.DebeCambiarPassword, &u.PasswordHash, &u.Habilitado, &u.Intentos,
		&u.BloqueadoHasta)
	return u, err
}

func (h *Handler) findUser(ctx context.Context, q database.Querier, where string, arg any) (userRecord, error) {
	return scanUser(q.QueryRow(ctx, userSelect+" WHERE "+where, arg))
}

// ---- Handlers ----

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Bind(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	ctx := r.Context()
	invalid := apperr.Unauthorized("Usuario o contraseña incorrectos")

	u, err := h.findUser(ctx, h.db, "u.username = $1 OR u.email = $1", req.Username)
	if errors.Is(err, database.ErrNoRows) {
		security.CheckDummy(req.Password)
		_ = audit.Log(ctx, h.db, audit.Entry{Accion: audit.LoginFallido, Entidad: "usuarios",
			UsuarioNombre: req.Username, Descripcion: "Usuario inexistente"})
		httpx.Error(w, r, invalid)
		return
	}
	if err != nil {
		httpx.Error(w, r, apperr.Internal(err))
		return
	}

	if u.BloqueadoHasta != nil && u.BloqueadoHasta.After(time.Now()) {
		httpx.Error(w, r, apperr.Forbidden(fmt.Sprintf("Usuario bloqueado por intentos fallidos. Intente después de las %s",
			u.BloqueadoHasta.Local().Format("15:04"))))
		return
	}
	if !security.CheckPassword(u.PasswordHash, req.Password) {
		h.registerFailure(ctx, u)
		httpx.Error(w, r, invalid)
		return
	}
	if !u.Habilitado {
		httpx.Error(w, r, apperr.Forbidden("El usuario está deshabilitado, contacte al administrador"))
		return
	}

	var resp sessionResponse
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE usuarios SET intentos_fallidos = 0, bloqueado_hasta = NULL, ultimo_acceso = SYSDATETIMEOFFSET()
		                           WHERE id = $1`, u.ID); err != nil {
			return err
		}
		resp, err = h.openSession(ctx, tx, w, u.User)
		if err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Login, Entidad: "usuarios", EntidadID: &u.ID,
			UsuarioID: &u.ID, UsuarioNombre: u.Nombres + " " + u.Apellidos, Descripcion: "Inicio de sesión"})
	})
	if err != nil {
		httpx.Error(w, r, apperr.Internal(err))
		return
	}
	httpx.OK(w, resp)
}

func (h *Handler) registerFailure(ctx context.Context, u userRecord) {
	intentos := u.Intentos + 1
	var bloqueo *time.Time
	if intentos >= h.opt.MaxFails {
		t := time.Now().Add(h.opt.Lockout)
		bloqueo, intentos = &t, 0
	}
	_, _ = h.db.Exec(ctx, `UPDATE usuarios SET intentos_fallidos = $2, bloqueado_hasta = $3 WHERE id = $1`,
		u.ID, intentos, bloqueo)
	desc := "Contraseña incorrecta"
	if bloqueo != nil {
		desc += " - usuario bloqueado temporalmente"
	}
	_ = audit.Log(ctx, h.db, audit.Entry{Accion: audit.LoginFallido, Entidad: "usuarios", EntidadID: &u.ID,
		UsuarioID: &u.ID, UsuarioNombre: u.Username, Descripcion: desc})
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cookie, err := r.Cookie(refreshCookie)
	if err != nil || cookie.Value == "" {
		httpx.Error(w, r, apperr.Unauthorized("No hay una sesión activa"))
		return
	}
	hash := security.HashToken(cookie.Value)

	var resp sessionResponse
	var revocarTodas *int64
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		var (
			sessionID, userID int64
			expira            time.Time
			revocada          *time.Time
		)
		err := tx.QueryRow(ctx, `SELECT id, usuario_id, expira_en, revocada_en FROM sesiones WITH (UPDLOCK, ROWLOCK) WHERE token_hash = $1`,
			hash).Scan(&sessionID, &userID, &expira, &revocada)
		if errors.Is(err, database.ErrNoRows) {
			return apperr.Unauthorized("La sesión no es válida")
		}
		if err != nil {
			return err
		}
		if revocada != nil {
			// Reutilización de un token ya rotado: posible robo, se cierran todas las sesiones.
			// Se ejecuta después de la tx (que hará rollback al devolver el error y libera el bloqueo de la fila).
			revocarTodas = &userID
			return apperr.Unauthorized("La sesión fue cerrada, inicie sesión nuevamente")
		}
		if expira.Before(time.Now()) {
			return apperr.Unauthorized("La sesión expiró, inicie sesión nuevamente")
		}

		u, err := h.findUser(ctx, tx, "u.id = $1", userID)
		if err != nil {
			return err
		}
		if !u.Habilitado {
			return apperr.Forbidden("El usuario está deshabilitado")
		}
		if _, err := tx.Exec(ctx, `UPDATE sesiones SET revocada_en = SYSDATETIMEOFFSET() WHERE id = $1`, sessionID); err != nil {
			return err
		}
		resp, err = h.openSession(ctx, tx, w, u.User)
		return err
	})
	if revocarTodas != nil {
		_, _ = h.db.Exec(ctx, `UPDATE sesiones SET revocada_en = SYSDATETIMEOFFSET() WHERE usuario_id = $1 AND revocada_en IS NULL`,
			*revocarTodas)
	}
	if err != nil {
		var appErr *apperr.Error
		if errors.As(err, &appErr) && appErr.Status == http.StatusUnauthorized {
			h.clearCookie(w)
		}
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, resp)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(refreshCookie); err == nil && cookie.Value != "" {
		var userID int64
		err := h.db.QueryRow(r.Context(), `UPDATE sesiones SET revocada_en = SYSDATETIMEOFFSET()
			OUTPUT INSERTED.usuario_id WHERE token_hash = $1 AND revocada_en IS NULL`, security.HashToken(cookie.Value)).Scan(&userID)
		if err == nil {
			_ = audit.Log(r.Context(), h.db, audit.Entry{Accion: audit.Logout, Entidad: "usuarios", EntidadID: &userID,
				UsuarioID: &userID, Descripcion: "Cierre de sesión"})
		}
	}
	h.clearCookie(w)
	httpx.Message(w, "Sesión cerrada")
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	p, _ := security.PrincipalFrom(r.Context())
	u, err := h.findUser(r.Context(), h.db, "u.id = $1", p.ID)
	if err != nil {
		httpx.Error(w, r, apperr.FromDB(err, "Usuario no encontrado"))
		return
	}
	httpx.OK(w, u.User)
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if err := httpx.Bind(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	ctx := r.Context()
	p, _ := security.PrincipalFrom(ctx)

	u, err := h.findUser(ctx, h.db, "u.id = $1", p.ID)
	if err != nil {
		httpx.Error(w, r, apperr.FromDB(err, "Usuario no encontrado"))
		return
	}
	if !security.CheckPassword(u.PasswordHash, req.CurrentPassword) {
		httpx.Error(w, r, apperr.Validation(map[string]string{"currentPassword": "La contraseña actual es incorrecta"}))
		return
	}
	hash, err := security.HashPassword(req.NewPassword)
	if err != nil {
		httpx.Error(w, r, apperr.Internal(err))
		return
	}

	currentHash := ""
	if c, err := r.Cookie(refreshCookie); err == nil {
		currentHash = security.HashToken(c.Value)
	}
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE usuarios SET password_hash = $2, debe_cambiar_password = 0 WHERE id = $1`,
			p.ID, hash); err != nil {
			return err
		}
		// Cierra las demás sesiones abiertas del usuario
		if _, err := tx.Exec(ctx, `UPDATE sesiones SET revocada_en = SYSDATETIMEOFFSET()
			WHERE usuario_id = $1 AND revocada_en IS NULL AND token_hash <> $2`, p.ID, currentHash); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.CambioPassword, Entidad: "usuarios", EntidadID: &p.ID,
			Descripcion: "Cambio de contraseña"})
	})
	if err != nil {
		httpx.Error(w, r, apperr.Internal(err))
		return
	}
	httpx.Message(w, "Contraseña actualizada correctamente")
}

// openSession crea el refresh token, lo guarda hasheado y emite el access token.
func (h *Handler) openSession(ctx context.Context, tx database.Tx, w http.ResponseWriter, u User) (sessionResponse, error) {
	token, hash, err := security.NewRefreshToken()
	if err != nil {
		return sessionResponse{}, err
	}
	client := security.ClientFrom(ctx)
	expira := time.Now().Add(h.opt.RefreshTTL)
	if _, err := tx.Exec(ctx, `INSERT INTO sesiones (usuario_id, token_hash, expira_en, ip, user_agent)
		VALUES ($1, $2, $3, NULLIF(CAST($4 AS NVARCHAR(45)), N''), NULLIF(CAST($5 AS NVARCHAR(300)), N''))`, u.ID, hash, expira, client.IP, client.UserAgent); err != nil {
		return sessionResponse{}, err
	}
	access, exp, err := h.tokens.Generate(u.principal())
	if err != nil {
		return sessionResponse{}, err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookie,
		Value:    token,
		Path:     "/api/v1/auth",
		Domain:   h.opt.CookieDomain,
		Expires:  expira,
		MaxAge:   int(h.opt.RefreshTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.opt.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	return sessionResponse{AccessToken: access, ExpiresAt: exp, User: u}, nil
}

func (h *Handler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: "", Path: "/api/v1/auth", Domain: h.opt.CookieDomain,
		MaxAge: -1, HttpOnly: true, Secure: h.opt.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
}
