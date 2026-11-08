-- =============================================================================
--  SGAT - Script 10 - Salida de empleados
--
--  Se puede ejecutar sobre una base existente (no borra datos) y más de una vez.
--
--  Flujo:
--    1. El encargado (o un admin) registra la salida: el empleado devuelve todos sus
--       activos, se genera el acta de devolución y los equipos quedan sin responsable
--       en la agencia (listos para quien ocupe el puesto).
--    2. Si la registró el encargado, la salida queda pendiente hasta que un admin la
--       confirma y deshabilita al empleado. Si la registró un admin, se confirma de una vez.
-- =============================================================================
SET ANSI_NULLS ON;
SET QUOTED_IDENTIFIER ON;
SET ANSI_PADDING ON;
SET ANSI_WARNINGS ON;
SET ARITHABORT ON;
SET CONCAT_NULL_YIELDS_NULL ON;
SET NUMERIC_ROUNDABORT OFF;
GO
USE sgat;
GO

IF COL_LENGTH(N'SGAT.empleados', N'fecha_salida') IS NULL
    ALTER TABLE SGAT.empleados ADD fecha_salida DATE NULL;
IF COL_LENGTH(N'SGAT.empleados', N'motivo_salida') IS NULL
    ALTER TABLE SGAT.empleados ADD motivo_salida NVARCHAR(300) NULL;
IF COL_LENGTH(N'SGAT.empleados', N'salida_pendiente') IS NULL
    ALTER TABLE SGAT.empleados ADD salida_pendiente BIT NOT NULL
        CONSTRAINT df_empleados_salida_pendiente DEFAULT 0;
GO

EXEC SGAT.sp_comentar N'empleados', N'Fecha en que el empleado dejó la institución (NULL = vigente)', N'fecha_salida';
EXEC SGAT.sp_comentar N'empleados', N'Salida registrada por el encargado, pendiente de confirmación de un administrador', N'salida_pendiente';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_empleados_salida_pendiente' AND object_id = OBJECT_ID(N'SGAT.empleados'))
    CREATE INDEX ix_empleados_salida_pendiente ON SGAT.empleados (id) WHERE salida_pendiente = 1;

-- Último responsable de cada activo (para mostrar de quién era un equipo devuelto)
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_asignaciones_activo_fecha' AND object_id = OBJECT_ID(N'SGAT.asignaciones'))
    CREATE INDEX ix_asignaciones_activo_fecha ON SGAT.asignaciones (activo_id, fecha_asignacion DESC, id DESC);
GO
