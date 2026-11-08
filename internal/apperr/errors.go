// Package apperr define los errores de negocio de la aplicación y su
// traducción desde errores de SQL Server.
package apperr

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	mssql "github.com/microsoft/go-mssqldb"
)

type Code string

const (
	CodeValidation   Code = "VALIDATION_ERROR"
	CodeBadRequest   Code = "BAD_REQUEST"
	CodeUnauthorized Code = "UNAUTHORIZED"
	CodeForbidden    Code = "FORBIDDEN"
	CodeNotFound     Code = "NOT_FOUND"
	CodeConflict     Code = "CONFLICT"
	CodeTooMany      Code = "TOO_MANY_REQUESTS"
	CodeInternal     Code = "INTERNAL_ERROR"
)

// Error es un error con código, mensaje para el usuario y estado HTTP.
type Error struct {
	Code    Code              `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Status  int               `json:"-"`
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

func New(status int, code Code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

func BadRequest(msg string) *Error   { return New(http.StatusBadRequest, CodeBadRequest, msg) }
func Unauthorized(msg string) *Error { return New(http.StatusUnauthorized, CodeUnauthorized, msg) }
func Forbidden(msg string) *Error    { return New(http.StatusForbidden, CodeForbidden, msg) }
func NotFound(msg string) *Error     { return New(http.StatusNotFound, CodeNotFound, msg) }
func Conflict(msg string) *Error     { return New(http.StatusConflict, CodeConflict, msg) }
func TooMany(msg string) *Error      { return New(http.StatusTooManyRequests, CodeTooMany, msg) }

func Validation(fields map[string]string) *Error {
	e := New(http.StatusUnprocessableEntity, CodeValidation, "Los datos enviados no son válidos")
	e.Fields = fields
	return e
}

func Internal(cause error) *Error {
	e := New(http.StatusInternalServerError, CodeInternal, "Ocurrió un error interno, intente nuevamente")
	e.cause = cause
	return e
}

// Mensajes amigables para restricciones conocidas de la base de datos.
var constraintMessages = map[string]string{
	"uq_agencias_codigo":       "Ya existe una agencia con ese código",
	"uq_departamentos_codigo":  "Ya existe un departamento con ese código",
	"uq_departamentos_nombre":  "Ya existe un departamento con ese nombre",
	"uq_empleados_cedula":      "Ya existe un empleado con esa cédula",
	"ck_empleados_cedula":      "La cédula debe tener 10 dígitos (o 13 si es RUC)",
	"uq_roles_codigo":          "Ya existe un rol con ese código",
	"uq_roles_nombre":          "Ya existe un rol con ese nombre",
	"ck_roles_codigo":          "El código del rol solo admite letras mayúsculas sin tildes, números y guion bajo",
	"uq_usuarios_username":     "El nombre de usuario ya está en uso",
	"uq_usuarios_email":        "El correo ya está registrado por otro usuario",
	"ck_usuarios_username":     "El usuario solo admite letras, números, punto, guion y guion bajo (3 a 50)",
	"uq_categorias_codigo":     "Ya existe una categoría con ese código",
	"uq_categorias_prefijo":    "Ya existe una categoría con ese prefijo",
	"uq_marcas_nombre":         "Ya existe una marca con ese nombre",
	"uq_proveedores_ruc":       "Ya existe un proveedor con ese RUC",
	"ck_proveedores_ruc":       "El RUC debe tener 13 dígitos",
	"uq_activos_codigo":        "Ya existe un activo con ese código",
	"uq_activos_serie":         "Ya existe un activo de esta categoría con el mismo número de serie",
	"ck_activos_padre":         "Un activo no puede estar instalado en sí mismo",
	"uq_asignaciones_vigente":  "El activo ya tiene una asignación vigente",
	"uq_repuestos_codigo":      "Ya existe un repuesto con ese código",
	"ck_repuestos_stock":       "Stock insuficiente para realizar la salida",
	"uq_actas_detalle":         "El activo está repetido en el acta",
	"ck_mantenimientos_fechas": "La fecha de fin no puede ser anterior a la fecha de inicio",
	"ck_asignaciones_fechas":   "La fecha de devolución no puede ser anterior a la de asignación",
}

// Números de error propios lanzados con THROW desde los procedimientos y triggers (ver scripts SQL).
const (
	errStockInsuficiente = 51001 // trg_movimientos_stock
	errRepuestoNoExiste  = 51002
)

// constraintRe extrae el nombre de la restricción o índice del mensaje de SQL Server, p. ej.:
//
//	Violation of UNIQUE KEY constraint 'uq_agencias_codigo'. ...
//	Cannot insert duplicate key row in object 'SGAT.usuarios' with unique index 'uq_usuarios_username'. ...
//	The INSERT statement conflicted with the CHECK constraint "ck_empleados_cedula". ...
var constraintRe = regexp.MustCompile(`(?:constraint|unique index)\s+['"]([^'"]+)['"]`)

func constraintName(msg string) string {
	if m := constraintRe.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// FromDB convierte errores de database/sql y SQL Server en errores de negocio.
func FromDB(err error, notFoundMsg string) error {
	if err == nil {
		return nil
	}
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, sql.ErrNoRows) {
		return NotFound(notFoundMsg)
	}

	var msErr mssql.Error
	var msErrPtr *mssql.Error
	found := errors.As(err, &msErr)
	if !found && errors.As(err, &msErrPtr) && msErrPtr != nil {
		msErr, found = *msErrPtr, true
	}
	if found {
		msg := msErr.Message
		if c := constraintName(msg); c != "" {
			if text, ok := constraintMessages[c]; ok {
				e := Conflict(text)
				e.cause = err
				return e
			}
		}
		switch msErr.Number {
		case errStockInsuficiente:
			e := Conflict(constraintMessages["ck_repuestos_stock"])
			e.cause = err
			return e
		case errRepuestoNoExiste:
			e := Conflict(msg)
			e.cause = err
			return e
		case 2627, 2601: // clave única duplicada
			e := Conflict("El registro ya existe")
			e.cause = err
			return e
		case 547: // FOREIGN KEY / REFERENCE o CHECK
			if strings.Contains(msg, "CHECK constraint") {
				e := BadRequest("Datos inválidos: " + msg)
				e.cause = err
				return e
			}
			e := Conflict("El registro está relacionado con otros datos y no puede modificarse o eliminarse")
			e.cause = err
			return e
		case 515, 2628, 8152, 245, 241, 242, 8114, 8115, 220, 248, 13609, 9413:
			// NULL no permitido, texto truncado, conversión de tipos / fechas, desbordamiento, JSON inválido
			e := BadRequest("Datos inválidos: " + msg)
			e.cause = err
			return e
		}
	}
	return Internal(err)
}
