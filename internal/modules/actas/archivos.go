package actas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/security"
)

// Archivo es el acta física firmada y escaneada que se adjunta a un acta.
type Archivo struct {
	ID             int64     `json:"id" db:"id"`
	ActaID         int64     `json:"actaId" db:"acta_id"`
	NombreOriginal string    `json:"nombre" db:"nombre_original"`
	Ruta           string    `json:"-" db:"ruta"`
	TipoMime       string    `json:"tipoMime" db:"tipo_mime"`
	TamanoBytes    int64     `json:"tamanoBytes" db:"tamano_bytes"`
	SubidoPorID    *int64    `json:"-" db:"subido_por"`
	SubidoPor      *string   `json:"subidoPor" db:"subido_por_nombre"`
	CreatedAt      time.Time `json:"createdAt" db:"created_at"`
}

const (
	maxArchivo = 10 << 20 // 10 MB (igual que ck_actas_archivos_tamano)
	campoForm  = "archivo"
)

// Tipos aceptados según el contenido real del archivo (no la extensión que envía el navegador).
var extensiones = map[string]string{"application/pdf": ".pdf", "image/jpeg": ".jpg", "image/png": ".png"}

const archivoSelect = `SELECT f.*, u.nombres + ' ' + u.apellidos AS subido_por_nombre
	FROM actas_archivos f LEFT JOIN usuarios u ON u.id = f.subido_por`

func listarArchivos(ctx context.Context, q database.Querier, actaID int64) ([]Archivo, error) {
	return database.List[Archivo](ctx, q, archivoSelect+" WHERE f.acta_id = $1 ORDER BY f.created_at DESC", actaID)
}

// visible verifica que el acta exista y que el usuario pueda verla (el encargado solo las de su agencia).
func (h *Handler) visible(ctx context.Context, q database.Querier, id int64) (anulada bool, numero string, err error) {
	w := (&database.Where{}).Add("ac.id = ?", id)
	scope(ctx, w)
	err = q.QueryRow(ctx, "SELECT ac.anulada, ac.numero "+fromSQL+w.SQL(), w.Args()...).Scan(&anulada, &numero)
	return anulada, numero, apperr.FromDB(err, "Acta no encontrada")
}

func (h *Handler) listArchivos(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	if _, _, err := h.visible(r.Context(), h.db, id); err != nil {
		return nil, err
	}
	return listarArchivos(r.Context(), h.db, id)
}

// subirArchivo recibe el acta firmada (multipart, campo "archivo") y la guarda en la carpeta de archivos.
func (h *Handler) subirArchivo(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	anulada, numero, err := h.visible(ctx, h.db, id)
	if err != nil {
		return nil, err
	}
	if anulada {
		return nil, apperr.Conflict("El acta " + numero + " está anulada")
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxArchivo+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return nil, apperr.BadRequest("El archivo no es válido o supera los 10 MB")
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	f, hdr, err := r.FormFile(campoForm)
	if err != nil {
		return nil, apperr.Validation(map[string]string{campoForm: "Seleccione el archivo del acta firmada"})
	}
	defer f.Close()
	if hdr.Size <= 0 || hdr.Size > maxArchivo {
		return nil, apperr.Validation(map[string]string{campoForm: "El archivo debe pesar como máximo 10 MB"})
	}

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	tipo, _, _ := mime.ParseMediaType(http.DetectContentType(head[:n]))
	ext, ok := extensiones[tipo]
	if !ok {
		return nil, apperr.Validation(map[string]string{campoForm: "Solo se permiten archivos PDF, JPG o PNG"})
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, apperr.Internal(err)
	}

	nombre, err := nombreAleatorio()
	if err != nil {
		return nil, apperr.Internal(err)
	}
	rel := filepath.ToSlash(filepath.Join("actas", time.Now().Format("2006"), nombre+ext))
	abs := filepath.Join(h.uploads, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return nil, apperr.Internal(err)
	}
	dst, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	written, err := io.Copy(dst, io.LimitReader(f, maxArchivo+1))
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil || written > maxArchivo {
		_ = os.Remove(abs)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		return nil, apperr.Validation(map[string]string{campoForm: "El archivo debe pesar como máximo 10 MB"})
	}

	var archivo Archivo
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		var archivoID int64
		if err := tx.QueryRow(ctx, `INSERT INTO actas_archivos (acta_id, nombre_original, ruta, tipo_mime, tamano_bytes, subido_por)
			VALUES ($1, $2, $3, $4, $5, $6);
			SELECT CAST(SCOPE_IDENTITY() AS BIGINT)`,
			id, nombreSeguro(hdr.Filename, ext), rel, tipo, written, security.UserID(ctx)).Scan(&archivoID); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, audit.Entry{Accion: audit.SubirArchivo, Entidad: "actas", EntidadID: &id,
			Descripcion: "Acta firmada adjuntada al acta " + numero}); err != nil {
			return err
		}
		archivo, err = database.Get[Archivo](ctx, tx, archivoSelect+" WHERE f.id = $1", archivoID)
		return err
	})
	if err != nil {
		_ = os.Remove(abs)
		return nil, apperr.FromDB(err, "")
	}
	return archivo, nil
}

