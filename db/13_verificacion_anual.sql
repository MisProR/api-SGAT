-- =============================================================================
--  SGAT - Script 13 - Verificación anual del inventario (toma física)
--
--  Se puede ejecutar sobre una base existente (no borra datos) y más de una vez.
--
--  Flujo:
--    1. El VERIFICADOR (o el superadmin) abre una verificación, p. ej. "Verificación 2026",
--       para todas las agencias o una sola (VER-2026-000001).
--    2. Recorre empleado por empleado y marca cada activo a su cargo como ENCONTRADO,
--       NO_ENCONTRADO o DIFERENCIA (otro equipo, dañado, otra serie...), con observación.
--       Queda registrado quién lo verificó y cuándo.
--    3. Genera el acta de verificación por agencia (tipo VERIFICACION) para firmarla y subirla.
--    4. Al cerrar, lo que no se revisó queda como NO_VERIFICADO y el resultado ya no cambia.
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

-- Rol VERIFICADOR (id 5): se inserta en 17_datos_iniciales.sql

-- -----------------------------------------------------------------------------
--  Verificaciones (campañas)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.verificaciones', N'U') IS NULL
CREATE TABLE SGAT.verificaciones (
    id              BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo          NVARCHAR(20)   NOT NULL,
    nombre          NVARCHAR(150)  NOT NULL,
    anio            SMALLINT       NOT NULL,
    agencia_id      BIGINT         NULL,
    estado          NVARCHAR(10)   NOT NULL DEFAULT 'ABIERTA',
    observaciones   NVARCHAR(1000) NULL,
    fecha_inicio    DATE           NOT NULL DEFAULT CAST(GETDATE() AS DATE),
    fecha_cierre    DATETIMEOFFSET NULL,
    creado_por      BIGINT         NULL,
    cerrado_por     BIGINT         NULL,
    created_at      DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at      DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_verificaciones_codigo UNIQUE (codigo),
    CONSTRAINT fk_verificaciones_agencia FOREIGN KEY (agencia_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_verificaciones_creado FOREIGN KEY (creado_por) REFERENCES SGAT.usuarios (id),
    CONSTRAINT fk_verificaciones_cerrado FOREIGN KEY (cerrado_por) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_verificaciones_estado CHECK (estado IN ('ABIERTA', 'CERRADA')),
    CONSTRAINT ck_verificaciones_cierre CHECK (estado = 'ABIERTA' OR fecha_cierre IS NOT NULL)
);
GO
EXEC SGAT.sp_comentar N'verificaciones', N'Campañas de verificación física del inventario (toma física anual)';
EXEC SGAT.sp_comentar N'verificaciones', N'Agencia verificada (NULL = todas las agencias)', N'agencia_id';
GO

EXEC SGAT.sp_crear_trigger_updated_at N'verificaciones';
-- Código legible VER-2026-000001: lo genera la API con EXEC SGAT.sp_generar_codigo_anual 'VER'
GO

-- -----------------------------------------------------------------------------
--  Resultado de cada activo en la verificación
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.verificacion_items', N'U') IS NULL
CREATE TABLE SGAT.verificacion_items (
    id                BIGINT IDENTITY(1,1) PRIMARY KEY,
    verificacion_id   BIGINT         NOT NULL,
    activo_id         BIGINT         NOT NULL,
    empleado_id       BIGINT         NULL,
    agencia_id        BIGINT         NOT NULL,
    resultado         NVARCHAR(15)   NOT NULL,
    observacion       NVARCHAR(300)  NULL,
    verificado_por    BIGINT         NULL,
    verificado_en     DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT fk_verificacion_items_ver FOREIGN KEY (verificacion_id) REFERENCES SGAT.verificaciones (id) ON DELETE CASCADE,
    CONSTRAINT fk_verificacion_items_activo FOREIGN KEY (activo_id) REFERENCES SGAT.activos (id),
    CONSTRAINT fk_verificacion_items_empleado FOREIGN KEY (empleado_id) REFERENCES SGAT.empleados (id),
    CONSTRAINT fk_verificacion_items_agencia FOREIGN KEY (agencia_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_verificacion_items_usuario FOREIGN KEY (verificado_por) REFERENCES SGAT.usuarios (id),
    CONSTRAINT uq_verificacion_items UNIQUE (verificacion_id, activo_id),
    CONSTRAINT ck_verificacion_items_resultado CHECK (resultado IN ('ENCONTRADO', 'NO_ENCONTRADO', 'DIFERENCIA', 'NO_VERIFICADO'))
);
GO
EXEC SGAT.sp_comentar N'verificacion_items', N'Resultado de la verificación de cada activo (responsable y agencia al momento de verificar)';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_verificacion_items_empleado' AND object_id = OBJECT_ID(N'SGAT.verificacion_items'))
    CREATE INDEX ix_verificacion_items_empleado ON SGAT.verificacion_items (verificacion_id, empleado_id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_verificacion_items_agencia' AND object_id = OBJECT_ID(N'SGAT.verificacion_items'))
    CREATE INDEX ix_verificacion_items_agencia ON SGAT.verificacion_items (verificacion_id, agencia_id);
GO

-- -----------------------------------------------------------------------------
--  Acta de verificación (constatación física) por agencia
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.ck_actas_tipo', N'C') IS NOT NULL
    ALTER TABLE SGAT.actas DROP CONSTRAINT ck_actas_tipo;
ALTER TABLE SGAT.actas ADD CONSTRAINT ck_actas_tipo
    CHECK (tipo IN ('ENTREGA', 'DEVOLUCION', 'TRASLADO', 'BAJA', 'VERIFICACION'));
GO

IF COL_LENGTH(N'SGAT.actas', N'verificacion_id') IS NULL
    ALTER TABLE SGAT.actas ADD verificacion_id BIGINT NULL;
GO
IF OBJECT_ID(N'SGAT.fk_actas_verificacion', N'F') IS NULL
    ALTER TABLE SGAT.actas
        ADD CONSTRAINT fk_actas_verificacion FOREIGN KEY (verificacion_id) REFERENCES SGAT.verificaciones (id);
GO
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_actas_verificacion' AND object_id = OBJECT_ID(N'SGAT.actas'))
    CREATE INDEX ix_actas_verificacion ON SGAT.actas (verificacion_id) WHERE verificacion_id IS NOT NULL;
GO

-- Evento del historial: "Verificado en la toma física 2026"
IF OBJECT_ID(N'SGAT.ck_historial_tipo', N'C') IS NOT NULL
    ALTER TABLE SGAT.historial_activos DROP CONSTRAINT ck_historial_tipo;
ALTER TABLE SGAT.historial_activos ADD CONSTRAINT ck_historial_tipo CHECK (tipo_evento IN (
    'REGISTRO', 'ACTUALIZACION', 'CAMBIO_ESTADO', 'ASIGNACION', 'DEVOLUCION', 'TRASLADO',
    'MANTENIMIENTO', 'REEMPLAZO', 'AVERIA', 'BAJA', 'ACTA',
    'ENVIO', 'RECEPCION', 'RECHAZO', 'VERIFICACION'));
GO
