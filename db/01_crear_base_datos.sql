-- =============================================================================
--  SISTEMA DE GESTIÓN DE ACTIVOS TECNOLÓGICOS (SGAT)
--  Script 01 - Creación de la base de datos y usuario de aplicación (SQL Server)
-- -----------------------------------------------------------------------------
--  Ejecutar conectado como administrador (sa o un login sysadmin) en SSMS o sqlcmd.
--  Requisitos: SQL Server 2017 o superior y autenticación mixta (SQL Server y Windows)
--  habilitada para que la API pueda entrar con el login sgat_app.
--  Cambie la contraseña del login de aplicación antes de usar en producción.
-- =============================================================================
SET ANSI_NULLS ON;
SET QUOTED_IDENTIFIER ON;
GO



-- Base de datos: intercalación en español, sin distinguir mayúsculas (los índices únicos de
-- usuario, correo, marca y número de serie no distinguen mayúsculas, como el lower() de PostgreSQL)
IF DB_ID(N'sgat') IS NULL
    CREATE DATABASE sgat COLLATE Modern_Spanish_CI_AS;
GO

-- Lecturas sin bloqueos (versionado de filas, similar a PostgreSQL)
ALTER DATABASE sgat SET READ_COMMITTED_SNAPSHOT ON WITH ROLLBACK IMMEDIATE;
GO

-- Login con el que se conectará la API (sin privilegios de administrador)
IF NOT EXISTS (SELECT 1 FROM sys.server_principals WHERE name = N'sgat_app')
    CREATE LOGIN sgat_app WITH PASSWORD = N'SgatApp2026*', DEFAULT_DATABASE = sgat, CHECK_EXPIRATION = OFF;
GO

USE sgat;
GO

-- Usuario de base de datos. Su esquema por defecto es "SGAT" (equivale al search_path de PostgreSQL):
-- así la API consulta las tablas sin anteponer el esquema.
IF NOT EXISTS (SELECT 1 FROM sys.database_principals WHERE name = N'sgat_app')
    CREATE USER sgat_app FOR LOGIN sgat_app WITH DEFAULT_SCHEMA = SGAT;
ELSE
    ALTER USER sgat_app WITH DEFAULT_SCHEMA = SGAT;
GO

IF NOT EXISTS (SELECT 1 FROM sys.extended_properties WHERE class = 0 AND name = N'MS_Description')
    EXEC sys.sp_addextendedproperty @name = N'MS_Description',
         @value = N'Sistema de Gestión de Activos Tecnológicos - Cooperativa';
GO

-- =============================================================================
--  A continuación ejecute en orden los scripts 02 al 19 de esta carpeta,
--  o todos de una vez con 00_ejecutar_todo.sql en modo SQLCMD.
-- =============================================================================
