package httpx

import "net/http"

// paged permite que Handle detecte resultados paginados y agregue "meta".
type paged interface{ envelope() Envelope }

func (p Page[T]) envelope() Envelope {
	items := p.Items
	if items == nil {
		items = []T{}
	}
	return Envelope{Success: true, Data: items, Meta: &p.Meta}
}

// MessageResult hace que Handle responda solo con un mensaje.
type MessageResult string

// Handle adapta una función (request) -> (datos, error) a un http.HandlerFunc
// que responde con el formato estándar. Elimina el código repetido en los handlers.
func Handle(fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return handle(http.StatusOK, fn)
}

// HandleCreated es igual a Handle pero responde 201 Created.
func HandleCreated(fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return handle(http.StatusCreated, fn)
}

func handle(status int, fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fn(r)
		if err != nil {
			Error(w, r, err)
			return
		}
		switch v := data.(type) {
		case paged:
			JSON(w, http.StatusOK, v.envelope())
		case MessageResult:
			JSON(w, status, Envelope{Success: true, Message: string(v)})
		default:
			JSON(w, status, Envelope{Success: true, Data: v})
		}
	}
}
