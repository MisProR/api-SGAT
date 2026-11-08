// Package middleware contiene los middlewares HTTP de la API.
package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"

	"sgat-api/internal/apperr"
	"sgat-api/internal/httpx"
	"sgat-api/internal/security"
)

// Authenticate valida el header "Authorization: Bearer <token>".
func Authenticate(tm *security.TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || raw == "" {
				httpx.Error(w, r, apperr.Unauthorized("Debe iniciar sesión"))
				return
			}
			p, err := tm.Parse(raw)
			if err != nil {
				httpx.Error(w, r, apperr.Unauthorized("La sesión expiró o no es válida"))
				return
			}
			next.ServeHTTP(w, r.WithContext(security.WithPrincipal(r.Context(), p)))
		})
	}
}

// RequireRoles permite el acceso solo a los roles indicados.
func RequireRoles(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := security.PrincipalFrom(r.Context())
			if !ok {
				httpx.Error(w, r, apperr.Unauthorized("Debe iniciar sesión"))
				return
			}
			if !slices.Contains(roles, p.Rol) {
				httpx.Error(w, r, apperr.Forbidden("No tiene permisos para realizar esta acción"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SuperAdminOnly es un atajo para RequireRoles(SUPERADMIN).
var SuperAdminOnly = RequireRoles(security.RoleSuperAdmin)

type rutaPermitida struct {
	metodo string
	patron *regexp.Regexp
}

func ruta(metodo, patron string) rutaPermitida {
	return rutaPermitida{metodo, regexp.MustCompile("^" + patron + "/?$")}
}

// rutasEncargado son las únicas rutas (relativas a /api/v1) que puede usar el rol ENCARGADO:
// consultar lo de su agencia, asignar activos a sus empleados, registrar devoluciones y salidas de empleados,
// enviar y recibir envíos, y adjuntar actas firmadas.
var rutasEncargado = []rutaPermitida{
	ruta(http.MethodGet, `/catalogos/opciones`),
	ruta(http.MethodGet, `/(agencias|departamentos)(/\d+)?`),
	ruta(http.MethodGet, `/empleados(/\d+(/activos|/devueltos)?)?`),
	ruta(http.MethodGet, `/activos(/\d+(/historial)?)?`),
	ruta(http.MethodGet, `/asignaciones`),
	ruta(http.MethodPost, `/asignaciones(/devolucion|/salida)?`),
	ruta(http.MethodGet, `/traslados`),
	ruta(http.MethodPost, `/traslados`),
	ruta(http.MethodGet, `/envios(/\d+|/por-recibir/total)?`),
	ruta(http.MethodPost, `/envios/\d+/(recibir|rechazar)`),
	// Reporte de daños de los equipos de su agencia (con pedido de reemplazo)
	ruta(http.MethodGet, `/averias(/\d+)?`),
	ruta(http.MethodPost, `/averias`),
	// Materiales que proveeduría envía a su agencia
	ruta(http.MethodGet, `/materiales(/\d+|/por-recibir/total)?`),
	ruta(http.MethodPost, `/materiales/\d+/(recibir|rechazar)`),
	ruta(http.MethodGet, `/actas(/\d+(/archivos(/\d+)?)?)?`),
	ruta(http.MethodPost, `/actas/\d+/archivos`),
	ruta(http.MethodGet, `/reportes/dashboard`),
}

// rutasProveeduria son las rutas del ENCARGADO DE PROVEEDURÍA: su bodega (stock y kardex), las entregas de
// materiales a las agencias, sus bienes con código (mobiliario: registro, asignación al jefe del área,
// devolución y envío a agencias; security.AreaScope los limita a su área), sus actas y la consulta de empleados.
var rutasProveeduria = []rutaPermitida{
	ruta(http.MethodGet, `/catalogos/opciones`),
	ruta(http.MethodGet, `/(agencias|departamentos)(/\d+)?`),
	ruta(http.MethodGet, `/empleados(/\d+(/activos|/devueltos)?)?`),
	ruta(http.MethodGet, `/activos(/exportar|/\d+(/historial)?)?`),
	ruta(http.MethodPost, `/activos`),
	ruta(http.MethodPut, `/activos/\d+`),
	ruta(http.MethodPatch, `/activos/\d+/estado`),
	ruta(http.MethodGet, `/(asignaciones|traslados)`),
	ruta(http.MethodPost, `/asignaciones(/devolucion)?`),
	ruta(http.MethodPost, `/traslados`),
	ruta(http.MethodGet, `/envios(/\d+|/por-recibir/total)?`),
	ruta(http.MethodPost, `/envios/\d+/(recibir|rechazar)`),
	ruta(http.MethodGet, `/bodega/(repuestos(/\d+)?|movimientos)`),
	ruta(http.MethodPost, `/bodega/(repuestos|movimientos)`),
	ruta(http.MethodPut, `/bodega/repuestos/\d+`),
	ruta(http.MethodGet, `/materiales(/\d+|/por-recibir/total)?`),
	ruta(http.MethodPost, `/materiales(/\d+/(recibir|rechazar))?`),
	ruta(http.MethodGet, `/actas(/\d+(/archivos(/\d+)?)?)?`),
	ruta(http.MethodPost, `/actas/\d+/archivos`),
	ruta(http.MethodGet, `/reportes/dashboard`),
}

// rutasVerificador: el VERIFICADOR consulta todo el inventario (todas las áreas y agencias) y registra la
// verificación anual; no puede modificar activos, empleados ni bodegas.
var rutasVerificador = []rutaPermitida{
	ruta(http.MethodGet, `/catalogos/opciones`),
	ruta(http.MethodGet, `/(agencias|departamentos)(/\d+)?`),
	ruta(http.MethodGet, `/empleados(/\d+(/activos)?)?`),
	ruta(http.MethodGet, `/activos(/exportar|/\d+(/historial)?)?`),
	ruta(http.MethodGet, `/asignaciones`),
	ruta(http.MethodGet, `/bodega/(repuestos(/\d+)?|movimientos)`),
	ruta(http.MethodGet, `/actas(/\d+(/archivos(/\d+)?)?)?`),
	ruta(http.MethodPost, `/actas/\d+/archivos`),
	ruta(http.MethodGet, `/reportes/dashboard`),
	ruta(http.MethodGet, `/verificaciones(/\d+(/empleados|/activos|/hallazgos|/inventario|/exportar)?)?`),
	ruta(http.MethodPost, `/verificaciones(/\d+/(registrar|acta|cerrar))?`),
}

// rutasRestringidas: roles que solo pueden usar una lista cerrada de rutas.
var rutasRestringidas = map[string][]rutaPermitida{
	security.RoleEncargado:   rutasEncargado,
	// El jefe hace lo mismo que el encargado, limitado a su departamento (lo filtra security.DepartamentoScope)
	security.RoleJefe: rutasEncargado,
	security.RoleProveeduria: rutasProveeduria,
	security.RoleVerificador: rutasVerificador,
}

// EncargadoAccess restringe al ENCARGADO y al ENCARGADO DE PROVEEDURÍA a sus rutas permitidas; los demás roles
// pasan sin cambios. El filtrado de los datos por agencia o área lo hace cada handler (security.AgenciaScope /
// security.AreaScope).
func EncargadoAccess(prefix string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := security.PrincipalFrom(r.Context())
			if rutas, restringido := rutasRestringidas[p.Rol]; ok && restringido {
				path := strings.TrimPrefix(r.URL.Path, prefix)
				permitida := slices.ContainsFunc(rutas, func(rp rutaPermitida) bool {
					return rp.metodo == r.Method && rp.patron.MatchString(path)
				})
				if !permitida {
					httpx.Error(w, r, apperr.Forbidden("No tiene permisos para realizar esta acción"))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientInfo guarda IP y User-Agent en el contexto para la auditoría.
func ClientInfo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.UserAgent()
		if len(ua) > 300 {
			ua = ua[:300]
		}
		ctx := security.WithClient(r.Context(), security.Client{IP: clientIP(r), UserAgent: ua})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func clientIP(r *http.Request) string {
	// chi RealIP ya reemplaza RemoteAddr con X-Forwarded-For / X-Real-IP si existen
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// SecurityHeaders agrega cabeceras de seguridad estándar.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Logger registra cada petición con slog.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		level := slog.LevelInfo
		if ww.Status() >= 500 {
			level = slog.LevelError
		} else if ww.Status() >= 400 {
			level = slog.LevelWarn
		}
		slog.Log(r.Context(), level, "http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", clientIP(r),
			"request_id", chimw.GetReqID(r.Context()),
		)
	})
}

// Recoverer captura panics y responde un 500 en formato JSON.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				slog.Error("panic", "error", rec, "stack", string(debug.Stack()))
				httpx.Error(w, r, apperr.Internal(nil))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RateLimit limita peticiones por IP (token bucket). Pensado para el login.
func RateLimit(perMinute int) func(http.Handler) http.Handler {
	type visitor struct {
		limiter  *rate.Limiter
		lastSeen time.Time
	}
	var (
		mu       sync.Mutex
		visitors = map[string]*visitor{}
	)

	go func() {
		for range time.Tick(time.Minute) {
			mu.Lock()
			for ip, v := range visitors {
				if time.Since(v.lastSeen) > 3*time.Minute {
					delete(visitors, ip)
				}
			}
			mu.Unlock()
		}
	}()

	every := rate.Every(time.Minute / time.Duration(max(perMinute, 1)))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			mu.Lock()
			v, ok := visitors[ip]
			if !ok {
				v = &visitor{limiter: rate.NewLimiter(every, perMinute)}
				visitors[ip] = v
			}
			v.lastSeen = time.Now()
			allowed := v.limiter.Allow()
			mu.Unlock()

			if !allowed {
				w.Header().Set("Retry-After", "60")
				httpx.Error(w, r, apperr.TooMany("Demasiados intentos, espere un minuto e intente nuevamente"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
