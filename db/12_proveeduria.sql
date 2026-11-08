-- =============================================================================
--  SGAT - Script 12 - Bodega de proveeduría y entregas de materiales
--
--  Se puede ejecutar sobre una base existente (no borra datos) y más de una vez.
--
--  Flujo:
--    1. El ENCARGADO DE PROVEEDURÍA controla el stock de su bodega (repuestos con área
--       PROVEEDURIA): audífonos, teclados, mouse, tintas, cintas, papel...
--    2. Envía materiales a una agencia (ENTREGA DE MATERIALES, PRV-2026-000001): el stock
--       se descuenta al enviar y la entrega queda EN_TRANSITO.
--    3. El encargado de la agencia la RECIBE (y elige a qué empleado se entrega) -> se genera
--       el acta de entrega con los materiales; o la RECHAZA -> el stock vuelve a la bodega.
--       Si el destino no tiene encargado, proveeduría entrega directamente (ENTREGADO).
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

-- Rol PROVEEDURIA (id 4): se inserta en 17_datos_iniciales.sql

-- -----------------------------------------------------------------------------
--  Área de cada artículo de bodega: los repuestos de impresoras siguen en seguridad física
-- -----------------------------------------------------------------------------
IF COL_LENGTH(N'SGAT.repuestos', N'area') IS NULL
    ALTER TABLE SGAT.repuestos ADD area NVARCHAR(20) NOT NULL
        CONSTRAINT df_repuestos_area DEFAULT 'SEGURIDAD_FISICA';
GO

IF OBJECT_ID(N'SGAT.ck_repuestos_area', N'C') IS NULL
    ALTER TABLE SGAT.repuestos
        ADD CONSTRAINT ck_repuestos_area CHECK (area IN ('SEGURIDAD_FISICA', 'PROVEEDURIA'));
GO

EXEC SGAT.sp_comentar N'repuestos', N'Bodega a la que pertenece: SEGURIDAD_FISICA (repuestos) o PROVEEDURIA (materiales)', N'area';
GO

-- La columna nueva va al final (igual que en PostgreSQL)
CREATE OR ALTER VIEW SGAT.v_repuestos AS
SELECT
    r.id,
    r.codigo,
    r.nombre,
    r.categoria_id,
    c.nombre                         AS categoria,
    r.marca_id,
    m.nombre                         AS marca,
    r.descripcion,
    r.unidad,
    r.stock_actual,
    r.stock_minimo,
    r.costo_unitario,
    CAST(r.stock_actual * r.costo_unitario AS DECIMAL(14,2)) AS valor_inventario,
    CAST(CASE WHEN r.stock_actual <= r.stock_minimo THEN 1 ELSE 0 END AS BIT) AS stock_bajo,
    r.agencia_id,
    ag.nombre                        AS agencia,
    r.ubicacion,
    r.habilitado,
    r.created_at,
    r.updated_at,
    r.area
FROM SGAT.repuestos r
LEFT JOIN SGAT.categorias c ON c.id = r.categoria_id
LEFT JOIN SGAT.marcas m     ON m.id = r.marca_id
LEFT JOIN SGAT.agencias ag  ON ag.id = r.agencia_id;
GO

