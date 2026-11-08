-- =============================================================================
--  SGAT - Script 14 - Reporte de daños desde las agencias
--
--  Se puede ejecutar sobre una base existente (no borra datos) y más de una vez.
--
--  El encargado de agencia reporta el daño de un equipo (avería) y puede pedir su reemplazo;
--  el administrador lo ve como "avería por atender" y envía el equipo de reemplazo.
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

IF COL_LENGTH(N'SGAT.averias', N'solicita_reemplazo') IS NULL
    ALTER TABLE SGAT.averias ADD solicita_reemplazo BIT NOT NULL
        CONSTRAINT df_averias_solicita_reemplazo DEFAULT 0;
GO
EXEC SGAT.sp_comentar N'averias', N'Quien reportó pide un equipo de reemplazo', N'solicita_reemplazo';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_averias_abiertas' AND object_id = OBJECT_ID(N'SGAT.averias'))
    CREATE INDEX ix_averias_abiertas ON SGAT.averias (estado) WHERE estado IN ('REPORTADA', 'EN_REVISION');
GO
