-- =============================================================================
--  SGAT - Script 05 - Catálogos e inventario de activos
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
--  Categorías de activo
--    EQUIPO     : computadoras, laptops, impresoras, UPS, cámaras
--    COMPONENTE : CPU/procesador, RAM, discos, fuentes de poder (van dentro de un equipo)
--    PERIFERICO : monitores, teclados, mouse, audífonos, parlantes
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.categorias', N'U') IS NULL
CREATE TABLE SGAT.categorias (
    id                  BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo              NVARCHAR(20)   NOT NULL,
    nombre              NVARCHAR(80)   NOT NULL,
    tipo                NVARCHAR(12)   NOT NULL,
    prefijo             NVARCHAR(5)    NOT NULL,
    icono               NVARCHAR(40)   NULL,
    vida_util_meses     SMALLINT       NOT NULL DEFAULT 60,
    meses_mantenimiento SMALLINT       NOT NULL DEFAULT 6,
    requiere_serie      BIT            NOT NULL DEFAULT 1,
    habilitado          BIT            NOT NULL DEFAULT 1,
    created_at          DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at          DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_categorias_codigo UNIQUE (codigo),
    CONSTRAINT uq_categorias_prefijo UNIQUE (prefijo),
    CONSTRAINT ck_categorias_tipo CHECK (tipo IN ('EQUIPO', 'COMPONENTE', 'PERIFERICO')),
    CONSTRAINT ck_categorias_vida_util CHECK (vida_util_meses > 0),
    CONSTRAINT ck_categorias_mantenimiento CHECK (meses_mantenimiento >= 0)
);
GO
EXEC SGAT.sp_comentar N'categorias', N'Tipos de activo tecnológico';
EXEC SGAT.sp_comentar N'categorias', N'Frecuencia de mantenimiento preventivo en meses (0 = no aplica)', N'meses_mantenimiento';
EXEC SGAT.sp_crear_trigger_updated_at N'categorias';
GO

-- -----------------------------------------------------------------------------
--  Marcas
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.marcas', N'U') IS NULL
CREATE TABLE SGAT.marcas (
    id          BIGINT IDENTITY(1,1) PRIMARY KEY,
    nombre      NVARCHAR(80)   NOT NULL,
    habilitado  BIT            NOT NULL DEFAULT 1,
    created_at  DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at  DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET()
);
GO
-- Sin distinguir mayúsculas por la intercalación (equivale a UNIQUE (lower(nombre)))
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'uq_marcas_nombre' AND object_id = OBJECT_ID(N'SGAT.marcas'))
    CREATE UNIQUE INDEX uq_marcas_nombre ON SGAT.marcas (nombre);
GO
EXEC SGAT.sp_crear_trigger_updated_at N'marcas';
GO

-- -----------------------------------------------------------------------------
--  Proveedores
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.proveedores', N'U') IS NULL
CREATE TABLE SGAT.proveedores (
    id             BIGINT IDENTITY(1,1) PRIMARY KEY,
    ruc            NVARCHAR(13)   NOT NULL,
    razon_social   NVARCHAR(200)  NOT NULL,
    contacto       NVARCHAR(120)  NULL,
    telefono       NVARCHAR(30)   NULL,
    email          NVARCHAR(150)  NULL,
    direccion      NVARCHAR(250)  NULL,
    habilitado     BIT            NOT NULL DEFAULT 1,
    created_at     DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at     DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_proveedores_ruc UNIQUE (ruc),
    -- Equivale a: ruc ~ '^[0-9]{13}$'
    CONSTRAINT ck_proveedores_ruc CHECK (LEN(ruc) = 13 AND ruc NOT LIKE '%[^0-9]%')
);
GO
EXEC SGAT.sp_crear_trigger_updated_at N'proveedores';
GO

