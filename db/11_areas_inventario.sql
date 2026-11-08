-- =============================================================================
--  SGAT - Script 11 - Áreas de inventario
--
--  Se puede ejecutar sobre una base existente (no borra datos) y más de una vez.
--
--  Cada categoría pertenece a un área y cada rol administra la suya:
--    SEGURIDAD_FISICA : computadoras, laptops, CPU, monitores, impresoras, UPS, redes,
--                       contadoras de billetes, cámaras...   -> ADMIN (seguridad física)
--    PROVEEDURIA      : audífonos, teclados, mouse, parlantes y materiales
--                       -> Encargado de proveeduría
--  El SUPERADMIN ve todas las áreas; el encargado de agencia ve todo lo de su agencia.
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

IF COL_LENGTH(N'SGAT.categorias', N'area') IS NULL
    ALTER TABLE SGAT.categorias ADD area NVARCHAR(20) NOT NULL
        CONSTRAINT df_categorias_area DEFAULT 'SEGURIDAD_FISICA';
GO

IF OBJECT_ID(N'SGAT.ck_categorias_area', N'C') IS NULL
    ALTER TABLE SGAT.categorias
        ADD CONSTRAINT ck_categorias_area CHECK (area IN ('SEGURIDAD_FISICA', 'PROVEEDURIA'));
GO

EXEC SGAT.sp_comentar N'categorias', N'Área que administra la categoría: SEGURIDAD_FISICA o PROVEEDURIA', N'area';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_categorias_area' AND object_id = OBJECT_ID(N'SGAT.categorias'))
    CREATE INDEX ix_categorias_area ON SGAT.categorias (area);
GO

-- Nombre del rol ADMIN y categorías por área: se cargan en 17_datos_iniciales.sql
