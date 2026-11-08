// Package activos gestiona el inventario de activos tecnológicos: registro,
// edición, cambio de estado, baja, consulta de detalle e historial y exportación.
package activos

import (
	"encoding/json"
	"time"

	"sgat-api/internal/types"
)

// Activo corresponde a la vista v_activos.
type Activo struct {
	ID                   int64           `json:"id" db:"id"`
	Codigo               string          `json:"codigo" db:"codigo"`
	CategoriaID          int64           `json:"categoriaId" db:"categoria_id"`
	Categoria            string          `json:"categoria" db:"categoria"`
	CategoriaTipo        string          `json:"categoriaTipo" db:"categoria_tipo"`
	CategoriaIcono       *string         `json:"categoriaIcono" db:"categoria_icono"`
	MarcaID              *int64          `json:"marcaId" db:"marca_id"`
	Marca                *string         `json:"marca" db:"marca"`
	Modelo               *string         `json:"modelo" db:"modelo"`
	NumeroSerie          *string         `json:"numeroSerie" db:"numero_serie"`
	CodigoContable       *string         `json:"codigoContable" db:"codigo_contable"`
	Descripcion          *string         `json:"descripcion" db:"descripcion"`
	Especificaciones     json.RawMessage `json:"especificaciones" db:"especificaciones"`
	Estado               string          `json:"estado" db:"estado"`
	Condicion            string          `json:"condicion" db:"condicion"`
	ActivoPadreID        *int64          `json:"activoPadreId" db:"activo_padre_id"`
	ActivoPadreCodigo    *string         `json:"activoPadreCodigo" db:"activo_padre_codigo"`
	AgenciaID            int64           `json:"agenciaId" db:"agencia_id"`
	Agencia              string          `json:"agencia" db:"agencia"`
	DepartamentoID       *int64          `json:"departamentoId" db:"departamento_id"`
	Departamento         *string         `json:"departamento" db:"departamento"`
	EmpleadoID           *int64          `json:"empleadoId" db:"empleado_id"`
	Empleado             *string         `json:"empleado" db:"empleado"`
	EmpleadoCedula       *string         `json:"empleadoCedula" db:"empleado_cedula"`
	ProveedorID          *int64          `json:"proveedorId" db:"proveedor_id"`
	Proveedor            *string         `json:"proveedor" db:"proveedor"`
	FechaCompra          *types.Date     `json:"fechaCompra" db:"fecha_compra"`
	ValorCompra          *float64        `json:"valorCompra" db:"valor_compra"`
	NumeroFactura        *string         `json:"numeroFactura" db:"numero_factura"`
	GarantiaHasta        *types.Date     `json:"garantiaHasta" db:"garantia_hasta"`
	EnGarantia           bool            `json:"enGarantia" db:"en_garantia"`
	Hostname             *string         `json:"hostname" db:"hostname"`
	DireccionIP          *string         `json:"direccionIp" db:"direccion_ip"`
	DireccionMAC         *string         `json:"direccionMac" db:"direccion_mac"`
	Observaciones        *string         `json:"observaciones" db:"observaciones"`
	FechaBaja            *types.Date     `json:"fechaBaja" db:"fecha_baja"`
	MotivoBaja           *string         `json:"motivoBaja" db:"motivo_baja"`
	ValorActual          *float64        `json:"valorActual" db:"valor_actual"`
	UltimoMantenimiento  *types.Date     `json:"ultimoMantenimiento" db:"ultimo_mantenimiento"`
	ProximoMantenimiento *types.Date     `json:"proximoMantenimiento" db:"proximo_mantenimiento"`
	TotalComponentes     int64           `json:"totalComponentes" db:"total_componentes"`
	CreatedAt            time.Time       `json:"createdAt" db:"created_at"`
	UpdatedAt            time.Time       `json:"updatedAt" db:"updated_at"`
	// Código del envío pendiente de recepción en el que viaja el activo (nil = no está en tránsito)
	EnvioPendiente *string `json:"envioPendiente" db:"envio_pendiente"`
	// Quién lo tuvo por última vez y cuándo lo devolvió (solo si hoy está sin responsable)
	UltimoResponsable *string     `json:"ultimoResponsable" db:"ultimo_responsable"`
	FechaDevolucion   *types.Date `json:"fechaDevolucion" db:"fecha_devolucion"`
}

