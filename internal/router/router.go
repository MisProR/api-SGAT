// Package router arma el árbol de rutas de la API y sus middlewares.
package router

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"sgat-api/internal/apperr"
	"sgat-api/internal/config"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	mw "sgat-api/internal/middleware"
	"sgat-api/internal/modules/actas"
	"sgat-api/internal/modules/activos"
	"sgat-api/internal/modules/auth"
	"sgat-api/internal/modules/averias"
	"sgat-api/internal/modules/bodega"
	"sgat-api/internal/modules/catalogos"
	"sgat-api/internal/modules/empleados"
	"sgat-api/internal/modules/mantenimientos"
	"sgat-api/internal/modules/movimientos"
	"sgat-api/internal/modules/organizacion"
	"sgat-api/internal/modules/proveeduria"
	"sgat-api/internal/modules/reemplazos"
	"sgat-api/internal/modules/roles"
	"sgat-api/internal/modules/verificaciones"
	"sgat-api/internal/modules/reportes"
	"sgat-api/internal/modules/sistema"
	"sgat-api/internal/modules/usuarios"
	"sgat-api/internal/security"
)

func New(cfg config.Config, db *database.DB, tokens *security.TokenManager) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(mw.Logger)
	r.Use(mw.Recoverer)
	r.Use(mw.SecurityHeaders)
	r.Use(mw.ClientInfo)
	r.Use(chimw.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-Id"},
		ExposedHeaders:   []string{"Content-Disposition"},
		AllowCredentials: true,
		MaxAge:           600,
	}))

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, r, apperr.NotFound("Ruta no encontrada"))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, r, apperr.New(http.StatusMethodNotAllowed, apperr.CodeBadRequest, "Método no permitido"))
	})

	authH := auth.NewHandler(db, tokens, auth.Options{
		RefreshTTL:   cfg.RefreshTokenTTL,
		CookieSecure: cfg.CookieSecure,
		CookieDomain: cfg.CookieDomain,
		MaxFails:     cfg.MaxLoginFails,
		Lockout:      cfg.LockoutDuration,
	})
	org := organizacion.NewHandler(db)
	cat := catalogos.NewHandler(db)
	act := activos.NewHandler(db)
	mov := movimientos.NewHandler(db)
	sis := sistema.NewHandler(db)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", health(db))
		r.Route("/auth", func(r chi.Router) {
			authH.Routes(r, mw.RateLimit(cfg.LoginRateLimit), mw.Authenticate(tokens))
		})

		// Rutas autenticadas (SUPERADMIN y ADMIN; el ENCARGADO solo accede a las rutas de mw.EncargadoAccess)
		r.Group(func(r chi.Router) {
			r.Use(mw.Authenticate(tokens))
			r.Use(mw.RequireRoles(security.RoleSuperAdmin, security.RoleAdmin, security.RoleEncargado, security.RoleProveeduria,
				security.RoleVerificador, security.RoleJefe))
			r.Use(mw.EncargadoAccess("/api/v1"))

			r.Get("/catalogos/opciones", cat.Opciones)

			r.Route("/agencias", org.AgenciaRoutes)
			r.Route("/departamentos", org.DepartamentoRoutes)
			r.Route("/empleados", empleados.NewHandler(db).Routes)
			r.Route("/categorias", cat.CategoriaRoutes)
			r.Route("/marcas", cat.MarcaRoutes)
			r.Route("/proveedores", cat.ProveedorRoutes)
			r.Route("/tecnicos", cat.TecnicoRoutes)

			r.Route("/activos", act.Routes)
			r.Route("/historial", act.HistorialRoutes)
			r.Route("/asignaciones", mov.AsignacionRoutes)
			r.Route("/traslados", mov.TrasladoRoutes)
			r.Route("/envios", mov.EnvioRoutes)
			r.Route("/mantenimientos", mantenimientos.NewHandler(db).Routes)
			r.Route("/reemplazos", reemplazos.NewHandler(db).Routes)
			r.Route("/averias", averias.NewHandler(db).Routes)
			r.Route("/bodega", bodega.NewHandler(db).Routes)
			r.Route("/materiales", proveeduria.NewHandler(db).Routes)
			r.Route("/verificaciones", verificaciones.NewHandler(db).Routes)
			r.Route("/actas", actas.NewHandler(db, cfg.UploadsDir).Routes)
			r.Route("/reportes", reportes.NewHandler(db).Routes)
			r.Route("/configuracion", sis.ConfiguracionRoutes)

			// Solo SUPERADMIN
			r.Group(func(r chi.Router) {
				r.Use(mw.SuperAdminOnly)
				r.Route("/usuarios", usuarios.NewHandler(db).Routes)
				r.Route("/roles", roles.NewHandler(db).Routes)
				r.Route("/auditoria", sis.AuditoriaRoutes)
			})
		})
	})
	return r
}

func health(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		status := "ok"
		code := http.StatusOK
		if err := db.Ping(ctx); err != nil {
			status, code = "database_unavailable", http.StatusServiceUnavailable
		}
		httpx.JSON(w, code, map[string]any{"status": status, "time": time.Now()})
	}
}