-- -----------------------------------------------------------------------------
--  ACTIVOS: inventario de equipos, componentes y periféricos
--  - activo_padre_id: componente instalado dentro de otro equipo (RAM en una PC)
--  - agencia/departamento/empleado: ubicación y responsable ACTUAL
--    (se mantienen desde asignaciones y traslados; el historial queda en esas tablas)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.activos', N'U') IS NULL
CREATE TABLE SGAT.activos (
    id                 BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo             NVARCHAR(20)   NOT NULL,
    categoria_id       BIGINT         NOT NULL,
    marca_id           BIGINT         NULL,
    modelo             NVARCHAR(120)  NULL,
    numero_serie       NVARCHAR(100)  NULL,
    codigo_contable    NVARCHAR(50)   NULL,
    descripcion        NVARCHAR(300)  NULL,
    especificaciones   NVARCHAR(MAX)  NOT NULL DEFAULT N'{}',
    estado             NVARCHAR(20)   NOT NULL DEFAULT 'ACTIVO',
    condicion          NVARCHAR(10)   NOT NULL DEFAULT 'BUENO',
    activo_padre_id    BIGINT         NULL,
    agencia_id         BIGINT         NOT NULL,
    departamento_id    BIGINT         NULL,
    empleado_id        BIGINT         NULL,
    proveedor_id       BIGINT         NULL,
    fecha_compra       DATE           NULL,
    valor_compra       DECIMAL(12,2)  NULL,
    numero_factura     NVARCHAR(50)   NULL,
    garantia_hasta     DATE           NULL,
    hostname           NVARCHAR(80)   NULL,
    direccion_ip       NVARCHAR(45)   NULL,
    direccion_mac      NVARCHAR(17)   NULL,
    observaciones      NVARCHAR(MAX)  NULL,
    fecha_baja         DATE           NULL,
    motivo_baja        NVARCHAR(300)  NULL,
    created_by         BIGINT         NULL,
    updated_by         BIGINT         NULL,
    created_at         DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at         DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_activos_codigo UNIQUE (codigo),
    CONSTRAINT fk_activos_categoria FOREIGN KEY (categoria_id) REFERENCES SGAT.categorias (id),
    CONSTRAINT fk_activos_marca FOREIGN KEY (marca_id) REFERENCES SGAT.marcas (id),
    -- SQL Server no admite ON DELETE SET NULL en una autorreferencia (ciclo de cascada):
    -- la API libera los componentes (activo_padre_id = NULL) antes de eliminar un equipo.
    CONSTRAINT fk_activos_padre FOREIGN KEY (activo_padre_id) REFERENCES SGAT.activos (id),
    CONSTRAINT fk_activos_agencia FOREIGN KEY (agencia_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_activos_departamento FOREIGN KEY (departamento_id) REFERENCES SGAT.departamentos (id),
    CONSTRAINT fk_activos_empleado FOREIGN KEY (empleado_id) REFERENCES SGAT.empleados (id),
    CONSTRAINT fk_activos_proveedor FOREIGN KEY (proveedor_id) REFERENCES SGAT.proveedores (id),
    CONSTRAINT fk_activos_created_by FOREIGN KEY (created_by) REFERENCES SGAT.usuarios (id),
    CONSTRAINT fk_activos_updated_by FOREIGN KEY (updated_by) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_activos_estado CHECK (estado IN ('ACTIVO', 'EN_MANTENIMIENTO', 'DANADO', 'DADO_DE_BAJA')),
    CONSTRAINT ck_activos_condicion CHECK (condicion IN ('NUEVO', 'BUENO', 'REGULAR', 'MALO')),
    CONSTRAINT ck_activos_valor CHECK (valor_compra IS NULL OR valor_compra >= 0),
    CONSTRAINT ck_activos_padre CHECK (activo_padre_id IS NULL OR activo_padre_id <> id),
    CONSTRAINT ck_activos_baja CHECK (estado <> 'DADO_DE_BAJA' OR fecha_baja IS NOT NULL),
    CONSTRAINT ck_activos_especificaciones CHECK (ISJSON(especificaciones) = 1)
);
GO
EXEC SGAT.sp_comentar N'activos', N'Inventario de activos tecnológicos';
EXEC SGAT.sp_comentar N'activos', N'Características técnicas según la categoría (procesador, ram_gb, almacenamiento, so, pulgadas, etc.)', N'especificaciones';
EXEC SGAT.sp_comentar N'activos', N'Equipo en el que está instalado este componente', N'activo_padre_id';
GO

-- Serie única por categoría (sin distinguir mayúsculas por la intercalación)
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'uq_activos_serie' AND object_id = OBJECT_ID(N'SGAT.activos'))
    CREATE UNIQUE INDEX uq_activos_serie ON SGAT.activos (categoria_id, numero_serie)
    WHERE numero_serie IS NOT NULL AND numero_serie <> '';
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_activos_categoria' AND object_id = OBJECT_ID(N'SGAT.activos'))
    CREATE INDEX ix_activos_categoria ON SGAT.activos (categoria_id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_activos_estado' AND object_id = OBJECT_ID(N'SGAT.activos'))
    CREATE INDEX ix_activos_estado ON SGAT.activos (estado);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_activos_agencia' AND object_id = OBJECT_ID(N'SGAT.activos'))
    CREATE INDEX ix_activos_agencia ON SGAT.activos (agencia_id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_activos_empleado' AND object_id = OBJECT_ID(N'SGAT.activos'))
    CREATE INDEX ix_activos_empleado ON SGAT.activos (empleado_id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_activos_padre' AND object_id = OBJECT_ID(N'SGAT.activos'))
    CREATE INDEX ix_activos_padre ON SGAT.activos (activo_padre_id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_activos_garantia' AND object_id = OBJECT_ID(N'SGAT.activos'))
    CREATE INDEX ix_activos_garantia ON SGAT.activos (garantia_hasta) WHERE estado <> 'DADO_DE_BAJA';
-- ix_activos_busqueda (GIN de trigramas) no existe en SQL Server: las búsquedas usan LIKE sin tildes
GO
EXEC SGAT.sp_crear_trigger_updated_at N'activos';
GO

-- Genera el código del activo con el prefijo de su categoría: LAP-000001
-- (en PostgreSQL era el trigger trg_activos_codigo; aquí la API lo llama antes del INSERT)
CREATE OR ALTER PROCEDURE SGAT.sp_generar_codigo_activo
    @categoria_id BIGINT,
    @codigo       NVARCHAR(20) OUTPUT
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @prefijo NVARCHAR(5), @valor BIGINT, @clave NVARCHAR(40);

    SELECT @prefijo = prefijo FROM SGAT.categorias WHERE id = @categoria_id;
    IF @prefijo IS NULL
    BEGIN
        SET @codigo = NULL;
        RETURN;
    END

    SET @clave = N'ACTIVO_' + @prefijo;
    EXEC SGAT.sp_siguiente_secuencia @clave, @valor OUTPUT;
    SET @codigo = @prefijo + N'-' + SGAT.fn_lpad(CAST(@valor AS NVARCHAR(20)), 6, N'0');
END;
GO
