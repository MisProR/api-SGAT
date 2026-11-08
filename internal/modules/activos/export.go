package activos

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"time"

	"sgat-api/internal/apperr"
	"sgat-api/internal/audit"
	"sgat-api/internal/database"
	"sgat-api/internal/httpx"
	"sgat-api/internal/modules/inventario"
)

// exportar descarga el inventario filtrado en CSV (separador ";" y BOM UTF-8 para Excel).
func (h *Handler) exportar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lq := httpx.ParseListQuery(r)
	where := filtros(r, lq)
	items, err := database.List[Activo](ctx, h.db, "SELECT v.* FROM v_activos v"+where.SQL()+
		" ORDER BY "+lq.OrderBy(sortable, "v.agencia, v.categoria, v.codigo"), where.Args()...)
	if err != nil {
		httpx.Error(w, r, apperr.Internal(err))
		return
	}
	_ = audit.Log(ctx, h.db, audit.Entry{Accion: audit.Exportar, Entidad: "activos",
		Descripcion: fmt.Sprintf("Exportación de inventario (%d registros)", len(items))})

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="inventario_%s.csv"`, time.Now().Format("20060102_1504")))
	_, _ = w.Write([]byte("\xEF\xBB\xBF"))

	cw := csv.NewWriter(w)
	cw.Comma = ';'
	_ = cw.Write([]string{"Código", "Categoría", "Tipo", "Marca", "Modelo", "Serie", "Código contable", "Estado", "Condición",
		"Instalado en", "Agencia", "Departamento", "Responsable", "Cédula", "Hostname", "IP", "Proveedor", "Fecha compra",
		"Valor compra", "Valor actual", "Garantía hasta", "Último mantenimiento", "Próximo mantenimiento"})
	for _, a := range items {
		_ = cw.Write([]string{a.Codigo, a.Categoria, a.CategoriaTipo, str(a.Marca), str(a.Modelo), str(a.NumeroSerie),
			str(a.CodigoContable), inventario.EstadoNombre[a.Estado], a.Condicion, str(a.ActivoPadreCodigo), a.Agencia,
			str(a.Departamento), str(a.Empleado), str(a.EmpleadoCedula), str(a.Hostname), str(a.DireccionIP), str(a.Proveedor),
			date(a.FechaCompra), money(a.ValorCompra), money(a.ValorActual), date(a.GarantiaHasta), date(a.UltimoMantenimiento),
			date(a.ProximoMantenimiento)})
	}
	cw.Flush()
}
