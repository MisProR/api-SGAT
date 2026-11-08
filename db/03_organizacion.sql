-- =============================================================================
--  SGAT - Script 03 - Estructura organizacional
--  Agencias (matriz / sucursales), departamentos, empleados y técnicos
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
--  Agencias: matriz, sucursales, agencias y ventanillas de la cooperativa
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.agencias', N'U') IS NULL
CREATE TABLE SGAT.agencias (
    id              BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo          NVARCHAR(20)   NOT NULL,
    nombre          NVARCHAR(120)  NOT NULL,
    tipo            NVARCHAR(20)   NOT NULL DEFAULT 'AGENCIA',
    provincia       NVARCHAR(80)   NULL,
    ciudad          NVARCHAR(80)   NULL,
    direccion       NVARCHAR(250)  NULL,
    telefono        NVARCHAR(30)   NULL,
    email           NVARCHAR(150)  NULL,
    habilitado      BIT            NOT NULL DEFAULT 1,
    created_at      DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at      DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_agencias_codigo UNIQUE (codigo),
    CONSTRAINT ck_agencias_tipo CHECK (tipo IN ('MATRIZ', 'SUCURSAL', 'AGENCIA', 'VENTANILLA', 'BODEGA'))
);
GO
EXEC SGAT.sp_comentar N'agencias', N'Matriz, sucursales, agencias y bodegas de la cooperativa';
EXEC SGAT.sp_crear_trigger_updated_at N'agencias';
GO

-- -----------------------------------------------------------------------------
--  Departamentos (transversales a todas las agencias)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.departamentos', N'U') IS NULL
CREATE TABLE SGAT.departamentos (
    id              BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo          NVARCHAR(20)   NOT NULL,
    nombre          NVARCHAR(120)  NOT NULL,
    descripcion     NVARCHAR(250)  NULL,
    habilitado      BIT            NOT NULL DEFAULT 1,
    created_at      DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at      DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_departamentos_codigo UNIQUE (codigo),
    CONSTRAINT uq_departamentos_nombre UNIQUE (nombre)
);
GO
EXEC SGAT.sp_comentar N'departamentos', N'Áreas funcionales: Créditos, Cajas, Tecnología, etc.';
EXEC SGAT.sp_crear_trigger_updated_at N'departamentos';
GO

-- -----------------------------------------------------------------------------
--  Empleados: responsables de los activos
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.empleados', N'U') IS NULL
CREATE TABLE SGAT.empleados (
    id                BIGINT IDENTITY(1,1) PRIMARY KEY,
    cedula            NVARCHAR(13)   NOT NULL,
    nombres           NVARCHAR(100)  NOT NULL,
    apellidos         NVARCHAR(100)  NOT NULL,
    email             NVARCHAR(150)  NULL,
    telefono          NVARCHAR(30)   NULL,
    cargo             NVARCHAR(120)  NULL,
    agencia_id        BIGINT         NOT NULL,
    departamento_id   BIGINT         NOT NULL,
    fecha_ingreso     DATE           NULL,
    habilitado        BIT            NOT NULL DEFAULT 1,
    created_at        DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at        DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_empleados_cedula UNIQUE (cedula),
    CONSTRAINT fk_empleados_agencia FOREIGN KEY (agencia_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_empleados_departamento FOREIGN KEY (departamento_id) REFERENCES SGAT.departamentos (id),
    -- Equivale a: cedula ~ '^[0-9]{10}([0-9]{3})?$'
    CONSTRAINT ck_empleados_cedula CHECK (LEN(cedula) IN (10, 13) AND cedula NOT LIKE '%[^0-9]%')
);
GO
EXEC SGAT.sp_comentar N'empleados', N'Personal de la cooperativa responsable de activos';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_empleados_agencia' AND object_id = OBJECT_ID(N'SGAT.empleados'))
    CREATE INDEX ix_empleados_agencia ON SGAT.empleados (agencia_id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_empleados_departamento' AND object_id = OBJECT_ID(N'SGAT.empleados'))
    CREATE INDEX ix_empleados_departamento ON SGAT.empleados (departamento_id);
-- ix_empleados_busqueda (GIN de trigramas) no existe en SQL Server: las búsquedas usan LIKE sin tildes
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_empleados_busqueda' AND object_id = OBJECT_ID(N'SGAT.empleados'))
    CREATE INDEX ix_empleados_busqueda ON SGAT.empleados (apellidos, nombres) INCLUDE (cedula);
GO
EXEC SGAT.sp_crear_trigger_updated_at N'empleados';
GO

-- -----------------------------------------------------------------------------
--  Técnicos: internos (personal TI) o externos (proveedores de servicio)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.tecnicos', N'U') IS NULL
CREATE TABLE SGAT.tecnicos (
    id              BIGINT IDENTITY(1,1) PRIMARY KEY,
    nombres         NVARCHAR(150)  NOT NULL,
    tipo            NVARCHAR(10)   NOT NULL DEFAULT 'INTERNO',
    empresa         NVARCHAR(150)  NULL,
    especialidad    NVARCHAR(120)  NULL,
    telefono        NVARCHAR(30)   NULL,
    email           NVARCHAR(150)  NULL,
    empleado_id     BIGINT         NULL,
    habilitado      BIT            NOT NULL DEFAULT 1,
    created_at      DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at      DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT ck_tecnicos_tipo CHECK (tipo IN ('INTERNO', 'EXTERNO')),
    CONSTRAINT fk_tecnicos_empleado FOREIGN KEY (empleado_id) REFERENCES SGAT.empleados (id)
);
GO
EXEC SGAT.sp_comentar N'tecnicos', N'Técnicos responsables de mantenimientos y reparaciones';
EXEC SGAT.sp_crear_trigger_updated_at N'tecnicos';
GO
