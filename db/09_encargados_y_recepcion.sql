-- =============================================================================
--  SGAT - Script 09 - Encargados de agencia, envíos con recepción y firma de actas
--
--  Se puede ejecutar sobre una base existente (no borra datos) y más de una vez.
--
--  Flujo:
--    1. Un encargado (o un admin) envía uno o varios activos a otra agencia
--       -> se crea un ENVÍO en estado EN_TRANSITO; los activos siguen en el origen.
--    2. Quien corresponde en el destino lo RECIBE (los activos pasan al destino)
--       o lo RECHAZA (los activos se quedan en el origen).
--    3. El acta del envío registra la firma de quien entrega y de quien recibe,
--       y admite subir el acta física firmada (PDF / imagen).
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

-- Rol ENCARGADO (id 3): se inserta en 17_datos_iniciales.sql

-- El encargado debe pertenecer a una agencia (rol 3 = ENCARGADO)
IF OBJECT_ID(N'SGAT.ck_usuarios_encargado_agencia', N'C') IS NULL
    ALTER TABLE SGAT.usuarios
        ADD CONSTRAINT ck_usuarios_encargado_agencia CHECK (rol_id <> 3 OR agencia_id IS NOT NULL);
GO

-- -----------------------------------------------------------------------------
--  Envíos: cabecera de un traslado entre agencias que requiere recepción
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.envios', N'U') IS NULL
CREATE TABLE SGAT.envios (
    id                        BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo                    NVARCHAR(20)   NOT NULL,
    agencia_origen_id         BIGINT         NOT NULL,
    agencia_destino_id        BIGINT         NOT NULL,
    departamento_destino_id   BIGINT         NULL,
    empleado_destino_id       BIGINT         NULL,
    motivo                    NVARCHAR(300)  NOT NULL,
    fecha_envio               DATE           NOT NULL DEFAULT CAST(GETDATE() AS DATE),
    estado                    NVARCHAR(12)   NOT NULL DEFAULT 'EN_TRANSITO',
    enviado_por               BIGINT         NULL,
    recibido_por              BIGINT         NULL,
    fecha_recepcion           DATETIMEOFFSET NULL,
    observaciones_recepcion   NVARCHAR(500)  NULL,
    created_at                DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at                DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_envios_codigo UNIQUE (codigo),
    CONSTRAINT fk_envios_ag_origen FOREIGN KEY (agencia_origen_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_envios_ag_destino FOREIGN KEY (agencia_destino_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_envios_dep_destino FOREIGN KEY (departamento_destino_id) REFERENCES SGAT.departamentos (id),
    CONSTRAINT fk_envios_emp_destino FOREIGN KEY (empleado_destino_id) REFERENCES SGAT.empleados (id),
    CONSTRAINT fk_envios_enviado_por FOREIGN KEY (enviado_por) REFERENCES SGAT.usuarios (id),
    CONSTRAINT fk_envios_recibido_por FOREIGN KEY (recibido_por) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_envios_estado CHECK (estado IN ('EN_TRANSITO', 'RECIBIDO', 'RECHAZADO')),
    CONSTRAINT ck_envios_agencias CHECK (agencia_origen_id <> agencia_destino_id),
    CONSTRAINT ck_envios_recepcion CHECK (estado = 'EN_TRANSITO' OR fecha_recepcion IS NOT NULL)
);
GO
EXEC SGAT.sp_comentar N'envios', N'Envíos de activos entre agencias pendientes de recepción, recibidos o rechazados';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_envios_estado' AND object_id = OBJECT_ID(N'SGAT.envios'))
    CREATE INDEX ix_envios_estado ON SGAT.envios (estado, agencia_destino_id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_envios_origen' AND object_id = OBJECT_ID(N'SGAT.envios'))
    CREATE INDEX ix_envios_origen ON SGAT.envios (agencia_origen_id, created_at DESC);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_envios_destino' AND object_id = OBJECT_ID(N'SGAT.envios'))
    CREATE INDEX ix_envios_destino ON SGAT.envios (agencia_destino_id, created_at DESC);
GO

EXEC SGAT.sp_crear_trigger_updated_at N'envios';
-- Código legible ENV-2026-000001: lo genera la API con EXEC SGAT.sp_generar_codigo_anual 'ENV'
GO

-- Cada fila de traslado pertenece a un envío (NULL = traslado directo anterior a este script)
IF COL_LENGTH(N'SGAT.traslados', N'envio_id') IS NULL
    ALTER TABLE SGAT.traslados ADD envio_id BIGINT NULL;
GO
IF OBJECT_ID(N'SGAT.fk_traslados_envio', N'F') IS NULL
    ALTER TABLE SGAT.traslados
        ADD CONSTRAINT fk_traslados_envio FOREIGN KEY (envio_id) REFERENCES SGAT.envios (id) ON DELETE CASCADE;
GO
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_traslados_envio' AND object_id = OBJECT_ID(N'SGAT.traslados'))
    CREATE INDEX ix_traslados_envio ON SGAT.traslados (envio_id);
GO

-- -----------------------------------------------------------------------------
--  Actas: vínculo con el envío y firma digital de ambas partes
--    Firma = usuario + fecha/hora en que confirmó (entrega al enviar, recepción al recibir)
-- -----------------------------------------------------------------------------
IF COL_LENGTH(N'SGAT.actas', N'envio_id') IS NULL
    ALTER TABLE SGAT.actas ADD envio_id BIGINT NULL;
IF COL_LENGTH(N'SGAT.actas', N'firma_entrega_usuario_id') IS NULL
    ALTER TABLE SGAT.actas ADD firma_entrega_usuario_id BIGINT NULL;
IF COL_LENGTH(N'SGAT.actas', N'firma_entrega_fecha') IS NULL
    ALTER TABLE SGAT.actas ADD firma_entrega_fecha DATETIMEOFFSET NULL;
IF COL_LENGTH(N'SGAT.actas', N'firma_recepcion_usuario_id') IS NULL
    ALTER TABLE SGAT.actas ADD firma_recepcion_usuario_id BIGINT NULL;
IF COL_LENGTH(N'SGAT.actas', N'firma_recepcion_fecha') IS NULL
    ALTER TABLE SGAT.actas ADD firma_recepcion_fecha DATETIMEOFFSET NULL;
GO

IF OBJECT_ID(N'SGAT.fk_actas_envio', N'F') IS NULL
    ALTER TABLE SGAT.actas ADD CONSTRAINT fk_actas_envio FOREIGN KEY (envio_id) REFERENCES SGAT.envios (id);
IF OBJECT_ID(N'SGAT.fk_actas_firma_entrega', N'F') IS NULL
    ALTER TABLE SGAT.actas
        ADD CONSTRAINT fk_actas_firma_entrega FOREIGN KEY (firma_entrega_usuario_id) REFERENCES SGAT.usuarios (id);
IF OBJECT_ID(N'SGAT.fk_actas_firma_recepcion', N'F') IS NULL
    ALTER TABLE SGAT.actas
        ADD CONSTRAINT fk_actas_firma_recepcion FOREIGN KEY (firma_recepcion_usuario_id) REFERENCES SGAT.usuarios (id);
-- Equivale a CHECK ((usuario IS NULL) = (fecha IS NULL))
IF OBJECT_ID(N'SGAT.ck_actas_firma_entrega', N'C') IS NULL
    ALTER TABLE SGAT.actas ADD CONSTRAINT ck_actas_firma_entrega
        CHECK ((firma_entrega_usuario_id IS NULL AND firma_entrega_fecha IS NULL)
            OR (firma_entrega_usuario_id IS NOT NULL AND firma_entrega_fecha IS NOT NULL));
IF OBJECT_ID(N'SGAT.ck_actas_firma_recepcion', N'C') IS NULL
    ALTER TABLE SGAT.actas ADD CONSTRAINT ck_actas_firma_recepcion
        CHECK ((firma_recepcion_usuario_id IS NULL AND firma_recepcion_fecha IS NULL)
            OR (firma_recepcion_usuario_id IS NOT NULL AND firma_recepcion_fecha IS NOT NULL));
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'uq_actas_envio' AND object_id = OBJECT_ID(N'SGAT.actas'))
    CREATE UNIQUE INDEX uq_actas_envio ON SGAT.actas (envio_id) WHERE envio_id IS NOT NULL;
GO

-- -----------------------------------------------------------------------------
--  Archivos del acta: acta física firmada y escaneada (PDF o imagen)
--    El archivo se guarda en el servidor; aquí solo la referencia y sus datos.
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.actas_archivos', N'U') IS NULL
CREATE TABLE SGAT.actas_archivos (
    id                BIGINT IDENTITY(1,1) PRIMARY KEY,
    acta_id           BIGINT         NOT NULL,
    nombre_original   NVARCHAR(200)  NOT NULL,
    ruta              NVARCHAR(300)  NOT NULL,
    tipo_mime         NVARCHAR(100)  NOT NULL,
    tamano_bytes      BIGINT         NOT NULL,
    subido_por        BIGINT         NULL,
    created_at        DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_actas_archivos_ruta UNIQUE (ruta),
    CONSTRAINT fk_actas_archivos_acta FOREIGN KEY (acta_id) REFERENCES SGAT.actas (id) ON DELETE CASCADE,
    CONSTRAINT fk_actas_archivos_usuario FOREIGN KEY (subido_por) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_actas_archivos_tipo CHECK (tipo_mime IN ('application/pdf', 'image/jpeg', 'image/png')),
    CONSTRAINT ck_actas_archivos_tamano CHECK (tamano_bytes > 0 AND tamano_bytes <= 10485760)
);
GO
EXEC SGAT.sp_comentar N'actas_archivos', N'Actas firmadas escaneadas (PDF, JPG o PNG de hasta 10 MB)';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_actas_archivos_acta' AND object_id = OBJECT_ID(N'SGAT.actas_archivos'))
    CREATE INDEX ix_actas_archivos_acta ON SGAT.actas_archivos (acta_id, created_at DESC);
GO

-- -----------------------------------------------------------------------------
--  Nuevas acciones de auditoría y eventos del historial de activos
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.ck_auditoria_accion', N'C') IS NOT NULL
    ALTER TABLE SGAT.auditoria DROP CONSTRAINT ck_auditoria_accion;
ALTER TABLE SGAT.auditoria ADD CONSTRAINT ck_auditoria_accion CHECK (accion IN (
    'CREAR', 'ACTUALIZAR', 'ELIMINAR', 'LOGIN', 'LOGIN_FALLIDO', 'LOGOUT',
    'CAMBIO_PASSWORD', 'ASIGNAR', 'DEVOLVER', 'TRASLADAR', 'DAR_BAJA', 'EXPORTAR',
    'ENVIAR', 'RECIBIR', 'RECHAZAR', 'FIRMAR', 'SUBIR_ARCHIVO'));
GO

IF OBJECT_ID(N'SGAT.ck_historial_tipo', N'C') IS NOT NULL
    ALTER TABLE SGAT.historial_activos DROP CONSTRAINT ck_historial_tipo;
ALTER TABLE SGAT.historial_activos ADD CONSTRAINT ck_historial_tipo CHECK (tipo_evento IN (
    'REGISTRO', 'ACTUALIZACION', 'CAMBIO_ESTADO', 'ASIGNACION', 'DEVOLUCION', 'TRASLADO',
    'MANTENIMIENTO', 'REEMPLAZO', 'AVERIA', 'BAJA', 'ACTA',
    'ENVIO', 'RECEPCION', 'RECHAZO'));
GO

-- -----------------------------------------------------------------------------
--  Permisos del usuario de aplicación (si existe). GRANT sobre el esquema ya cubre las tablas nuevas.
-- -----------------------------------------------------------------------------
IF EXISTS (SELECT 1 FROM sys.database_principals WHERE name = N'sgat_app')
    GRANT SELECT, INSERT, UPDATE, DELETE, EXECUTE ON SCHEMA::SGAT TO sgat_app;
GO
