-- =============================================================================
--  SGAT - Ejecución completa en SQL Server (OPCIONAL)
--
--  Si prefiere hacerlo a mano, abra y ejecute (F5) uno por uno en este orden:
--     01_crear_base_datos.sql          (conectado como sa / sysadmin)
--     02_esquema_y_utilidades.sql
--     03_organizacion.sql
--     04_seguridad.sql
--     05_catalogos_y_activos.sql
--     06_operaciones.sql
--     07_bodega_actas_configuracion.sql
--     08_vistas_reportes.sql
--     09_encargados_y_recepcion.sql
--     10_salida_empleados.sql
--     11_areas_inventario.sql
--     12_proveeduria.sql
--     13_verificacion_anual.sql
--     14_reporte_danos.sql
--     15_mobiliario_y_ajustes.sql
--     16_jefe_departamento.sql
--     17_datos_iniciales.sql           (obligatorio: roles, usuarios, catálogos, configuración)
--     18_datos_demo.sql                (opcional: 2 registros de prueba por tabla)
--     19_permisos.sql                  (siempre al final)
--
--  Para ejecutarlos todos de una vez con este archivo se requiere modo SQLCMD:
--    * SSMS: menú Consulta > Modo SQLCMD, abrir este archivo y ejecutar (F5).
--    * Línea de comandos:
--        sqlcmd -S localhost -E -i "ruta\database\db\00_ejecutar_todo.sql"
--      Para una instancia con nombre use -S localhost\SQLEXPRESS
--
--  Ajuste la variable RUTA si mueve la carpeta.
--  Para no cargar datos de demostración, comente la línea del script 18.
-- =============================================================================
:setvar RUTA "D:\PROYECTO_EMPRESAS\IT-Asset-Manager\proyecto-db-sqlserver\database\db"
:on error exit

PRINT '>> 01 Base de datos y login de aplicación';
:r "$(RUTA)\01_crear_base_datos.sql"
PRINT '>> 02 Esquema y utilidades';
:r "$(RUTA)\02_esquema_y_utilidades.sql"
PRINT '>> 03 Organización';
:r "$(RUTA)\03_organizacion.sql"
PRINT '>> 04 Seguridad';
:r "$(RUTA)\04_seguridad.sql"
PRINT '>> 05 Catálogos y activos';
:r "$(RUTA)\05_catalogos_y_activos.sql"
PRINT '>> 06 Operaciones';
:r "$(RUTA)\06_operaciones.sql"
PRINT '>> 07 Bodega, actas y configuración';
:r "$(RUTA)\07_bodega_actas_configuracion.sql"
PRINT '>> 08 Vistas';
:r "$(RUTA)\08_vistas_reportes.sql"
PRINT '>> 09 Encargados, envíos con recepción y firma de actas';
:r "$(RUTA)\09_encargados_y_recepcion.sql"
PRINT '>> 10 Salida de empleados';
:r "$(RUTA)\10_salida_empleados.sql"
PRINT '>> 11 Áreas de inventario';
:r "$(RUTA)\11_areas_inventario.sql"
PRINT '>> 12 Bodega de proveeduría y entregas de materiales';
:r "$(RUTA)\12_proveeduria.sql"
PRINT '>> 13 Verificación anual del inventario';
:r "$(RUTA)\13_verificacion_anual.sql"
PRINT '>> 14 Reporte de daños desde las agencias';
:r "$(RUTA)\14_reporte_danos.sql"
PRINT '>> 15 Mobiliario, periféricos y bajas de materiales';
:r "$(RUTA)\15_mobiliario_y_ajustes.sql"
PRINT '>> 16 Rol Jefe de departamento';
:r "$(RUTA)\16_jefe_departamento.sql"
PRINT '>> 17 Datos iniciales';
:r "$(RUTA)\17_datos_iniciales.sql"
PRINT '>> 18 Datos demo (opcional)';
:r "$(RUTA)\18_datos_demo.sql"
PRINT '>> 19 Permisos';
:r "$(RUTA)\19_permisos.sql"
PRINT '>> Base de datos SGAT lista';
GO
