-- =============================================================================
--  SGAT - Script 18 - Datos de DEMOSTRACIÓN (OPCIONAL)
--  Solo 2 registros por tabla para probar el sistema. NO lo ejecute en producción.
--  Requiere el script 17 (datos iniciales).
-- =============================================================================
SET ANSI_NULLS ON;
SET QUOTED_IDENTIFIER ON;
SET ANSI_PADDING ON;
SET ANSI_WARNINGS ON;
SET ARITHABORT ON;
SET CONCAT_NULL_YIELDS_NULL ON;
SET NUMERIC_ROUNDABORT OFF;
SET NOCOUNT ON;
GO
USE sgat;
GO

-- -----------------------------------------------------------------------------
--  Proveedores (2)
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.proveedores (ruc, razon_social, contacto, telefono, email)
SELECT v.ruc, v.razon_social, v.contacto, v.telefono, v.email
FROM (VALUES
    (N'1790000000001', N'Tecnología Integral S.A.',  N'Carlos Mena',   N'0991111111', N'ventas@tecintegral.ec'),
    (N'0990000000001', N'Computadoras del Pacífico', N'María Andrade', N'0992222222', N'info@compacifico.ec')
) AS v(ruc, razon_social, contacto, telefono, email)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.proveedores p WHERE p.ruc = v.ruc);
GO

-- -----------------------------------------------------------------------------
--  Empleados (2)
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.empleados (cedula, nombres, apellidos, email, cargo, agencia_id, departamento_id, fecha_ingreso)
SELECT v.cedula, v.nombres, v.apellidos, v.email, v.cargo,
       (SELECT id FROM SGAT.agencias WHERE codigo = N'MAT'),
       (SELECT id FROM SGAT.departamentos WHERE codigo = v.dep),
       v.ingreso
FROM (VALUES
    (N'1700000001', N'Juan',  N'Pérez', N'juan.perez@cooperativa.local', N'Analista de Sistemas', N'TIC', CAST('2023-03-01' AS DATE)),
    (N'1700000002', N'María', N'Gómez', N'maria.gomez@cooperativa.local', N'Asesor de Crédito',   N'CRE', CAST('2024-06-15' AS DATE))
) AS v(cedula, nombres, apellidos, email, cargo, dep, ingreso)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.empleados e WHERE e.cedula = v.cedula);
GO

-- -----------------------------------------------------------------------------
--  Técnicos (2): uno interno y uno externo
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.tecnicos (nombres, tipo, empresa, especialidad, telefono)
SELECT v.nombres, v.tipo, v.empresa, v.especialidad, v.telefono
FROM (VALUES
    (N'Ing. Roberto Salinas', N'INTERNO', CAST(NULL AS NVARCHAR(150)), N'Hardware y redes', N'0994444444'),
    (N'Marco Villacís',       N'EXTERNO', N'Tecnología Integral S.A.', N'Impresoras',       N'0996666666')
) AS v(nombres, tipo, empresa, especialidad, telefono)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.tecnicos t WHERE t.nombres = v.nombres);
GO

-- -----------------------------------------------------------------------------
--  Activos (2), asignaciones (2), historial (2) y mantenimientos (2)
-- -----------------------------------------------------------------------------
SET XACT_ABORT ON;
IF EXISTS (SELECT 1 FROM SGAT.activos)
    PRINT N'Ya existen activos, se omiten los datos demo de inventario';