// descargarArchivo envía el archivo adjunto (solo si el usuario puede ver el acta).
func (h *Handler) descargarArchivo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	archivoID, err := httpx.IDParam(r, "archivoId")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if _, _, err := h.visible(ctx, h.db, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, err := database.Get[Archivo](ctx, h.db, archivoSelect+" WHERE f.id = $1 AND f.acta_id = $2", archivoID, id)
	if err != nil {
		httpx.Error(w, r, apperr.FromDB(err, "Archivo no encontrado"))
		return
	}
	abs, err := h.rutaSegura(a.Ruta)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	file, err := os.Open(abs)
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("El archivo ya no está disponible en el servidor"))
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", a.TipoMime)
	w.Header().Set("Content-Length", fmt.Sprint(a.TamanoBytes))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.NombreOriginal}))
	_, _ = io.Copy(w, file)
}

// eliminarArchivo quita un adjunto (solo SUPERADMIN, para corregir cargas equivocadas).
func (h *Handler) eliminarArchivo(r *http.Request) (any, error) {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	archivoID, err := httpx.IDParam(r, "archivoId")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	var ruta, numero string
	err = database.WithTx(ctx, h.db, func(tx database.Tx) error {
		if err := tx.QueryRow(ctx, `DELETE f OUTPUT DELETED.ruta, ac.numero
			FROM actas_archivos f JOIN actas ac ON ac.id = f.acta_id
			WHERE f.id = $1 AND f.acta_id = $2`, archivoID, id).
			Scan(&ruta, &numero); err != nil {
			return apperr.FromDB(err, "Archivo no encontrado")
		}
		return audit.Log(ctx, tx, audit.Entry{Accion: audit.Eliminar, Entidad: "actas", EntidadID: &id,
			Descripcion: "Archivo eliminado del acta " + numero})
	})
	if err != nil {
		return nil, err
	}
	if abs, err := h.rutaSegura(ruta); err == nil {
		_ = os.Remove(abs)
	}
	return httpx.MessageResult("Archivo eliminado"), nil
}

// rutaSegura convierte la ruta guardada en una ruta absoluta dentro de la carpeta de archivos.
func (h *Handler) rutaSegura(rel string) (string, error) {
	base, err := filepath.Abs(h.uploads)
	if err != nil {
		return "", apperr.Internal(err)
	}
	abs := filepath.Join(base, filepath.FromSlash(rel))
	if r, err := filepath.Rel(base, abs); err != nil || strings.HasPrefix(r, "..") {
		return "", apperr.Internal(errors.New("ruta de archivo fuera de la carpeta permitida"))
	}
	return abs, nil
}

func nombreAleatorio() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// nombreSeguro conserva el nombre original (para la descarga) sin rutas ni caracteres de control.
func nombreSeguro(nombre, ext string) string {
	nombre = filepath.Base(strings.ReplaceAll(nombre, "\\", "/"))
	nombre = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' {
			return -1
		}
		return r
	}, nombre)
	nombre = strings.TrimSuffix(nombre, filepath.Ext(nombre))
	if nombre == "" || nombre == "." {
		nombre = "acta"
	}
	if r := []rune(nombre); len(r) > 150 {
		nombre = string(r[:150])
	}
	return nombre + ext
}
