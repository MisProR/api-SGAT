-- =============================================================================
--  SGAT - Script 17 - Datos iniciales OBLIGATORIOS
--  Roles, agencias, usuarios, categorías, departamentos, marcas y configuración.
--  Ejecutar después de crear toda la estructura (scripts 01 al 16).
--  Se puede ejecutar más de una vez: solo inserta lo que todavía no existe.
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
--  Roles del sistema (todos son necesarios: la API usa estos id)
-- -----------------------------------------------------------------------------
SET IDENTITY_INSERT SGAT.roles ON;

INSERT INTO SGAT.roles (id, codigo, nombre, descripcion, perfil, es_sistema)
SELECT v.id, v.codigo, v.nombre, v.descripcion, v.codigo, 1
FROM (VALUES
    (1, N'SUPERADMIN',  N'Superadministrador',               N'Acceso total: usuarios, configuración, auditoría y eliminación de registros'),
    (2, N'ADMIN',       N'Administrador de seguridad física', N'Gestiona los equipos de seguridad física: computadoras, impresoras, UPS, redes y contadoras de billetes'),
    (3, N'ENCARGADO',   N'Encargado de agencia',             N'Envía y recibe activos de su agencia, registra devoluciones y firma las actas de recepción'),
    (4, N'PROVEEDURIA', N'Encargado de proveeduría',         N'Administra la bodega de proveeduría (materiales, periféricos menores e insumos) y sus entregas a las agencias'),
    (5, N'VERIFICADOR', N'Verificador de inventario',        N'Consulta todas las áreas y agencias y registra la verificación física anual de los activos'),
    (6, N'JEFE',        N'Jefe de departamento',             N'Recibe y entrega con acta los bienes de su departamento y responde por los de sus empleados')
) AS v(id, codigo, nombre, descripcion)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.roles r WHERE r.id = v.id);

SET IDENTITY_INSERT SGAT.roles OFF;
GO

-- -----------------------------------------------------------------------------
--  Agencias (2): matriz y bodega central
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.agencias (codigo, nombre, tipo, provincia, ciudad, direccion)
SELECT v.codigo, v.nombre, v.tipo, v.provincia, v.ciudad, v.direccion
FROM (VALUES
    (N'MAT', N'Oficina Matriz',    N'MATRIZ', N'Pichincha', N'Quito', N'Dirección de la matriz'),
    (N'BOD', N'Bodega Central TI', N'BODEGA', N'Pichincha', N'Quito', N'Bodega de tecnología')
) AS v(codigo, nombre, tipo, provincia, ciudad, direccion)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.agencias a WHERE a.codigo = v.codigo);
GO

-- -----------------------------------------------------------------------------
--  Departamentos (2)
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.departamentos (codigo, nombre, descripcion)
SELECT v.codigo, v.nombre, v.descripcion
FROM (VALUES
    (N'TIC', N'Tecnología', N'Tecnologías de la información y comunicación'),
    (N'CRE', N'Créditos',   N'Análisis y otorgamiento de créditos')
) AS v(codigo, nombre, descripcion)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.departamentos d WHERE d.codigo = v.codigo);
GO

-- -----------------------------------------------------------------------------
--  Usuarios
--    superadmin / Admin2026*  (el sistema obliga a cambiarla en el primer ingreso)
--    admin      / Admin2026*
--  Hash bcrypt (costo 10) ya calculado de "Admin2026*", compatible con la API.
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.usuarios (rol_id, username, email, nombres, apellidos, password_hash, debe_cambiar_password)
SELECT 1, N'superadmin', N'superadmin@cooperativa.local', N'Super', N'Administrador',
       N'$2a$10$tA4b6LVuCN65XtQgfi1.PekRQOVT6lzQRpL8mmppofYQ4gStpII5u', 1
WHERE NOT EXISTS (SELECT 1 FROM SGAT.usuarios WHERE username = N'superadmin');

INSERT INTO SGAT.usuarios (rol_id, username, email, nombres, apellidos, password_hash, agencia_id)
SELECT 2, N'admin', N'admin@cooperativa.local', N'Administrador', N'Operativo',
       N'$2a$10$tA4b6LVuCN65XtQgfi1.PekRQOVT6lzQRpL8mmppofYQ4gStpII5u',
       (SELECT id FROM SGAT.agencias WHERE codigo = N'MAT')
WHERE NOT EXISTS (SELECT 1 FROM SGAT.usuarios WHERE username = N'admin');
GO

-- -----------------------------------------------------------------------------
--  Categorías (2): una por área (icono = nombre del ícono Lucide)
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.categorias (codigo, nombre, tipo, prefijo, icono, vida_util_meses, meses_mantenimiento, requiere_serie, area)
SELECT v.codigo, v.nombre, v.tipo, v.prefijo, v.icono, v.vida, v.mant, v.serie, v.area
FROM (VALUES
    (N'PC',         N'Computadora de escritorio', N'EQUIPO',     N'PC',  N'monitor',   60,  6, 1, N'SEGURIDAD_FISICA'),
    (N'ESCRITORIO', N'Escritorio',                N'MOBILIARIO', N'ESC', N'lamp-desk', 120, 0, 0, N'PROVEEDURIA')
) AS v(codigo, nombre, tipo, prefijo, icono, vida, mant, serie, area)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.categorias c WHERE c.codigo = v.codigo);
GO

-- -----------------------------------------------------------------------------
--  Marcas (2)
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.marcas (nombre)
SELECT v.nombre FROM (VALUES (N'Dell'), (N'HP')) AS v(nombre)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.marcas m WHERE m.nombre = v.nombre);
GO

-- -----------------------------------------------------------------------------
--  Configuración general (parámetros que lee el sistema: se dejan todos)
-- -----------------------------------------------------------------------------
INSERT INTO SGAT.configuracion (clave, valor, descripcion, grupo)
SELECT v.clave, v.valor, v.descripcion, v.grupo
FROM (VALUES
    (N'institucion.nombre',         N'Cooperativa de Ahorro y Crédito', N'Nombre de la institución', N'INSTITUCION'),
    (N'institucion.ruc',            N'0000000000001',                   N'RUC de la institución', N'INSTITUCION'),
    (N'institucion.direccion',      N'Dirección matriz',                N'Dirección de la matriz', N'INSTITUCION'),
    (N'institucion.telefono',       N'(02) 000-0000',                   N'Teléfono principal', N'INSTITUCION'),
    (N'institucion.ciudad',         N'Quito',                           N'Ciudad que aparece en las actas', N'INSTITUCION'),
    (N'actas.responsable_ti',       N'Jefe de Tecnología',              N'Cargo que firma la entrega en las actas', N'ACTAS'),
    (N'actas.pie',                  N'El custodio se compromete a hacer buen uso de los bienes entregados y a reportar cualquier novedad al departamento de Tecnología.', N'Texto legal al pie de las actas', N'ACTAS'),
    (N'alertas.dias_garantia',      N'30',                              N'Días de anticipación para alertar vencimiento de garantías', N'ALERTAS'),
    (N'alertas.dias_mantenimiento', N'7',                               N'Días de anticipación para alertar mantenimientos próximos', N'ALERTAS')
) AS v(clave, valor, descripcion, grupo)
WHERE NOT EXISTS (SELECT 1 FROM SGAT.configuracion c WHERE c.clave = v.clave);
GO
