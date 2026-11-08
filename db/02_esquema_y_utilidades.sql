-- =============================================================================
--  SGAT - Script 02 - Esquema y funciones / procedimientos utilitarios (SQL Server)
--  Ejecutar sobre la base: sgat
--
--  Equivalencias con la versión PostgreSQL:
--    pgcrypto  -> los hash bcrypt los genera la API (Go); los datos iniciales traen el hash ya calculado
--    unaccent  -> búsquedas con la intercalación Latin1_General_CI_AI (sin tildes ni mayúsculas)
--    pg_trgm   -> no aplica (los índices GIN de trigramas no existen en SQL Server)
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

-- Esquema propio de la aplicación: SGAT
IF SCHEMA_ID(N'SGAT') IS NULL
    EXEC (N'CREATE SCHEMA SGAT AUTHORIZATION dbo');
GO

-- -----------------------------------------------------------------------------
--  Procedimiento: agrega o actualiza la descripción (MS_Description) de una tabla,
--  vista o columna. Reemplaza a COMMENT ON de PostgreSQL.
-- -----------------------------------------------------------------------------
CREATE OR ALTER PROCEDURE SGAT.sp_comentar
    @objeto  SYSNAME,
    @texto   NVARCHAR(500),
    @columna SYSNAME = NULL,
    @tipo    VARCHAR(10) = 'TABLE'
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @existe BIT = 0;

    IF @columna IS NULL
    BEGIN
        IF EXISTS (SELECT 1 FROM sys.extended_properties
                   WHERE major_id = OBJECT_ID(N'SGAT.' + @objeto) AND minor_id = 0 AND name = N'MS_Description')
            SET @existe = 1;
        IF @existe = 1
            EXEC sys.sp_updateextendedproperty N'MS_Description', @texto, N'SCHEMA', N'SGAT', @tipo, @objeto;
        ELSE
            EXEC sys.sp_addextendedproperty N'MS_Description', @texto, N'SCHEMA', N'SGAT', @tipo, @objeto;
    END
    ELSE
    BEGIN
        IF EXISTS (SELECT 1 FROM sys.extended_properties
                   WHERE major_id = OBJECT_ID(N'SGAT.' + @objeto)
                     AND minor_id = COLUMNPROPERTY(OBJECT_ID(N'SGAT.' + @objeto), @columna, 'ColumnId')
                     AND name = N'MS_Description')
            SET @existe = 1;
        IF @existe = 1
            EXEC sys.sp_updateextendedproperty N'MS_Description', @texto, N'SCHEMA', N'SGAT', @tipo, @objeto, N'COLUMN', @columna;
        ELSE
            EXEC sys.sp_addextendedproperty N'MS_Description', @texto, N'SCHEMA', N'SGAT', @tipo, @objeto, N'COLUMN', @columna;
    END
END;
GO

-- -----------------------------------------------------------------------------
--  Procedimiento: crea el trigger que mantiene actualizada la columna updated_at
--  (equivale a SGAT.fn_set_updated_at + CREATE TRIGGER ... BEFORE UPDATE).
--  SQL Server no tiene triggers BEFORE: es un AFTER UPDATE que vuelve a fijar la fecha.
-- -----------------------------------------------------------------------------
CREATE OR ALTER PROCEDURE SGAT.sp_crear_trigger_updated_at
    @tabla SYSNAME,
    @pk    SYSNAME = N'id'
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @sql NVARCHAR(MAX) = N'
CREATE OR ALTER TRIGGER SGAT.' + QUOTENAME(N'trg_' + @tabla + N'_updated_at') + N'
ON SGAT.' + QUOTENAME(@tabla) + N'
AFTER UPDATE
AS
BEGIN
    SET NOCOUNT ON;
    IF NOT EXISTS (SELECT 1 FROM inserted) RETURN;
    UPDATE t SET updated_at = SYSDATETIMEOFFSET()
    FROM SGAT.' + QUOTENAME(@tabla) + N' t
    JOIN inserted i ON i.' + QUOTENAME(@pk) + N' = t.' + QUOTENAME(@pk) + N';
END;';
    EXEC sys.sp_executesql @sql;
END;
GO

-- -----------------------------------------------------------------------------
--  Secuencias por clave (códigos legibles: LAP-000001, MNT-2026-000001, ...)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.secuencias', N'U') IS NULL
CREATE TABLE SGAT.secuencias (
    clave       NVARCHAR(40)   NOT NULL PRIMARY KEY,
    valor       BIGINT         NOT NULL DEFAULT 0,
    updated_at  DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET()
);
GO
EXEC SGAT.sp_comentar N'secuencias', N'Contadores para generar códigos legibles por prefijo';
GO

-- Devuelve el siguiente valor de la clave. Seguro ante concurrencia: UPDLOCK + HOLDLOCK bloquea la
-- clave (exista o no) hasta el fin de la transacción que lo llama.
CREATE OR ALTER PROCEDURE SGAT.sp_siguiente_secuencia
    @clave NVARCHAR(40),
    @valor BIGINT OUTPUT
AS
BEGIN
    SET NOCOUNT ON;
    SET XACT_ABORT ON;
    BEGIN TRANSACTION;

    UPDATE SGAT.secuencias WITH (UPDLOCK, HOLDLOCK)
       SET @valor = valor = valor + 1,
           updated_at = SYSDATETIMEOFFSET()
     WHERE clave = @clave;

    IF @@ROWCOUNT = 0
    BEGIN
        INSERT INTO SGAT.secuencias (clave, valor) VALUES (@clave, 1);
        SET @valor = 1;
    END

    COMMIT TRANSACTION;
END;
GO

-- lpad de PostgreSQL: rellena a la izquierda hasta @largo (y recorta si el texto es más largo)
CREATE OR ALTER FUNCTION SGAT.fn_lpad (@texto NVARCHAR(100), @largo INT, @relleno NCHAR(1))
RETURNS NVARCHAR(100)
WITH SCHEMABINDING
AS
BEGIN
    RETURN CASE WHEN LEN(@texto) >= @largo THEN LEFT(@texto, @largo)
                ELSE REPLICATE(@relleno, @largo - LEN(@texto)) + @texto END;
END;
GO

-- Normaliza texto para búsquedas (minúsculas y sin tildes). La API busca con la intercalación
-- Latin1_General_CI_AI, que ya ignora tildes y mayúsculas; esta función se deja por compatibilidad.
CREATE OR ALTER FUNCTION SGAT.fn_normalizar (@texto NVARCHAR(MAX))
RETURNS NVARCHAR(MAX)
WITH SCHEMABINDING
AS
BEGIN
    RETURN TRANSLATE(LOWER(COALESCE(@texto, N'')),
                     N'áéíóúàèìòùäëïöüâêîôûñç',
                     N'aeiouaeiouaeiouaeiounc');
END;
GO
