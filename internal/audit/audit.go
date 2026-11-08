// Package audit registra las acciones de los usuarios en la tabla auditoria
// e incluye el registro del historial (línea de tiempo) de los activos.
package audit

import (
	"context"
	"encoding/json"

	"sgat-api/internal/database"
	"sgat-api/internal/security"
)

// Acciones de auditoría (deben coincidir con ck_auditoria_accion)
const (
	Crear          = "CREAR"
	Actualizar     = "ACTUALIZAR"
	Eliminar       = "ELIMINAR"
	Login          = "LOGIN"
	LoginFallido   = "LOGIN_FALLIDO"
	Logout         = "LOGOUT"
	CambioPassword = "CAMBIO_PASSWORD"
	Asignar        = "ASIGNAR"
	Devolver       = "DEVOLVER"
	Trasladar      = "TRASLADAR"
	DarBaja        = "DAR_BAJA"
	Exportar       = "EXPORTAR"
	Enviar         = "ENVIAR"
	Recibir        = "RECIBIR"
	Rechazar       = "RECHAZAR"
	Firmar         = "FIRMAR"
	SubirArchivo   = "SUBIR_ARCHIVO"
)

type Entry struct {
	Accion      string
	Entidad     string
	EntidadID   *int64
	Descripcion string
	Antes       any
	Despues     any
	// UsuarioID/UsuarioNombre permiten auditar cuando aún no hay sesión (login)
	UsuarioID     *int64
	UsuarioNombre string
}

// Log inserta el registro de auditoría usando el usuario y cliente del contexto.
// Se recomienda llamarlo con la misma transacción de la operación auditada.
func Log(ctx context.Context, q database.Querier, e Entry) error {
	usuarioID, nombre := e.UsuarioID, e.UsuarioNombre
	if p, ok := security.PrincipalFrom(ctx); ok && usuarioID == nil {
		usuarioID, nombre = &p.ID, p.Nombre
	}
	client := security.ClientFrom(ctx)
	_, err := q.Exec(ctx, `
		INSERT INTO auditoria (usuario_id, usuario_nombre, accion, entidad, entidad_id, descripcion,
		                       datos_anteriores, datos_nuevos, ip, user_agent)
		VALUES ($1, NULLIF(CAST($2 AS NVARCHAR(120)), N''), $3, $4, $5, NULLIF(CAST($6 AS NVARCHAR(500)), N''), $7, $8,
		        NULLIF(CAST($9 AS NVARCHAR(45)), N''), NULLIF(CAST($10 AS NVARCHAR(300)), N''))`,
		usuarioID, nombre, e.Accion, e.Entidad, e.EntidadID, truncate(e.Descripcion, 500),
		toJSON(e.Antes), toJSON(e.Despues), client.IP, client.UserAgent)
	return err
}

// Tipos de evento del historial de activos (deben coincidir con ck_historial_tipo)
const (
	EvRegistro      = "REGISTRO"
	EvActualizacion = "ACTUALIZACION"
	EvCambioEstado  = "CAMBIO_ESTADO"
	EvAsignacion    = "ASIGNACION"
	EvDevolucion    = "DEVOLUCION"
	EvTraslado      = "TRASLADO"
	EvMantenimiento = "MANTENIMIENTO"
	EvReemplazo     = "REEMPLAZO"
	EvAveria        = "AVERIA"
	EvBaja          = "BAJA"
	EvActa          = "ACTA"
	EvEnvio         = "ENVIO"
	EvRecepcion     = "RECEPCION"
	EvRechazo       = "RECHAZO"
	EvVerificacion  = "VERIFICACION"
)

type Event struct {
	ActivoID     int64
	Tipo         string
	Descripcion  string
	Datos        any
	Referencia   string
	ReferenciaID *int64
}

// History agrega un evento a la línea de tiempo del activo.
func History(ctx context.Context, q database.Querier, ev Event) error {
	datos := toJSON(ev.Datos)
	if datos == nil {
		datos = []byte("{}")
	}
	_, err := q.Exec(ctx, `
		INSERT INTO historial_activos (activo_id, tipo_evento, descripcion, datos, referencia, referencia_id, usuario_id)
		VALUES ($1, $2, $3, $4, NULLIF(CAST($5 AS NVARCHAR(30)), N''), $6, $7)`,
		ev.ActivoID, ev.Tipo, truncate(ev.Descripcion, 500), datos, ev.Referencia, ev.ReferenciaID, security.UserID(ctx))
	return err
}

// toJSON serializa el valor como texto JSON (columnas NVARCHAR(MAX) con ISJSON).
func toJSON(v any) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return nil
	}
	return b
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