-- -----------------------------------------------------------------------------
--  Entregas de materiales a las agencias
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.entregas_material', N'U') IS NULL
CREATE TABLE SGAT.entregas_material (
    id                        BIGINT IDENTITY(1,1) PRIMARY KEY,
    codigo                    NVARCHAR(20)   NOT NULL,
    agencia_destino_id        BIGINT         NOT NULL,
    departamento_id           BIGINT         NULL,
    empleado_id               BIGINT         NULL,
    motivo                    NVARCHAR(300)  NOT NULL,
    fecha                     DATE           NOT NULL DEFAULT CAST(GETDATE() AS DATE),
    estado                    NVARCHAR(12)   NOT NULL DEFAULT 'EN_TRANSITO',
    enviado_por               BIGINT         NULL,
    recibido_por              BIGINT         NULL,
    fecha_recepcion           DATETIMEOFFSET NULL,
    observaciones_recepcion   NVARCHAR(500)  NULL,
    acta_id                   BIGINT         NULL,
    created_at                DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at                DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_entregas_material_codigo UNIQUE (codigo),
    CONSTRAINT fk_entregas_material_agencia FOREIGN KEY (agencia_destino_id) REFERENCES SGAT.agencias (id),
    CONSTRAINT fk_entregas_material_dep FOREIGN KEY (departamento_id) REFERENCES SGAT.departamentos (id),
    CONSTRAINT fk_entregas_material_emp FOREIGN KEY (empleado_id) REFERENCES SGAT.empleados (id),
    CONSTRAINT fk_entregas_material_enviado FOREIGN KEY (enviado_por) REFERENCES SGAT.usuarios (id),
    CONSTRAINT fk_entregas_material_recibido FOREIGN KEY (recibido_por) REFERENCES SGAT.usuarios (id),
    CONSTRAINT fk_entregas_material_acta FOREIGN KEY (acta_id) REFERENCES SGAT.actas (id),
    CONSTRAINT ck_entregas_material_estado CHECK (estado IN ('EN_TRANSITO', 'RECIBIDO', 'RECHAZADO', 'ENTREGADO')),
    CONSTRAINT ck_entregas_material_recepcion CHECK (estado = 'EN_TRANSITO' OR fecha_recepcion IS NOT NULL)
);
GO
EXEC SGAT.sp_comentar N'entregas_material', N'Envíos de materiales de proveeduría a las agencias, con recepción del encargado';
EXEC SGAT.sp_comentar N'entregas_material', N'Empleado que recibe el material (NULL = uso de la agencia)', N'empleado_id';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_entregas_material_estado' AND object_id = OBJECT_ID(N'SGAT.entregas_material'))
    CREATE INDEX ix_entregas_material_estado ON SGAT.entregas_material (estado, agencia_destino_id);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_entregas_material_fecha' AND object_id = OBJECT_ID(N'SGAT.entregas_material'))
    CREATE INDEX ix_entregas_material_fecha ON SGAT.entregas_material (created_at DESC);
GO

EXEC SGAT.sp_crear_trigger_updated_at N'entregas_material';
-- Código legible PRV-2026-000001: lo genera la API con EXEC SGAT.sp_generar_codigo_anual 'PRV'
GO

IF OBJECT_ID(N'SGAT.entregas_material_detalle', N'U') IS NULL
CREATE TABLE SGAT.entregas_material_detalle (
    id               BIGINT IDENTITY(1,1) PRIMARY KEY,
    entrega_id       BIGINT        NOT NULL,
    repuesto_id      BIGINT        NOT NULL,
    cantidad         INT           NOT NULL,
    costo_unitario   DECIMAL(12,2) NOT NULL DEFAULT 0,
    CONSTRAINT fk_entregas_material_det_entrega FOREIGN KEY (entrega_id) REFERENCES SGAT.entregas_material (id) ON DELETE CASCADE,
    CONSTRAINT fk_entregas_material_det_repuesto FOREIGN KEY (repuesto_id) REFERENCES SGAT.repuestos (id),
    CONSTRAINT uq_entregas_material_det UNIQUE (entrega_id, repuesto_id),
    CONSTRAINT ck_entregas_material_det_cantidad CHECK (cantidad > 0)
);
GO
EXEC SGAT.sp_comentar N'entregas_material_detalle', N'Materiales y cantidades de cada entrega';
GO

-- -----------------------------------------------------------------------------
--  Materiales incluidos en un acta (además de los activos de actas_detalle)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.actas_materiales', N'U') IS NULL
CREATE TABLE SGAT.actas_materiales (
    id            BIGINT IDENTITY(1,1) PRIMARY KEY,
    acta_id       BIGINT   NOT NULL,
    repuesto_id   BIGINT   NOT NULL,
    cantidad      INT      NOT NULL,
    CONSTRAINT fk_actas_materiales_acta FOREIGN KEY (acta_id) REFERENCES SGAT.actas (id) ON DELETE CASCADE,
    CONSTRAINT fk_actas_materiales_repuesto FOREIGN KEY (repuesto_id) REFERENCES SGAT.repuestos (id),
    CONSTRAINT uq_actas_materiales UNIQUE (acta_id, repuesto_id),
    CONSTRAINT ck_actas_materiales_cantidad CHECK (cantidad > 0)
);
GO
EXEC SGAT.sp_comentar N'actas_materiales', N'Materiales de proveeduría entregados en un acta';
GO
