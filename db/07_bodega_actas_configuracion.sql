-- =============================================================================
--  SGAT - Script 07 - Bodega y repuestos, actas, configuración y generación de códigos
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
--  Repuestos / insumos en bodega (con control de stock)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.repuestos', N'U') IS NULL
CREATE TABLE SGAT.repuestos (
    id               BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo           NVARCHAR(30)   NOT NULL,
    nombre           NVARCHAR(150)  NOT NULL,
    categoria_id     BIGINT         NULL,
    marca_id         BIGINT         NULL,
    descripcion      NVARCHAR(500)  NULL,
    unidad           NVARCHAR(20)   NOT NULL DEFAULT 'UNIDAD',
    stock_actual     INT            NOT NULL DEFAULT 0,
    stock_minimo     INT            NOT NULL DEFAULT 0,
    costo_unitario   DECIMAL(12,2)  NOT NULL DEFAULT 0,
    agencia_id       BIGINT         NULL,
    ubicacion        NVARCHAR(120)  NULL,
    habilitado       BIT            NOT NULL DEFAULT 1,
    created_at       DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at       DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_repuestos_codigo UNIQUE (codigo),
    CONSTRAINT fk_repuestos_categoria FOREIGN KEY (categoria_id) REFERENCES SGAT.categorias (id),
    CONSTRAINT fk_repuestos_marca FOREIGN KEY (marca_id) REFERENCES SGAT.marcas (id),
    CONSTRAINT fk_repuestos_agencia FOREIGN KEY (agencia_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT ck_repuestos_stock CHECK (stock_actual >= 0),
    CONSTRAINT ck_repuestos_minimo CHECK (stock_minimo >= 0),
    CONSTRAINT ck_repuestos_costo CHECK (costo_unitario >= 0)
);
GO
EXEC SGAT.sp_comentar N'repuestos', N'Repuestos e insumos almacenados en bodega';
EXEC SGAT.sp_crear_trigger_updated_at N'repuestos';
GO

IF OBJECT_ID(N'SGAT.fk_reemplazos_repuesto', N'F') IS NULL
ALTER TABLE SGAT.reemplazos
    ADD CONSTRAINT fk_reemplazos_repuesto FOREIGN KEY (repuesto_id) REFERENCES SGAT.repuestos (id);
GO

-- -----------------------------------------------------------------------------
--  Movimientos de bodega (kardex)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.movimientos_bodega', N'U') IS NULL
CREATE TABLE SGAT.movimientos_bodega (
    id                BIGINT IDENTITY(1,1) PRIMARY KEY,
    repuesto_id       BIGINT         NOT NULL,
    tipo              NVARCHAR(10)   NOT NULL,
    cantidad          INT            NOT NULL,
    stock_anterior    INT            NOT NULL DEFAULT 0,
    stock_resultante  INT            NOT NULL DEFAULT 0,
    costo_unitario    DECIMAL(12,2)  NOT NULL DEFAULT 0,
    motivo            NVARCHAR(300)  NOT NULL,
    documento         NVARCHAR(60)   NULL,
    activo_id         BIGINT         NULL,
    mantenimiento_id  BIGINT         NULL,
    usuario_id        BIGINT         NULL,
    created_at        DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT fk_movimientos_repuesto FOREIGN KEY (repuesto_id) REFERENCES SGAT.repuestos (id),
    CONSTRAINT fk_movimientos_activo FOREIGN KEY (activo_id) REFERENCES SGAT.activos (id) ON DELETE SET NULL,
    -- PostgreSQL: ON DELETE SET NULL (segundo camino de cascada desde activos, no admitido en SQL Server)
    CONSTRAINT fk_movimientos_mantenimiento FOREIGN KEY (mantenimiento_id) REFERENCES SGAT.mantenimientos (id),
    CONSTRAINT fk_movimientos_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_movimientos_tipo CHECK (tipo IN ('ENTRADA', 'SALIDA', 'AJUSTE')),
    CONSTRAINT ck_movimientos_cantidad CHECK (
        (tipo IN ('ENTRADA', 'SALIDA') AND cantidad > 0) OR (tipo = 'AJUSTE' AND cantidad >= 0))
);
GO
EXEC SGAT.sp_comentar N'movimientos_bodega', N'Kardex de entradas, salidas y ajustes de repuestos';
EXEC SGAT.sp_comentar N'movimientos_bodega', N'En AJUSTE representa el nuevo stock físico contado', N'cantidad';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_movimientos_repuesto' AND object_id = OBJECT_ID(N'SGAT.movimientos_bodega'))
    CREATE INDEX ix_movimientos_repuesto ON SGAT.movimientos_bodega (repuesto_id, created_at DESC);
GO

-- Actualiza el stock del repuesto al registrar un movimiento (y valida stock suficiente).
-- SQL Server no tiene triggers BEFORE: es INSTEAD OF INSERT, calcula stock_anterior/resultante e
-- inserta la fila. Errores: 51001 = stock insuficiente, 51002 = repuesto inexistente.
CREATE OR ALTER TRIGGER SGAT.trg_movimientos_stock
ON SGAT.movimientos_bodega
INSTEAD OF INSERT
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @repuesto_id BIGINT, @tipo NVARCHAR(10), @cantidad INT, @costo DECIMAL(12,2), @motivo NVARCHAR(300),
            @documento NVARCHAR(60), @activo_id BIGINT, @mantenimiento_id BIGINT, @usuario_id BIGINT,
            @created_at DATETIMEOFFSET, @stock INT, @resultante INT, @msg NVARCHAR(400);

    DECLARE c_mov CURSOR LOCAL FAST_FORWARD FOR
        SELECT repuesto_id, tipo, cantidad, costo_unitario, motivo, documento, activo_id, mantenimiento_id,
               usuario_id, created_at
        FROM inserted;
    OPEN c_mov;
    FETCH NEXT FROM c_mov INTO @repuesto_id, @tipo, @cantidad, @costo, @motivo, @documento, @activo_id,
                               @mantenimiento_id, @usuario_id, @created_at;
    WHILE @@FETCH_STATUS = 0
    BEGIN
        SET @stock = NULL;
        SELECT @stock = stock_actual FROM SGAT.repuestos WITH (UPDLOCK, ROWLOCK) WHERE id = @repuesto_id;

        IF @stock IS NULL
        BEGIN
            SET @msg = CONCAT(N'Repuesto ', @repuesto_id, N' no existe');
            THROW 51002, @msg, 1;
        END

        SET @resultante = CASE @tipo
            WHEN 'ENTRADA' THEN @stock + @cantidad
            WHEN 'SALIDA'  THEN @stock - @cantidad
            ELSE @cantidad
        END;

        IF @resultante < 0
        BEGIN
            SET @msg = CONCAT(N'Stock insuficiente: disponible ', @stock, N', solicitado ', @cantidad);
            THROW 51001, @msg, 1;
        END

        UPDATE SGAT.repuestos SET stock_actual = @resultante WHERE id = @repuesto_id;

        -- Se inserta al final: @@IDENTITY queda con el id del movimiento (lo usa la API)
        INSERT INTO SGAT.movimientos_bodega (repuesto_id, tipo, cantidad, stock_anterior, stock_resultante, costo_unitario,
                                             motivo, documento, activo_id, mantenimiento_id, usuario_id, created_at)
        VALUES (@repuesto_id, @tipo, @cantidad, @stock, @resultante, COALESCE(@costo, 0), @motivo, @documento, @activo_id,
                @mantenimiento_id, @usuario_id, COALESCE(@created_at, SYSDATETIMEOFFSET()));

        FETCH NEXT FROM c_mov INTO @repuesto_id, @tipo, @cantidad, @costo, @motivo, @documento, @activo_id,
                                   @mantenimiento_id, @usuario_id, @created_at;
    END
    CLOSE c_mov;
    DEALLOCATE c_mov;
END;
GO

-- -----------------------------------------------------------------------------
--  Actas de entrega, devolución, traslado y baja
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.actas', N'U') IS NULL
CREATE TABLE SGAT.actas (
    id               BIGINT IDENTITY(1,1) PRIMARY KEY,
    numero           NVARCHAR(25)   NOT NULL,
    tipo             NVARCHAR(12)   NOT NULL,
    fecha            DATE           NOT NULL DEFAULT CAST(GETDATE() AS DATE),
    empleado_id      BIGINT         NULL,
    agencia_id       BIGINT         NOT NULL,
    departamento_id  BIGINT         NULL,
    entregado_por    NVARCHAR(150)  NULL,
    recibido_por     NVARCHAR(150)  NULL,
    observaciones    NVARCHAR(1000) NULL,
    anulada          BIT            NOT NULL DEFAULT 0,
    usuario_id       BIGINT         NULL,
    created_at       DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_actas_numero UNIQUE (numero),
    CONSTRAINT fk_actas_empleado FOREIGN KEY (empleado_id) REFERENCES SGAT.empleados (id),
    CONSTRAINT fk_actas_agencia FOREIGN KEY (agencia_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_actas_departamento FOREIGN KEY (departamento_id) REFERENCES SGAT.departamentos (id),
    CONSTRAINT fk_actas_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id),
    CONSTRAINT ck_actas_tipo CHECK (tipo IN ('ENTREGA', 'DEVOLUCION', 'TRASLADO', 'BAJA'))
);
GO
EXEC SGAT.sp_comentar N'actas', N'Actas firmadas de entrega-recepción, devolución, traslado y baja';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_actas_fecha' AND object_id = OBJECT_ID(N'SGAT.actas'))
    CREATE INDEX ix_actas_fecha ON SGAT.actas (fecha DESC);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_actas_empleado' AND object_id = OBJECT_ID(N'SGAT.actas'))
    CREATE INDEX ix_actas_empleado ON SGAT.actas (empleado_id);
GO

IF OBJECT_ID(N'SGAT.actas_detalle', N'U') IS NULL
CREATE TABLE SGAT.actas_detalle (
    id             BIGINT IDENTITY(1,1) PRIMARY KEY,
    acta_id        BIGINT         NOT NULL,
    activo_id      BIGINT         NOT NULL,
    estado_activo  NVARCHAR(20)   NOT NULL,
    condicion      NVARCHAR(10)   NOT NULL,
    observacion    NVARCHAR(300)  NULL,
    CONSTRAINT fk_actas_detalle_acta FOREIGN KEY (acta_id) REFERENCES SGAT.actas (id) ON DELETE CASCADE,
    CONSTRAINT fk_actas_detalle_activo FOREIGN KEY (activo_id) REFERENCES SGAT.activos (id),
    CONSTRAINT uq_actas_detalle UNIQUE (acta_id, activo_id)
);
GO
EXEC SGAT.sp_comentar N'actas_detalle', N'Activos incluidos en cada acta (con su estado al momento del acta)';
GO

-- -----------------------------------------------------------------------------
--  Parámetros de configuración del sistema
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.configuracion', N'U') IS NULL
CREATE TABLE SGAT.configuracion (
    clave        NVARCHAR(60)   NOT NULL PRIMARY KEY,
    valor        NVARCHAR(MAX)  NOT NULL,
    descripcion  NVARCHAR(250)  NULL,
    grupo        NVARCHAR(30)   NOT NULL DEFAULT 'GENERAL',
    updated_by   BIGINT         NULL,
    updated_at   DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT fk_configuracion_usuario FOREIGN KEY (updated_by) REFERENCES SGAT.usuarios (id)
);
GO
EXEC SGAT.sp_comentar N'configuracion', N'Parámetros generales (datos de la institución, alertas, etc.)';
EXEC SGAT.sp_crear_trigger_updated_at N'configuracion', N'clave';
GO

-- -----------------------------------------------------------------------------
--  Generación automática de códigos legibles
--    Averías        : AVR-2026-000001
--    Mantenimientos : MNT-2026-000001
--    Actas          : ACT-2026-000001
--    (y ENV, PRV, VER en los scripts 09, 12 y 13)
--  En PostgreSQL lo hacía el trigger fn_generar_codigo_anual; en SQL Server la API llama a este
--  procedimiento justo antes del INSERT (los triggers BEFORE INSERT no existen).
-- -----------------------------------------------------------------------------
CREATE OR ALTER PROCEDURE SGAT.sp_generar_codigo_anual
    @prefijo NVARCHAR(5),
    @codigo  NVARCHAR(25) OUTPUT
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @anio NCHAR(4) = CAST(YEAR(SYSDATETIMEOFFSET()) AS NCHAR(4)),
            @clave NVARCHAR(40), @valor BIGINT;

    SET @clave = @prefijo + N'_' + @anio;
    EXEC SGAT.sp_siguiente_secuencia @clave, @valor OUTPUT;
    SET @codigo = @prefijo + N'-' + @anio + N'-' + SGAT.fn_lpad(CAST(@valor AS NVARCHAR(20)), 6, N'0');
END;
GO
