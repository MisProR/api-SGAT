-- =============================================================================
--  SGAT - Script 19 - Permisos del usuario de aplicación
--  Se ejecuta al final (después del 18_datos_demo). Los permisos sobre el esquema cubren también los
--  objetos que se creen en el futuro dentro de "SGAT".
-- =============================================================================
SET ANSI_NULLS ON;
SET QUOTED_IDENTIFIER ON;
GO
USE sgat;
GO

IF NOT EXISTS (SELECT 1 FROM sys.database_principals WHERE name = N'sgat_app')
BEGIN
    IF EXISTS (SELECT 1 FROM sys.server_principals WHERE name = N'sgat_app')
        CREATE USER sgat_app FOR LOGIN sgat_app WITH DEFAULT_SCHEMA = SGAT;
    ELSE
        PRINT N'No existe el login sgat_app: ejecute primero 01_crear_base_datos.sql';
END
GO

IF EXISTS (SELECT 1 FROM sys.database_principals WHERE name = N'sgat_app')
BEGIN
    ALTER USER sgat_app WITH DEFAULT_SCHEMA = SGAT;

    GRANT SELECT, INSERT, UPDATE, DELETE, EXECUTE ON SCHEMA::SGAT TO sgat_app;

    -- La auditoría y el historial son solo de inserción (no se pueden alterar)
    DENY UPDATE, DELETE ON SGAT.auditoria TO sgat_app;
    DENY UPDATE ON SGAT.historial_activos TO sgat_app;
END
GO
