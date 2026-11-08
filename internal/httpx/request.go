package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"sgat-api/internal/apperr"
)

const maxBodyBytes = 1 << 20 // 1 MB

var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Usa el nombre del campo JSON en los mensajes de error
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
	_ = v.RegisterValidation("cedula", func(fl validator.FieldLevel) bool {
		return isDigits(fl.Field().String(), 10) || isDigits(fl.Field().String(), 13)
	})
	_ = v.RegisterValidation("ruc", func(fl validator.FieldLevel) bool {
		return isDigits(fl.Field().String(), 13)
	})
	_ = v.RegisterValidation("password", func(fl validator.FieldLevel) bool {
		return StrongPassword(fl.Field().String())
	})
	return v
}

func isDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// StrongPassword exige mínimo 8 caracteres con mayúscula, minúscula, número y símbolo.
func StrongPassword(p string) bool {
	if len(p) < 8 || len(p) > 72 {
		return false
	}
	var up, low, num, sym bool
	for _, c := range p {
		switch {
		case c >= 'A' && c <= 'Z':
			up = true
		case c >= 'a' && c <= 'z':
			low = true
		case c >= '0' && c <= '9':
			num = true
		default:
			sym = true
		}
	}
	return up && low && num && sym
}

// Bind decodifica el JSON del cuerpo (rechazando campos desconocidos) y valida la estructura.
func Bind(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if dec.More() {
		return apperr.BadRequest("El cuerpo debe contener un único objeto JSON")
	}
	return Validate(dst)
}

func Validate(v any) error {
	if err := validate.Struct(v); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			fields := make(map[string]string, len(verrs))
			for _, fe := range verrs {
				fields[fieldPath(fe)] = fieldMessage(fe)
			}
			return apperr.Validation(fields)
		}
		return apperr.BadRequest(err.Error())
	}
	return nil
}

// fieldPath devuelve la ruta JSON del campo (ej. "reemplazos[0].descripcion"),
// omitiendo el nombre de la estructura raíz y de las estructuras embebidas.
func fieldPath(fe validator.FieldError) string {
	parts := strings.Split(fe.Namespace(), ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts[1:] {
		if p == "" || (p[0] >= 'A' && p[0] <= 'Z') {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return fe.Field()
	}
	return strings.Join(out, ".")
}

func fieldMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required", "required_if", "required_without":
		return "Es obligatorio"
	case "email":
		return "Debe ser un correo válido"
	case "min":
		if fe.Kind() == reflect.String {
			return fmt.Sprintf("Debe tener al menos %s caracteres", fe.Param())
		}
		if fe.Kind() == reflect.Slice {
			return fmt.Sprintf("Debe tener al menos %s elementos", fe.Param())
		}
		return "Debe ser mayor o igual a " + fe.Param()
	case "max":
		if fe.Kind() == reflect.String {
			return fmt.Sprintf("Debe tener máximo %s caracteres", fe.Param())
		}
		return "Debe ser menor o igual a " + fe.Param()
	case "gt":
		return "Debe ser mayor a " + fe.Param()
	case "gte":
		return "Debe ser mayor o igual a " + fe.Param()
	case "oneof":
		return "Debe ser uno de: " + strings.ReplaceAll(fe.Param(), " ", ", ")
	case "cedula":
		return "Debe tener 10 dígitos (cédula) o 13 (RUC)"
	case "ruc":
		return "Debe tener 13 dígitos"
	case "password":
		return "Mínimo 8 caracteres con mayúscula, minúscula, número y símbolo"
	case "ip":
		return "Debe ser una dirección IP válida"
	case "mac":
		return "Debe ser una dirección MAC válida"
	case "nefield":
		return "Debe ser diferente de " + fe.Param()
	}
	return "Valor inválido"
}

func decodeError(err error) error {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, io.EOF):
		return apperr.BadRequest("El cuerpo de la solicitud está vacío")
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return apperr.BadRequest("JSON mal formado")
	case errors.As(err, &typeErr):
		return apperr.Validation(map[string]string{typeErr.Field: "Tipo de dato inválido"})
	case errors.As(err, &maxErr):
		return apperr.BadRequest("El cuerpo de la solicitud es demasiado grande")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return apperr.BadRequest("Campo no permitido: " + strings.TrimPrefix(err.Error(), "json: unknown field "))
	}
	return apperr.BadRequest(err.Error())
}

// IDParam obtiene un parámetro de ruta numérico positivo.
func IDParam(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.BadRequest("Identificador inválido")
	}
	return id, nil
}

// QueryInt64 devuelve un parámetro numérico opcional de la query string.
func QueryInt64(r *http.Request, name string) *int64 {
	v, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	if err != nil || v <= 0 {
		return nil
	}
	return &v
}

// QueryString devuelve un parámetro de texto opcional (nil si viene vacío).
func QueryString(r *http.Request, name string) *string {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil
	}
	return &v
}

func QueryBool(r *http.Request, name string) *bool {
	v, err := strconv.ParseBool(r.URL.Query().Get(name))
	if err != nil {
		return nil
	}
	return &v
}
