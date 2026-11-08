-- =============================================================================
--  SGAT - Script 06 - Operaciones sobre activos
--  Asignaciones, traslados, averías, mantenimientos, reemplazos e historial
--
--  Nota sobre cascadas: SQL Server no permite dos caminos de ON DELETE hacia la misma tabla.
--  Las FK secundarias (mantenimientos.averia_id y reemplazos.mantenimiento_id) quedan sin acción
--  (NO ACTION); al eliminar un activo sus averías, mantenimientos y reemplazos se borran por la
--  cascada principal (activo_id), igual que en PostgreSQL.
--  Se puede ejecutar más de una vez: solo crea lo que todavía no existe.
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

-- -----------------------------------------------------------------------------
--  Asignaciones: qué empleado tiene a cargo cada activo (y desde cuándo)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.asignaciones', N'U') IS NULL
CREATE TABLE SGAT.asignaciones (
    id                 BIGINT IDENTITY(1,1) PRIMARY KEY,
    activo_id          BIGINT         NOT NULL,
    empleado_id        BIGINT         NOT NULL,
    agencia_id         BIGINT         NOT NULL,
    departamento_id    BIGINT         NOT NULL,
    fecha_asignacion   DATE           NOT NULL DEFAULT CAST(GETDATE() AS DATE),
    fecha_devolucion   DATE           NULL,
    estado             NVARCHAR(10)   NOT NULL DEFAULT 'VIGENTE',
    observaciones      NVARCHAR(500)  NULL,
    motivo_devolucion  NVARCHAR(300)  NULL,
    usuario_id         BIGINT         NULL,
    created_at         DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at         DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT fk_asignaciones_activo FOREIGN KEY (activo_id) REFERENCES SGAT.activos (id) ON DELETE CASCADE,
    CONSTRAINT fk_asignaciones_empleado FOREIGN KEY (empleado_id) REFERENCES SGAT.empleados (id),
    CONSTRAINT fk_asignaciones_agencia FOREIGN KEY (agencia_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_asignaciones_departamento FOREIGN KEY (departamento_id) REFERENCES SGAT.departamentos (id),
    CONSTRAINT fk_asignaciones_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_asignaciones_estado CHECK (estado IN ('VIGENTE', 'DEVUELTA')),
    CONSTRAINT ck_asignaciones_fechas CHECK (fecha_devolucion IS NULL OR fecha_devolucion >= fecha_asignacion),
    CONSTRAINT ck_asignaciones_devolucion CHECK (estado = 'VIGENTE' OR fecha_devolucion IS NOT NULL)
);
GO
EXEC SGAT.sp_comentar N'asignaciones', N'Historial de responsables de cada activo';
GO

-- Un activo solo puede tener UNA asignación vigente
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'uq_asignaciones_vigente' AND object_id = OBJECT_ID(N'SGAT.asignaciones'))
    CREATE UNIQUE INDEX uq_asignaciones_vigente ON SGAT.asignaciones (activo_id) WHERE estado = 'VIGENTE';
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_asignaciones_empleado' AND object_id = OBJECT_ID(N'SGAT.asignaciones'))
    CREATE INDEX ix_asignaciones_empleado ON SGAT.asignaciones (empleado_id, estado);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_asignaciones_fecha' AND object_id = OBJECT_ID(N'SGAT.asignaciones'))
    CREATE INDEX ix_asignaciones_fecha ON SGAT.asignaciones (fecha_asignacion DESC);
GO
EXEC SGAT.sp_crear_trigger_updated_at N'asignaciones';
GO

