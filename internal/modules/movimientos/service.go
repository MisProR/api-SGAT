// Package movimientos gestiona asignaciones de activos a empleados, devoluciones
// y traslados entre agencias/departamentos, con generación opcional de actas.
package movimientos

import (
	"context"
	"errors"
	"fmt"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/modules/inventario"
	"sgat-api/internal/security"
	"sgat-api/internal/types"
)

type empleadoInfo struct {
	ID             int64
	Nombre         string
	AgenciaID      int64
	DepartamentoID int64
	Habilitado     bool
}

func findEmpleado(ctx context.Context, q database.Querier, id int64) (empleadoInfo, error) {
	var e empleadoInfo
	err := q.QueryRow(ctx, `SELECT id, nombres + ' ' + apellidos, agencia_id, departamento_id, habilitado
		FROM empleados WHERE id = $1`, id).Scan(&e.ID, &e.Nombre, &e.AgenciaID, &e.DepartamentoID, &e.Habilitado)
	if err != nil {
		return e, apperr.FromDB(err, "Empleado no encontrado")
	}
	if !e.Habilitado {
		return e, apperr.Conflict("El empleado " + e.Nombre + " está deshabilitado")
	}
	return e, nil
}

// AsignarActivo asigna un activo a un empleado dentro de una transacción existente
// (usado al registrar un activo con responsable).
func AsignarActivo(ctx context.Context, tx database.Tx, activoID, empleadoID int64, fecha types.Date, obs string) error {
	emp, err := findEmpleado(ctx, tx, empleadoID)
	if err != nil {
		return err
	}
	_, err = asignar(ctx, tx, activoID, emp, fecha, obs)
	return err
}

// asignar entrega un activo (y sus componentes) a un empleado dentro de la tx.
// Si el activo tenía otro responsable, cierra esa asignación primero.
// Si la agencia del empleado es distinta, registra el traslado correspondiente.
func asignar(ctx context.Context, tx database.Tx, activoID int64, emp empleadoInfo, fecha types.Date, obs string) (int64, error) {
	a, err := inventario.LockOperable(ctx, tx, activoID)
	if err != nil {
		return 0, err
	}
	if a.PadreID != nil {
		return 0, apperr.Conflict("El componente " + a.Codigo + " está instalado en otro equipo; asigne el equipo principal")
	}
	if a.EmpleadoID != nil && *a.EmpleadoID == emp.ID {
		return 0, apperr.Conflict("El activo " + a.Codigo + " ya está asignado a " + emp.Nombre)
	}
	if a.EmpleadoID != nil {
		if err := cerrarAsignacion(ctx, tx, a, fecha, "Reasignación a "+emp.Nombre); err != nil {
			return 0, err
		}
	}

	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO asignaciones (activo_id, empleado_id, agencia_id, departamento_id, fecha_asignacion,
		observaciones, usuario_id) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7);
		SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
		a.ID, emp.ID, emp.AgenciaID, emp.DepartamentoID, fecha, obs, security.UserID(ctx)).Scan(&id)
	if err != nil {
		return 0, err
	}

	if a.AgenciaID != emp.AgenciaID || !ptrEq(a.DepartamentoID, &emp.DepartamentoID) {
		motivo := "Asignación a " + emp.Nombre
		if obs != "" {
			motivo += ". " + obs
		}
		if err := registrarTraslado(ctx, tx, a, emp.AgenciaID, &emp.DepartamentoID, &emp.ID, fecha, motivo); err != nil {
			return 0, err
		}
	}
	if err := inventario.Mover(ctx, tx, a.ID, inventario.Ubicacion{AgenciaID: emp.AgenciaID,
		DepartamentoID: &emp.DepartamentoID, EmpleadoID: &emp.ID}); err != nil {
		return 0, err
	}
	if err := audit.History(ctx, tx, audit.Event{ActivoID: a.ID, Tipo: audit.EvAsignacion,
		Descripcion: "Asignado a " + emp.Nombre, Referencia: "asignaciones", ReferenciaID: &id,
		Datos: map[string]any{"empleadoId": emp.ID, "empleado": emp.Nombre}}); err != nil {
		return 0, err
	}
	return id, audit.Log(ctx, tx, audit.Entry{Accion: audit.Asignar, Entidad: "activos", EntidadID: &a.ID,
		Descripcion: fmt.Sprintf("Activo %s asignado a %s", a.Codigo, emp.Nombre)})
}

// cerrarAsignacion marca como devuelta la asignación vigente del activo.
func cerrarAsignacion(ctx context.Context, tx database.Tx, a inventario.Activo, fecha types.Date, motivo string) error {
	var id int64
	var empleado string
	// OUTPUT ... INTO: asignaciones tiene trigger de updated_at (SQL Server no admite OUTPUT directo)
	err := tx.QueryRow(ctx, `
		DECLARE @r TABLE (id BIGINT, empleado NVARCHAR(250));
		UPDATE s SET estado = 'DEVUELTA',
		       fecha_devolucion = CASE WHEN CAST($2 AS DATE) > s.fecha_asignacion THEN CAST($2 AS DATE) ELSE s.fecha_asignacion END,
		       motivo_devolucion = NULLIF($3, '')
		OUTPUT INSERTED.id, e.nombres + ' ' + e.apellidos INTO @r
		FROM asignaciones s JOIN empleados e ON e.id = s.empleado_id
		WHERE s.activo_id = $1 AND s.estado = 'VIGENTE';
		SELECT id, empleado FROM @r`, a.ID, fecha, motivo).Scan(&id, &empleado)
	if errors.Is(err, database.ErrNoRows) {
		return nil // el activo tenía responsable pero sin asignación formal (datos migrados)
	}
	if err != nil {
		return err
	}
	desc := "Devuelto por " + empleado
	if motivo != "" {
		desc += ". " + motivo
	}
	return audit.History(ctx, tx, audit.Event{ActivoID: a.ID, Tipo: audit.EvDevolucion, Descripcion: desc,
		Referencia: "asignaciones", ReferenciaID: &id})
}

func registrarTraslado(ctx context.Context, tx database.Tx, a inventario.Activo, agenciaDestino int64, depDestino, empDestino *int64,
	fecha types.Date, motivo string) error {
	var id int64
	var origen, destino string
	err := tx.QueryRow(ctx, `
		INSERT INTO traslados (activo_id, agencia_origen_id, agencia_destino_id, departamento_origen_id, departamento_destino_id,
		                       empleado_origen_id, empleado_destino_id, fecha, motivo, usuario_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);
		SELECT CAST(SCOPE_IDENTITY() AS BIGINT), (SELECT nombre FROM agencias WHERE id = $2), (SELECT nombre FROM agencias WHERE id = $3)`,
		a.ID, a.AgenciaID, agenciaDestino, a.DepartamentoID, depDestino, a.EmpleadoID, empDestino, fecha, motivo,
		security.UserID(ctx)).Scan(&id, &origen, &destino)
	if err != nil {
		return err
	}
	desc := fmt.Sprintf("Traslado de %s a %s. %s", origen, destino, motivo)
	if a.AgenciaID == agenciaDestino {
		desc = fmt.Sprintf("Cambio de departamento en %s. %s", destino, motivo)
	}
	return audit.History(ctx, tx, audit.Event{ActivoID: a.ID, Tipo: audit.EvTraslado, Descripcion: desc, Referencia: "traslados", ReferenciaID: &id,
		Datos: map[string]any{"agenciaOrigenId": a.AgenciaID, "agenciaDestinoId": agenciaDestino}})
}
