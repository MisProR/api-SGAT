-- =============================================================================
--  SGAT - Script 08 - Vistas para consultas, reportes y paneles
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
--  Activos con todos sus datos descriptivos (base para listados e informes)
-- -----------------------------------------------------------------------------
CREATE OR ALTER VIEW SGAT.v_activos AS
SELECT
    a.id,
    a.codigo,
    a.categoria_id,
    c.nombre                                   AS categoria,
    c.tipo                                     AS categoria_tipo,
    c.icono                                    AS categoria_icono,
    a.marca_id,
    m.nombre                                   AS marca,
    a.modelo,
    a.numero_serie,
    a.codigo_contable,
    a.descripcion,
    a.especificaciones,
    a.estado,
    a.condicion,
    a.activo_padre_id,
    p.codigo                                   AS activo_padre_codigo,
    a.agencia_id,
    ag.nombre                                  AS agencia,
    a.departamento_id,
    d.nombre                                   AS departamento,
    a.empleado_id,
    CASE WHEN e.id IS NOT NULL THEN e.nombres + N' ' + e.apellidos END AS empleado,
    e.cedula                                   AS empleado_cedula,
    a.proveedor_id,
    pr.razon_social                            AS proveedor,
    a.fecha_compra,
    a.valor_compra,
    a.numero_factura,
    a.garantia_hasta,
    CAST(CASE WHEN a.garantia_hasta IS NOT NULL AND a.garantia_hasta >= CAST(GETDATE() AS DATE)
              THEN 1 ELSE 0 END AS BIT)        AS en_garantia,
    a.hostname,
    a.direccion_ip,
    a.direccion_mac,
    a.observaciones,
    a.fecha_baja,
    a.motivo_baja,
    -- Depreciación lineal según la vida útil de la categoría (meses completos transcurridos, como age())
    CASE
        WHEN a.valor_compra IS NULL OR a.fecha_compra IS NULL THEN NULL
        ELSE CAST(ROUND(CASE WHEN dep.valor < 0 THEN 0 ELSE dep.valor END, 2) AS DECIMAL(14,2))
    END                                        AS valor_actual,
    (SELECT MAX(mt.fecha_fin) FROM SGAT.mantenimientos mt
      WHERE mt.activo_id = a.id AND mt.estado = 'FINALIZADO')                     AS ultimo_mantenimiento,
    (SELECT MIN(mt.fecha_programada) FROM SGAT.mantenimientos mt
      WHERE mt.activo_id = a.id AND mt.estado IN ('PROGRAMADO', 'EN_PROCESO'))    AS proximo_mantenimiento,
    (SELECT COUNT(*) FROM SGAT.activos h WHERE h.activo_padre_id = a.id)          AS total_componentes,
    a.created_at,
    a.updated_at
FROM SGAT.activos a
JOIN SGAT.categorias c         ON c.id = a.categoria_id
JOIN SGAT.agencias ag          ON ag.id = a.agencia_id
LEFT JOIN SGAT.marcas m        ON m.id = a.marca_id
LEFT JOIN SGAT.activos p       ON p.id = a.activo_padre_id
LEFT JOIN SGAT.departamentos d ON d.id = a.departamento_id
LEFT JOIN SGAT.empleados e     ON e.id = a.empleado_id
LEFT JOIN SGAT.proveedores pr  ON pr.id = a.proveedor_id
OUTER APPLY (
    SELECT a.valor_compra * (1 - CAST(
               DATEDIFF(month, a.fecha_compra, CAST(GETDATE() AS DATE))
               - CASE WHEN DAY(CAST(GETDATE() AS DATE)) < DAY(a.fecha_compra) THEN 1 ELSE 0 END
           AS DECIMAL(18,6)) / c.vida_util_meses) AS valor
) dep;
GO
EXEC SGAT.sp_comentar N'v_activos', N'Activos con descripciones, depreciación y datos de mantenimiento', NULL, 'VIEW';
GO

-- -----------------------------------------------------------------------------
--  Mantenimientos con datos del activo y del técnico
-- -----------------------------------------------------------------------------
CREATE OR ALTER VIEW SGAT.v_mantenimientos AS
SELECT
    mt.id,
    mt.codigo,
    mt.activo_id,
    a.codigo                       AS activo_codigo,
    c.nombre                       AS categoria,
    TRIM(COALESCE(mr.nombre, N'') + N' ' + COALESCE(a.modelo, N'')) AS activo_descripcion,
    a.agencia_id,
    ag.nombre                      AS agencia,
    mt.averia_id,
    av.codigo                      AS averia_codigo,
    mt.tecnico_id,
    t.nombres                      AS tecnico,
    t.tipo                         AS tecnico_tipo,
    t.empresa                      AS tecnico_empresa,
    mt.tipo,
    mt.estado,
    mt.fecha_programada,
    mt.fecha_inicio,
    mt.fecha_fin,
    mt.diagnostico,
    mt.trabajo_realizado,
    mt.costo,
    mt.proxima_fecha,
    mt.observaciones,
    CAST(CASE WHEN mt.estado IN ('PROGRAMADO', 'EN_PROCESO') AND mt.fecha_programada < CAST(GETDATE() AS DATE)
              THEN 1 ELSE 0 END AS BIT) AS vencido,
    (SELECT COUNT(*) FROM SGAT.reemplazos r WHERE r.mantenimiento_id = mt.id) AS total_reemplazos,
    mt.created_at,
    mt.updated_at