-- -----------------------------------------------------------------------------
--  Traslados entre agencias / departamentos
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.traslados', N'U') IS NULL
CREATE TABLE SGAT.traslados (
    id                        BIGINT IDENTITY(1,1) PRIMARY KEY,
    activo_id                 BIGINT         NOT NULL,
    agencia_origen_id         BIGINT         NOT NULL,
    agencia_destino_id        BIGINT         NOT NULL,
    departamento_origen_id    BIGINT         NULL,
    departamento_destino_id   BIGINT         NULL,
    empleado_origen_id        BIGINT         NULL,
    empleado_destino_id       BIGINT         NULL,
    fecha                     DATE           NOT NULL DEFAULT CAST(GETDATE() AS DATE),
    motivo                    NVARCHAR(300)  NOT NULL,
    usuario_id                BIGINT         NULL,
    created_at                DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT fk_traslados_activo FOREIGN KEY (activo_id) REFERENCES SGAT.activos (id) ON DELETE CASCADE,
    CONSTRAINT fk_traslados_ag_origen FOREIGN KEY (agencia_origen_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_traslados_ag_destino FOREIGN KEY (agencia_destino_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_traslados_dep_origen FOREIGN KEY (departamento_origen_id) REFERENCES SGAT.departamentos (id),
    CONSTRAINT fk_traslados_dep_destino FOREIGN KEY (departamento_destino_id) REFERENCES SGAT.departamentos (id),
    CONSTRAINT fk_traslados_emp_origen FOREIGN KEY (empleado_origen_id) REFERENCES SGAT.empleados (id),
    CONSTRAINT fk_traslados_emp_destino FOREIGN KEY (empleado_destino_id) REFERENCES SGAT.empleados (id),
    CONSTRAINT fk_traslados_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id)
);
GO
EXEC SGAT.sp_comentar N'traslados', N'Movimientos de activos entre agencias y departamentos';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_traslados_activo' AND object_id = OBJECT_ID(N'SGAT.traslados'))
    CREATE INDEX ix_traslados_activo ON SGAT.traslados (activo_id, fecha DESC);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_traslados_fecha' AND object_id = OBJECT_ID(N'SGAT.traslados'))
    CREATE INDEX ix_traslados_fecha ON SGAT.traslados (fecha DESC);
GO

-- -----------------------------------------------------------------------------
--  Averías / incidencias reportadas
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.averias', N'U') IS NULL
CREATE TABLE SGAT.averias (
    id               BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo           NVARCHAR(20)   NOT NULL,
    activo_id        BIGINT         NOT NULL,
    reportado_por    BIGINT         NULL,
    fecha_reporte    DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    descripcion      NVARCHAR(1000) NOT NULL,
    severidad        NVARCHAR(10)   NOT NULL DEFAULT 'MEDIA',
    estado           NVARCHAR(15)   NOT NULL DEFAULT 'REPORTADA',
    solucion         NVARCHAR(1000) NULL,
    fecha_solucion   DATETIMEOFFSET NULL,
    usuario_id       BIGINT         NULL,
    created_at       DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at       DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_averias_codigo UNIQUE (codigo),
    CONSTRAINT fk_averias_activo FOREIGN KEY (activo_id) REFERENCES SGAT.activos (id) ON DELETE CASCADE,
    CONSTRAINT fk_averias_reportado FOREIGN KEY (reportado_por) REFERENCES SGAT.empleados (id),
    CONSTRAINT fk_averias_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_averias_severidad CHECK (severidad IN ('BAJA', 'MEDIA', 'ALTA', 'CRITICA')),
    CONSTRAINT ck_averias_estado CHECK (estado IN ('REPORTADA', 'EN_REVISION', 'RESUELTA', 'SIN_SOLUCION'))
);
GO
EXEC SGAT.sp_comentar N'averias', N'Fallas e incidencias reportadas sobre los activos';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_averias_activo' AND object_id = OBJECT_ID(N'SGAT.averias'))
    CREATE INDEX ix_averias_activo ON SGAT.averias (activo_id, fecha_reporte DESC);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_averias_estado' AND object_id = OBJECT_ID(N'SGAT.averias'))
    CREATE INDEX ix_averias_estado ON SGAT.averias (estado);
GO
EXEC SGAT.sp_crear_trigger_updated_at N'averias';
GO

-- -----------------------------------------------------------------------------
--  Mantenimientos preventivos y correctivos
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.mantenimientos', N'U') IS NULL
CREATE TABLE SGAT.mantenimientos (
    id                 BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo             NVARCHAR(20)   NOT NULL,
    activo_id          BIGINT         NOT NULL,
    averia_id          BIGINT         NULL,
    tecnico_id         BIGINT         NOT NULL,
    tipo               NVARCHAR(12)   NOT NULL,
    estado             NVARCHAR(12)   NOT NULL DEFAULT 'PROGRAMADO',
    fecha_programada   DATE           NOT NULL,
    fecha_inicio       DATE           NULL,
    fecha_fin          DATE           NULL,
    diagnostico        NVARCHAR(1000) NULL,
    trabajo_realizado  NVARCHAR(2000) NULL,
    costo              DECIMAL(12,2)  NOT NULL DEFAULT 0,
    proxima_fecha      DATE           NULL,
    observaciones      NVARCHAR(1000) NULL,
    usuario_id         BIGINT         NULL,
    created_at         DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at         DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_mantenimientos_codigo UNIQUE (codigo),
    CONSTRAINT fk_mantenimientos_activo FOREIGN KEY (activo_id) REFERENCES SGAT.activos (id) ON DELETE CASCADE,
    -- PostgreSQL: ON DELETE SET NULL (segundo camino de cascada desde activos, no admitido en SQL Server)
    CONSTRAINT fk_mantenimientos_averia FOREIGN KEY (averia_id) REFERENCES SGAT.averias (id),
    CONSTRAINT fk_mantenimientos_tecnico FOREIGN KEY (tecnico_id) REFERENCES SGAT.tecnicos (id),
    CONSTRAINT fk_mantenimientos_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_mantenimientos_tipo CHECK (tipo IN ('PREVENTIVO', 'CORRECTIVO')),
    CONSTRAINT ck_mantenimientos_estado CHECK (estado IN ('PROGRAMADO', 'EN_PROCESO', 'FINALIZADO', 'CANCELADO')),
    CONSTRAINT ck_mantenimientos_costo CHECK (costo >= 0),
    CONSTRAINT ck_mantenimientos_fechas CHECK (fecha_fin IS NULL OR fecha_inicio IS NULL OR fecha_fin >= fecha_inicio),
    CONSTRAINT ck_mantenimientos_finalizado CHECK (estado <> 'FINALIZADO' OR fecha_fin IS NOT NULL)
);
GO
EXEC SGAT.sp_comentar N'mantenimientos', N'Mantenimientos preventivos y correctivos con su técnico responsable';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_mantenimientos_activo' AND object_id = OBJECT_ID(N'SGAT.mantenimientos'))
    CREATE INDEX ix_mantenimientos_activo ON SGAT.mantenimientos (activo_id, fecha_programada DESC);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_mantenimientos_estado' AND object_id = OBJECT_ID(N'SGAT.mantenimientos'))
    CREATE INDEX ix_mantenimientos_estado ON SGAT.mantenimientos (estado, fecha_programada);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_mantenimientos_tecnico' AND object_id = OBJECT_ID(N'SGAT.mantenimientos'))
    CREATE INDEX ix_mantenimientos_tecnico ON SGAT.mantenimientos (tecnico_id);
GO
EXEC SGAT.sp_crear_trigger_updated_at N'mantenimientos';
GO

-- -----------------------------------------------------------------------------
--  Reemplazos de componentes (RAM, disco, fuente, etc.)
--  Puede originarse en un mantenimiento o registrarse de forma independiente.
--  El componente nuevo puede ser un activo inventariado o un repuesto de bodega.
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.reemplazos', N'U') IS NULL
CREATE TABLE SGAT.reemplazos (
    id                        BIGINT IDENTITY(1,1) PRIMARY KEY,
    activo_id                 BIGINT         NOT NULL,
    mantenimiento_id          BIGINT         NULL,
    componente_retirado_id    BIGINT         NULL,
    componente_instalado_id   BIGINT         NULL,
    repuesto_id               BIGINT         NULL,
    cantidad                  INT            NOT NULL DEFAULT 1,
    descripcion               NVARCHAR(500)  NOT NULL,
    motivo                    NVARCHAR(300)  NULL,
    costo                     DECIMAL(12,2)  NOT NULL DEFAULT 0,
    fecha                     DATE           NOT NULL DEFAULT CAST(GETDATE() AS DATE),
    usuario_id                BIGINT         NULL,
    created_at                DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT fk_reemplazos_activo FOREIGN KEY (activo_id) REFERENCES SGAT.activos (id) ON DELETE CASCADE,
    -- PostgreSQL: ON DELETE SET NULL (segundo camino de cascada desde activos, no admitido en SQL Server)
    CONSTRAINT fk_reemplazos_mantenimiento FOREIGN KEY (mantenimiento_id) REFERENCES SGAT.mantenimientos (id),
    CONSTRAINT fk_reemplazos_retirado FOREIGN KEY (componente_retirado_id) REFERENCES SGAT.activos (id),
    CONSTRAINT fk_reemplazos_instalado FOREIGN KEY (componente_instalado_id) REFERENCES SGAT.activos (id),
    CONSTRAINT fk_reemplazos_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_reemplazos_cantidad CHECK (cantidad > 0),
    CONSTRAINT ck_reemplazos_costo CHECK (costo >= 0)
);
GO
EXEC SGAT.sp_comentar N'reemplazos', N'Historial de reemplazo de componentes de los equipos';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_reemplazos_activo' AND object_id = OBJECT_ID(N'SGAT.reemplazos'))
    CREATE INDEX ix_reemplazos_activo ON SGAT.reemplazos (activo_id, fecha DESC);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_reemplazos_mantenimiento' AND object_id = OBJECT_ID(N'SGAT.reemplazos'))
    CREATE INDEX ix_reemplazos_mantenimiento ON SGAT.reemplazos (mantenimiento_id);
GO

-- -----------------------------------------------------------------------------
--  Historial unificado del activo (línea de tiempo)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.historial_activos', N'U') IS NULL
CREATE TABLE SGAT.historial_activos (
    id             BIGINT IDENTITY(1,1) PRIMARY KEY,
    activo_id      BIGINT         NOT NULL,
    tipo_evento    NVARCHAR(20)   NOT NULL,
    descripcion    NVARCHAR(500)  NOT NULL,
    datos          NVARCHAR(MAX)  NOT NULL DEFAULT N'{}',
    referencia     NVARCHAR(30)   NULL,
    referencia_id  BIGINT         NULL,
    usuario_id     BIGINT         NULL,
    created_at     DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT fk_historial_activo FOREIGN KEY (activo_id) REFERENCES SGAT.activos (id) ON DELETE CASCADE,
    CONSTRAINT fk_historial_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id) ON DELETE SET NULL,
    CONSTRAINT ck_historial_tipo CHECK (tipo_evento IN (
        'REGISTRO', 'ACTUALIZACION', 'CAMBIO_ESTADO', 'ASIGNACION', 'DEVOLUCION', 'TRASLADO',
        'MANTENIMIENTO', 'REEMPLAZO', 'AVERIA', 'BAJA', 'ACTA')),
    CONSTRAINT ck_historial_datos CHECK (ISJSON(datos) = 1)
);
GO
EXEC SGAT.sp_comentar N'historial_activos', N'Línea de tiempo de eventos de cada activo';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_historial_activo' AND object_id = OBJECT_ID(N'SGAT.historial_activos'))
    CREATE INDEX ix_historial_activo ON SGAT.historial_activos (activo_id, created_at DESC);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_historial_fecha' AND object_id = OBJECT_ID(N'SGAT.historial_activos'))
    CREATE INDEX ix_historial_fecha ON SGAT.historial_activos (created_at DESC);
GO
