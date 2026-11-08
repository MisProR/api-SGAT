-- =============================================================================
--  SGAT - Script 15 - Mobiliario por área, periféricos del admin y bajas de materiales
--
--  Se puede ejecutar sobre una base existente (no borra datos) y más de una vez.
--
--  1. Periféricos (teclado, mouse, audífonos, parlantes) con código vuelven a ser activos del
--     admin (seguridad física). Los que proveeduría compra sueltos se controlan como stock en su bodega.
--  2. Mobiliario (escritorios, sillas, mesas, archivadores...) son bienes con código del área de
--     proveeduría, a cargo del jefe de cada departamento, con acta y verificación anual.
--  3. El empleado puede marcarse como jefe de su departamento (responsable del área).
--  4. Kardex: tipo BAJA para materiales dañados o vencidos (descuenta stock, distinto de una salida).
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

-- 1. Área de los periféricos: se fija en 17_datos_iniciales.sql

-- 2. Mobiliario
IF OBJECT_ID(N'SGAT.ck_categorias_tipo', N'C') IS NOT NULL
    ALTER TABLE SGAT.categorias DROP CONSTRAINT ck_categorias_tipo;
ALTER TABLE SGAT.categorias ADD CONSTRAINT ck_categorias_tipo
    CHECK (tipo IN ('EQUIPO', 'COMPONENTE', 'PERIFERICO', 'MOBILIARIO'));
GO

-- Categorías de mobiliario: se cargan en 17_datos_iniciales.sql

-- 3. Jefe de departamento (responsable de los bienes del área en su agencia)
IF COL_LENGTH(N'SGAT.empleados', N'es_jefe') IS NULL
    ALTER TABLE SGAT.empleados ADD es_jefe BIT NOT NULL CONSTRAINT df_empleados_es_jefe DEFAULT 0;
GO
EXEC SGAT.sp_comentar N'empleados', N'Jefe de su departamento en su agencia: responsable de los bienes del área', N'es_jefe';
GO

-- 4. Baja de materiales en el kardex
IF OBJECT_ID(N'SGAT.ck_movimientos_tipo', N'C') IS NOT NULL
    ALTER TABLE SGAT.movimientos_bodega DROP CONSTRAINT ck_movimientos_tipo;
ALTER TABLE SGAT.movimientos_bodega ADD CONSTRAINT ck_movimientos_tipo
    CHECK (tipo IN ('ENTRADA', 'SALIDA', 'AJUSTE', 'BAJA'));
IF OBJECT_ID(N'SGAT.ck_movimientos_cantidad', N'C') IS NOT NULL
    ALTER TABLE SGAT.movimientos_bodega DROP CONSTRAINT ck_movimientos_cantidad;
ALTER TABLE SGAT.movimientos_bodega ADD CONSTRAINT ck_movimientos_cantidad CHECK (
    (tipo IN ('ENTRADA', 'SALIDA', 'BAJA') AND cantidad > 0) OR (tipo = 'AJUSTE' AND cantidad >= 0));
GO

-- El trigger de stock ahora también descuenta en las BAJAS
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
            WHEN 'BAJA'    THEN @stock - @cantidad
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
