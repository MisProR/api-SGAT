// Package httpx agrupa utilidades HTTP: respuestas JSON uniformes,
// lectura y validación del cuerpo, paginación y parámetros.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"sgat-api/internal/apperr"
)

// Envelope es el formato estándar de todas las respuestas de la API.
type Envelope struct {
	Success bool          `json:"success"`
	Data    any           `json:"data,omitempty"`
	Meta    *PageMeta     `json:"meta,omitempty"`
	Message string        `json:"message,omitempty"`
	Error   *apperr.Error `json:"error,omitempty"`
}

func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("error codificando respuesta", "error", err)
	}
}

func OK(w http.ResponseWriter, data any) {
	JSON(w, http.StatusOK, Envelope{Success: true, Data: data})
}

func Created(w http.ResponseWriter, data any) {
	JSON(w, http.StatusCreated, Envelope{Success: true, Data: data})
}

func Message(w http.ResponseWriter, msg string) {
	JSON(w, http.StatusOK, Envelope{Success: true, Message: msg})
}

// Error responde con el error de negocio o, si es inesperado, con un 500 sin filtrar detalles.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		appErr = apperr.Internal(err)
	}
	if appErr.Status >= 500 {
		slog.Error("error interno", "method", r.Method, "path", r.URL.Path, "error", err)
	}
	JSON(w, appErr.Status, Envelope{Success: false, Error: appErr})
}
