-- =============================================================================
--  SGAT - Script 04 - Seguridad: roles, usuarios, sesiones y auditoría
--  Se puede ejecutar más de una vez: solo crea lo que todavía no existe.
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
--  Roles de acceso. El superadministrador puede crear, editar y eliminar roles.
--  Cada rol tiene un PERFIL que define qué puede hacer en el sistema:
--    SUPERADMIN : control total (usuarios, roles, configuración, auditoría, eliminación)
--    ADMIN      : equipos de seguridad física (inventario, asignaciones, mantenimientos...)
--    ENCARGADO  : activos de UNA agencia (envía, recibe, devoluciones)
--    JEFE       : bienes y empleados de su departamento en su agencia
--    PROVEEDURIA: bodega de proveeduría y entregas de materiales
--    VERIFICADOR: consulta todo y registra la verificación anual
--  Los 6 roles iniciales (es_sistema = 1) no se pueden eliminar ni cambiar de perfil.
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.roles', N'U') IS NULL
CREATE TABLE SGAT.roles (
    id           SMALLINT IDENTITY(1,1) PRIMARY KEY,
    codigo       NVARCHAR(20)   NOT NULL,
    nombre       NVARCHAR(60)   NOT NULL,
    descripcion  NVARCHAR(250)  NULL,
    perfil       NVARCHAR(20)   NOT NULL,
    es_sistema   BIT            NOT NULL DEFAULT 0,
    habilitado   BIT            NOT NULL DEFAULT 1,
    created_at   DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at   DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_roles_codigo UNIQUE (codigo),
    CONSTRAINT uq_roles_nombre UNIQUE (nombre),
    CONSTRAINT ck_roles_perfil CHECK (perfil IN ('SUPERADMIN', 'ADMIN', 'ENCARGADO', 'JEFE', 'PROVEEDURIA', 'VERIFICADOR')),
    -- Equivale a: codigo ~ '^[A-Z0-9_]+$'
    CONSTRAINT ck_roles_codigo CHECK (codigo COLLATE Latin1_General_BIN NOT LIKE '%[^A-Z0-9_]%')
);
GO
EXEC SGAT.sp_comentar N'roles', N'Roles de acceso al sistema (los crea y administra el superadministrador)';
EXEC SGAT.sp_comentar N'roles', N'Perfil de permisos que aplica el sistema a los usuarios del rol', N'perfil';
EXEC SGAT.sp_comentar N'roles', N'Rol inicial del sistema: no se puede eliminar ni cambiar de perfil', N'es_sistema';
EXEC SGAT.sp_crear_trigger_updated_at N'roles';
GO

-- -----------------------------------------------------------------------------
--  Usuarios del sistema
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.usuarios', N'U') IS NULL
CREATE TABLE SGAT.usuarios (
    id                     BIGINT IDENTITY(1,1) PRIMARY KEY,
    rol_id                 SMALLINT       NOT NULL,
    username               NVARCHAR(50)   NOT NULL,
    email                  NVARCHAR(150)  NOT NULL,
    nombres                NVARCHAR(100)  NOT NULL,
    apellidos              NVARCHAR(100)  NOT NULL,
    password_hash          NVARCHAR(100)  NOT NULL,
    agencia_id             BIGINT         NULL,
    habilitado             BIT            NOT NULL DEFAULT 1,
    debe_cambiar_password  BIT            NOT NULL DEFAULT 0,
    intentos_fallidos      SMALLINT       NOT NULL DEFAULT 0,
    bloqueado_hasta        DATETIMEOFFSET NULL,
    ultimo_acceso          DATETIMEOFFSET NULL,
    created_at             DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    updated_at             DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT fk_usuarios_rol FOREIGN KEY (rol_id) REFERENCES SGAT.roles (id),
    CONSTRAINT fk_usuarios_agencia FOREIGN KEY (agencia_id) REFERENCES SGAT.agencias (id),
    -- Equivale a: username ~ '^[a-zA-Z0-9._-]{3,50}$' (intercalación binaria: sin letras con tilde)
    CONSTRAINT ck_usuarios_username CHECK (LEN(username) >= 3
        AND username COLLATE Latin1_General_BIN NOT LIKE '%[^a-zA-Z0-9._-]%')
);
GO
EXEC SGAT.sp_comentar N'usuarios', N'Usuarios con acceso al sistema';
EXEC SGAT.sp_comentar N'usuarios', N'Hash bcrypt de la contraseña', N'password_hash';
GO