ELSE
BEGIN
    BEGIN TRANSACTION;

    DECLARE @usuario BIGINT = (SELECT id FROM SGAT.usuarios WHERE username = N'superadmin'),
            @ag      BIGINT = (SELECT id FROM SGAT.agencias WHERE codigo = N'MAT'),
            @prov    BIGINT = (SELECT MIN(id) FROM SGAT.proveedores),
            @emp1    BIGINT = (SELECT id FROM SGAT.empleados WHERE cedula = N'1700000001'),
            @emp2    BIGINT = (SELECT id FROM SGAT.empleados WHERE cedula = N'1700000002'),
            @dep1    BIGINT = (SELECT id FROM SGAT.departamentos WHERE codigo = N'TIC'),
            @dep2    BIGINT = (SELECT id FROM SGAT.departamentos WHERE codigo = N'CRE'),
            @cat_pc  BIGINT = (SELECT id FROM SGAT.categorias WHERE codigo = N'PC'),
            @cat_esc BIGINT = (SELECT id FROM SGAT.categorias WHERE codigo = N'ESCRITORIO'),
            @dell    BIGINT = (SELECT id FROM SGAT.marcas WHERE nombre = N'Dell'),
            @tec1    BIGINT = (SELECT MIN(id) FROM SGAT.tecnicos),
            @hoy     DATE   = CAST(GETDATE() AS DATE),
            @codigo  NVARCHAR(25), @pc BIGINT, @esc BIGINT;

    -- Activo 1: computadora asignada a Juan Pérez (Tecnología)
    EXEC SGAT.sp_generar_codigo_activo @cat_pc, @codigo OUTPUT;
    INSERT INTO SGAT.activos (codigo, categoria_id, marca_id, modelo, numero_serie, especificaciones, estado, condicion,
                                               agencia_id, departamento_id, empleado_id, proveedor_id, fecha_compra, valor_compra,
                                               garantia_hasta, hostname, created_by)
    VALUES (@codigo, @cat_pc, @dell, N'OptiPlex 7010', N'SN000001',
            N'{"procesador":"Intel Core i5-12500","ram_gb":16,"almacenamiento":"SSD 512 GB","sistema_operativo":"Windows 11 Pro"}',
            N'ACTIVO', N'BUENO', @ag, @dep1, @emp1, @prov, DATEADD(day, -200, @hoy), 950.00,
            DATEADD(day, 895, @hoy), N'COOP-0001', @usuario);
    SET @pc = CAST(SCOPE_IDENTITY() AS BIGINT);

    -- Activo 2: escritorio asignado a María Gómez (Créditos)
    EXEC SGAT.sp_generar_codigo_activo @cat_esc, @codigo OUTPUT;
    INSERT INTO SGAT.activos (codigo, categoria_id, modelo, descripcion, estado, condicion,
                                               agencia_id, departamento_id, empleado_id, fecha_compra, valor_compra, created_by)
    VALUES (@codigo, @cat_esc, N'Escritorio en L', N'Escritorio de melamina 1.40 m', N'ACTIVO', N'BUENO',
            @ag, @dep2, @emp2, DATEADD(day, -100, @hoy), 180.00, @usuario);
    SET @esc = CAST(SCOPE_IDENTITY() AS BIGINT);

    INSERT INTO SGAT.asignaciones (activo_id, empleado_id, agencia_id, departamento_id, fecha_asignacion, usuario_id)
    VALUES (@pc,  @emp1, @ag, @dep1, DATEADD(day, -197, @hoy), @usuario),
           (@esc, @emp2, @ag, @dep2, DATEADD(day, -97,  @hoy), @usuario);

    INSERT INTO SGAT.historial_activos (activo_id, tipo_evento, descripcion, usuario_id)
    VALUES (@pc,  N'ASIGNACION', N'Asignado a Juan Pérez',  @usuario),
           (@esc, N'ASIGNACION', N'Asignado a María Gómez', @usuario);

    -- Mantenimientos de la computadora: uno finalizado y uno programado
    EXEC SGAT.sp_generar_codigo_anual N'MNT', @codigo OUTPUT;
    INSERT INTO SGAT.mantenimientos (codigo, activo_id, tecnico_id, tipo, estado, fecha_programada, fecha_inicio, fecha_fin,
                                                      diagnostico, trabajo_realizado, costo, proxima_fecha, usuario_id)
    VALUES (@codigo, @pc, @tec1, N'PREVENTIVO', N'FINALIZADO', DATEADD(day, -30, @hoy), DATEADD(day, -30, @hoy), DATEADD(day, -30, @hoy),
            N'Revisión general del equipo', N'Limpieza interna y actualización de controladores', 15.00, DATEADD(day, 150, @hoy), @usuario);

    EXEC SGAT.sp_generar_codigo_anual N'MNT', @codigo OUTPUT;
    INSERT INTO SGAT.mantenimientos (codigo, activo_id, tecnico_id, tipo, estado, fecha_programada, usuario_id)
    VALUES (@codigo, @pc, @tec1, N'PREVENTIVO', N'PROGRAMADO', DATEADD(day, 150, @hoy), @usuario);

    COMMIT TRANSACTION;
END
GO

-- -----------------------------------------------------------------------------
--  Bodega (2): un repuesto de seguridad física y un material de proveeduría
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.repuestos (codigo, nombre, marca_id, unidad, stock_minimo, costo_unitario, agencia_id, ubicacion, area)
SELECT v.codigo, v.nombre, (SELECT id FROM SGAT.marcas WHERE nombre = v.marca), v.unidad, v.minimo, v.costo,
       (SELECT id FROM SGAT.agencias WHERE codigo = N'BOD'), N'Estante A', v.area
FROM (VALUES
    (N'REP-RAM-8', N'Memoria RAM DDR4 8GB', N'HP', N'UNIDAD', 3, 28.00, N'SEGURIDAD_FISICA'),
    (N'MAT-PAPEL', N'Resma de papel A4',    NULL,  N'RESMA',  10, 4.50, N'PROVEEDURIA')
) AS v(codigo, nombre, marca, unidad, minimo, costo, area)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.repuestos r WHERE r.codigo = v.codigo);
GO

-- Entrada inicial de stock (el trigger trg_movimientos_stock calcula el stock)
INSERT INTO SGAT.movimientos_bodega (repuesto_id, tipo, cantidad, costo_unitario, motivo, documento, usuario_id)
SELECT r.id, N'ENTRADA', CASE r.codigo WHEN N'REP-RAM-8' THEN 6 ELSE 20 END, r.costo_unitario, N'Inventario inicial', N'INV-INICIAL',
       (SELECT id FROM SGAT.usuarios WHERE username = N'superadmin')
FROM SGAT.repuestos r
WHERE r.codigo IN (N'REP-RAM-8', N'MAT-PAPEL')
  AND NOT EXISTS (SELECT 1 FROM SGAT.movimientos_bodega m WHERE m.repuesto_id = r.id);
GO
