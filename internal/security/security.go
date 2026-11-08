// Package security contiene la emisión/validación de JWT, el hash de contraseñas
// y el usuario autenticado que viaja en el contexto de cada petición.
package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	RoleSuperAdmin = "SUPERADMIN"
	RoleAdmin      = "ADMIN"
	// RoleEncargado opera solo los activos de su agencia: envía, recibe y registra devoluciones.
	RoleEncargado = "ENCARGADO"
	// RoleProveeduria administra la bodega de proveeduría (materiales) y sus entregas a las agencias.
	RoleProveeduria = "PROVEEDURIA"
	// RoleVerificador consulta todo y registra la verificación física anual; no modifica activos.
	RoleVerificador = "VERIFICADOR"
	// RoleJefe responde por los bienes y empleados de su departamento en su agencia (un encargado de su área).
	RoleJefe = "JEFE"
)

// Principal es el usuario autenticado de la petición.
type Principal struct {
	ID             int64
	Username       string
	Nombre         string
	Rol            string
	AgenciaID      *int64
	DepartamentoID *int64
}

func (p Principal) IsSuperAdmin() bool { return p.Rol == RoleSuperAdmin }

func (p Principal) IsEncargado() bool { return p.Rol == RoleEncargado }

// IsLocal: roles limitados a una agencia (el encargado) o a un departamento de una agencia (el jefe).
func (p Principal) IsLocal() bool { return p.Rol == RoleEncargado || p.Rol == RoleJefe }

type Claims struct {
	Username       string `json:"usr"`
	Nombre         string `json:"name"`
	Rol            string `json:"rol"`
	AgenciaID      *int64 `json:"agc,omitempty"`
	DepartamentoID *int64 `json:"dep,omitempty"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

func NewTokenManager(secret, issuer string, ttl time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), issuer: issuer, ttl: ttl}
}

func (m *TokenManager) TTL() time.Duration { return m.ttl }

// Generate emite un access token firmado HS256.
func (m *TokenManager) Generate(p Principal) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(m.ttl)
	claims := Claims{
		Username:  p.Username,
		Nombre:    p.Nombre,
		Rol:            p.Rol,
		AgenciaID:      p.AgenciaID,
		DepartamentoID: p.DepartamentoID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   strconv.FormatInt(p.ID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	return token, exp, err
}

// Parse valida firma, emisor y vigencia del token.
func (m *TokenManager) Parse(raw string) (Principal, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(m.issuer), jwt.WithExpirationRequired())
	if err != nil {
		return Principal{}, err
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return Principal{}, errors.New("subject inválido")
	}
	if claims.Rol == RoleEncargado && claims.AgenciaID == nil {
		return Principal{}, errors.New("encargado sin agencia")
	}
	if claims.Rol == RoleJefe && (claims.AgenciaID == nil || claims.DepartamentoID == nil) {
		return Principal{}, errors.New("jefe sin agencia o departamento")
	}
	return Principal{ID: id, Username: claims.Username, Nombre: claims.Nombre, Rol: claims.Rol, AgenciaID: claims.AgenciaID,
		DepartamentoID: claims.DepartamentoID}, nil
}

// ---- Contraseñas ----

func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
	if err != nil {
		return "", fmt.Errorf("hash de contraseña: %w", err)
	}
	return string(h), nil
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// dummyHash se usa para igualar el tiempo de respuesta cuando el usuario no existe.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), 12)

func CheckDummy(plain string) { _ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plain)) }

// ---- Refresh tokens ----

// NewRefreshToken devuelve el token opaco (para la cookie) y su hash SHA-256 (para la BD).
func NewRefreshToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ---- Contexto ----

type ctxKey int

const (
	principalKey ctxKey = iota
	clientKey
)

// Client contiene datos del cliente HTTP para auditoría.
type Client struct {
	IP        string
	UserAgent string
}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// AgenciaScope devuelve la agencia a la que se limita el usuario (ENCARGADO y JEFE); nil = sin restricción.
func AgenciaScope(ctx context.Context) *int64 {
	if p, ok := PrincipalFrom(ctx); ok && p.IsLocal() {
		return p.AgenciaID
	}
	return nil
}

// DepartamentoScope devuelve el departamento al que se limita el JEFE (dentro de su agencia); nil = sin restricción.
func DepartamentoScope(ctx context.Context) *int64 {
	if p, ok := PrincipalFrom(ctx); ok && p.Rol == RoleJefe {
		return p.DepartamentoID
	}
	return nil
}

// Áreas de inventario: a cada categoría la administra un área.
const (
	// AreaSeguridadFisica: equipos (computadoras, impresoras, UPS, redes, contadoras de billetes...). La gestiona el ADMIN.
	AreaSeguridadFisica = "SEGURIDAD_FISICA"
	// AreaProveeduria: materiales y periféricos menores (audífonos, teclados, mouse, tintas...).
	AreaProveeduria = "PROVEEDURIA"
)

// AreaScope devuelve el área de inventario a la que se limita el usuario (el ADMIN es de seguridad física y
// el encargado de proveeduría, de proveeduría); nil = todas las áreas (SUPERADMIN y ENCARGADO, que se limita
// por agencia).
func AreaScope(ctx context.Context) *string {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return nil
	}
	var area string
	switch p.Rol {
	case RoleAdmin:
		area = AreaSeguridadFisica
	case RoleProveeduria:
		area = AreaProveeduria
	default:
		return nil
	}
	return &area
}

// UserID devuelve el id del usuario autenticado o nil (para columnas usuario_id).
func UserID(ctx context.Context) *int64 {
	if p, ok := PrincipalFrom(ctx); ok {
		return &p.ID
	}
	return nil
}

func WithClient(ctx context.Context, c Client) context.Context {
	return context.WithValue(ctx, clientKey, c)
}

func ClientFrom(ctx context.Context) Client {
	c, _ := ctx.Value(clientKey).(Client)
	return c
}