-- La intercalación de la base no distingue mayúsculas: equivale a UNIQUE (lower(username))
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'uq_usuarios_username' AND object_id = OBJECT_ID(N'SGAT.usuarios'))
    CREATE UNIQUE INDEX uq_usuarios_username ON SGAT.usuarios (username);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'uq_usuarios_email' AND object_id = OBJECT_ID(N'SGAT.usuarios'))
    CREATE UNIQUE INDEX uq_usuarios_email ON SGAT.usuarios (email);
GO
EXEC SGAT.sp_crear_trigger_updated_at N'usuarios';
GO

-- -----------------------------------------------------------------------------
--  Refresh tokens (sesiones). Se guarda solo el hash SHA-256 del token.
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.sesiones', N'U') IS NULL
CREATE TABLE SGAT.sesiones (
    id            BIGINT IDENTITY(1,1) PRIMARY KEY,
    usuario_id    BIGINT         NOT NULL,
    token_hash    NCHAR(64)      NOT NULL,
    expira_en     DATETIMEOFFSET NOT NULL,
    revocada_en   DATETIMEOFFSET NULL,
    ip            NVARCHAR(45)   NULL,
    user_agent    NVARCHAR(300)  NULL,
    created_at    DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT uq_sesiones_token UNIQUE (token_hash),
    CONSTRAINT fk_sesiones_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id) ON DELETE CASCADE
);
GO
EXEC SGAT.sp_comentar N'sesiones', N'Sesiones activas (refresh tokens hasheados)';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_sesiones_usuario' AND object_id = OBJECT_ID(N'SGAT.sesiones'))
    CREATE INDEX ix_sesiones_usuario ON SGAT.sesiones (usuario_id) WHERE revocada_en IS NULL;
GO

-- -----------------------------------------------------------------------------
--  Auditoría: registro de todas las acciones relevantes del sistema
--  (JSONB -> NVARCHAR(MAX) validado con ISJSON)
-- -----------------------------------------------------------------------------
IF OBJECT_ID(N'SGAT.auditoria', N'U') IS NULL
CREATE TABLE SGAT.auditoria (
    id                BIGINT IDENTITY(1,1) PRIMARY KEY,
    usuario_id        BIGINT         NULL,
    usuario_nombre    NVARCHAR(120)  NULL,
    accion            NVARCHAR(30)   NOT NULL,
    entidad           NVARCHAR(60)   NOT NULL,
    entidad_id        BIGINT         NULL,
    descripcion       NVARCHAR(500)  NULL,
    datos_anteriores  NVARCHAR(MAX)  NULL,
    datos_nuevos      NVARCHAR(MAX)  NULL,
    ip                NVARCHAR(45)   NULL,
    user_agent        NVARCHAR(300)  NULL,
    created_at        DATETIMEOFFSET NOT NULL DEFAULT SYSDATETIMEOFFSET(),
    CONSTRAINT fk_auditoria_usuario FOREIGN KEY (usuario_id) REFERENCES SGAT.usuarios (id) ON DELETE SET NULL,
    CONSTRAINT ck_auditoria_accion CHECK (accion IN (
        'CREAR', 'ACTUALIZAR', 'ELIMINAR', 'LOGIN', 'LOGIN_FALLIDO', 'LOGOUT',
        'CAMBIO_PASSWORD', 'ASIGNAR', 'DEVOLVER', 'TRASLADAR', 'DAR_BAJA', 'EXPORTAR')),
    CONSTRAINT ck_auditoria_datos_anteriores CHECK (datos_anteriores IS NULL OR ISJSON(datos_anteriores) = 1),
    CONSTRAINT ck_auditoria_datos_nuevos CHECK (datos_nuevos IS NULL OR ISJSON(datos_nuevos) = 1)
);
GO
EXEC SGAT.sp_comentar N'auditoria', N'Bitácora de acciones de los usuarios';
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_auditoria_fecha' AND object_id = OBJECT_ID(N'SGAT.auditoria'))
    CREATE INDEX ix_auditoria_fecha ON SGAT.auditoria (created_at DESC);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_auditoria_usuario' AND object_id = OBJECT_ID(N'SGAT.auditoria'))
    CREATE INDEX ix_auditoria_usuario ON SGAT.auditoria (usuario_id, created_at DESC);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'ix_auditoria_entidad' AND object_id = OBJECT_ID(N'SGAT.auditoria'))
    CREATE INDEX ix_auditoria_entidad ON SGAT.auditoria (entidad, entidad_id);
GO