FROM SGAT.mantenimientos mt
JOIN SGAT.activos a        ON a.id = mt.activo_id
JOIN SGAT.categorias c     ON c.id = a.categoria_id
JOIN SGAT.agencias ag      ON ag.id = a.agencia_id
JOIN SGAT.tecnicos t       ON t.id = mt.tecnico_id
LEFT JOIN SGAT.marcas mr   ON mr.id = a.marca_id
LEFT JOIN SGAT.averias av  ON av.id = mt.averia_id;
GO

-- -----------------------------------------------------------------------------
--  Resumen de activos por agencia y estado (panel / informes)
-- -----------------------------------------------------------------------------
CREATE OR ALTER VIEW SGAT.v_resumen_agencias AS
SELECT
    ag.id                                                                   AS agencia_id,
    ag.codigo,
    ag.nombre                                                               AS agencia,
    ag.tipo,
    COUNT(a.id)                                                             AS total,
    COUNT(CASE WHEN a.estado = 'ACTIVO' THEN a.id END)                      AS activos,
    COUNT(CASE WHEN a.estado = 'EN_MANTENIMIENTO' THEN a.id END)            AS en_mantenimiento,
    COUNT(CASE WHEN a.estado = 'DANADO' THEN a.id END)                      AS danados,
    COUNT(CASE WHEN a.estado = 'DADO_DE_BAJA' THEN a.id END)                AS dados_de_baja,
    COUNT(CASE WHEN a.empleado_id IS NOT NULL THEN a.id END)                AS asignados,
    COALESCE(SUM(CASE WHEN a.estado <> 'DADO_DE_BAJA' THEN a.valor_compra END), 0) AS valor_total,
    (SELECT COUNT(*) FROM SGAT.empleados e WHERE e.agencia_id = ag.id AND e.habilitado = 1) AS empleados
FROM SGAT.agencias ag
LEFT JOIN SGAT.activos a ON a.agencia_id = ag.id
GROUP BY ag.id, ag.codigo, ag.nombre, ag.tipo;
GO

-- -----------------------------------------------------------------------------
--  Activos asignados por empleado (base para actas de entrega)
-- -----------------------------------------------------------------------------
CREATE OR ALTER VIEW SGAT.v_asignaciones AS
SELECT
    s.id,
    s.activo_id,
    a.codigo                     AS activo_codigo,
    c.nombre                     AS categoria,
    m.nombre                     AS marca,
    a.modelo,
    a.numero_serie,
    s.empleado_id,
    e.nombres + N' ' + e.apellidos AS empleado,
    e.cedula                     AS empleado_cedula,
    e.cargo                      AS empleado_cargo,
    s.agencia_id,
    ag.nombre                    AS agencia,
    s.departamento_id,
    d.nombre                     AS departamento,
    s.fecha_asignacion,
    s.fecha_devolucion,
    s.estado,
    s.observaciones,
    s.motivo_devolucion,
    s.usuario_id,
    u.nombres + N' ' + u.apellidos AS usuario,
    s.created_at
FROM SGAT.asignaciones s
JOIN SGAT.activos a        ON a.id = s.activo_id
JOIN SGAT.categorias c     ON c.id = a.categoria_id
JOIN SGAT.empleados e      ON e.id = s.empleado_id
JOIN SGAT.agencias ag      ON ag.id = s.agencia_id
JOIN SGAT.departamentos d  ON d.id = s.departamento_id
LEFT JOIN SGAT.marcas m    ON m.id = a.marca_id
LEFT JOIN SGAT.usuarios u  ON u.id = s.usuario_id;
GO

-- -----------------------------------------------------------------------------
--  Repuestos con alerta de stock bajo
-- -----------------------------------------------------------------------------
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
    r.updated_at
FROM SGAT.repuestos r
LEFT JOIN SGAT.categorias c ON c.id = r.categoria_id
LEFT JOIN SGAT.marcas m     ON m.id = r.marca_id
LEFT JOIN SGAT.agencias ag  ON ag.id = r.agencia_id;
GO