// Detalle agrega al activo sus componentes instalados.
type Detalle struct {
	Activo
	Componentes []Activo `json:"componentes"`
}

// Input contiene los datos editables de un activo.
type Input struct {
	CategoriaID      int64          `json:"categoriaId" validate:"required,gt=0"`
	MarcaID          *int64         `json:"marcaId" validate:"omitempty,gt=0"`
	Modelo           *string        `json:"modelo" validate:"omitempty,max=120"`
	NumeroSerie      *string        `json:"numeroSerie" validate:"omitempty,max=100"`
	CodigoContable   *string        `json:"codigoContable" validate:"omitempty,max=50"`
	Descripcion      *string        `json:"descripcion" validate:"omitempty,max=300"`
	Especificaciones map[string]any `json:"especificaciones"`
	Condicion        string         `json:"condicion" validate:"required,oneof=NUEVO BUENO REGULAR MALO"`
	ActivoPadreID    *int64         `json:"activoPadreId" validate:"omitempty,gt=0"`
	ProveedorID      *int64         `json:"proveedorId" validate:"omitempty,gt=0"`
	FechaCompra      *types.Date    `json:"fechaCompra"`
	ValorCompra      *float64       `json:"valorCompra" validate:"omitempty,gte=0,lt=10000000"`
	NumeroFactura    *string        `json:"numeroFactura" validate:"omitempty,max=50"`
	GarantiaHasta    *types.Date    `json:"garantiaHasta"`
	Hostname         *string        `json:"hostname" validate:"omitempty,max=80"`
	DireccionIP      *string        `json:"direccionIp" validate:"omitempty,ip"`
	DireccionMAC     *string        `json:"direccionMac" validate:"omitempty,mac"`
	Observaciones    *string        `json:"observaciones" validate:"omitempty,max=4000"`
}

// CreateInput agrega la ubicación inicial y, opcionalmente, el responsable.
type CreateInput struct {
	Input
	AgenciaID      int64  `json:"agenciaId" validate:"required,gt=0"`
	DepartamentoID *int64 `json:"departamentoId" validate:"omitempty,gt=0"`
	EmpleadoID     *int64 `json:"empleadoId" validate:"omitempty,gt=0"`
	// Computadora completa: componentes (se instalan en el equipo) y periféricos (van con él) registrados a la vez
	Incluye []IncluidoInput `json:"incluye" validate:"omitempty,max=30,dive"`
}

// IncluidoInput es un componente o periférico que se registra junto con el equipo.
type IncluidoInput struct {
	CategoriaID int64   `json:"categoriaId" validate:"required,gt=0"`
	MarcaID     *int64  `json:"marcaId" validate:"omitempty,gt=0"`
	Modelo      *string `json:"modelo" validate:"omitempty,max=120"`
	NumeroSerie *string `json:"numeroSerie" validate:"omitempty,max=100"`
}

type EstadoInput struct {
	Estado string `json:"estado" validate:"required,oneof=ACTIVO EN_MANTENIMIENTO DANADO"`
	Motivo string `json:"motivo" validate:"required,max=300"`
}

type BajaInput struct {
	Fecha              *types.Date `json:"fecha"`
	Motivo             string      `json:"motivo" validate:"required,max=300"`
	IncluirComponentes bool        `json:"incluirComponentes"`
	GenerarActa        bool        `json:"generarActa"`
}

// Evento del historial del activo.
type Evento struct {
	ID           int64           `json:"id" db:"id"`
	ActivoID     int64           `json:"activoId" db:"activo_id"`
	ActivoCodigo string          `json:"activoCodigo" db:"activo_codigo"`
	TipoEvento   string          `json:"tipoEvento" db:"tipo_evento"`
	Descripcion  string          `json:"descripcion" db:"descripcion"`
	Datos        json.RawMessage `json:"datos" db:"datos"`
	Referencia   *string         `json:"referencia" db:"referencia"`
	ReferenciaID *int64          `json:"referenciaId" db:"referencia_id"`
	Usuario      *string         `json:"usuario" db:"usuario"`
	CreatedAt    time.Time       `json:"createdAt" db:"created_at"`
}
