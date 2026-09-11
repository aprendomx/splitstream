package platforms

import "sort"

// Registry es el conjunto de proveedores cargados. Es un valor, no un global: main.go lo
// construye y se lo da a la API y al chat; los tests montan el suyo con dobles.
type Registry struct{ byID map[ID]Provider }

func NewRegistry(ps ...Provider) *Registry {
	r := &Registry{byID: map[ID]Provider{}}
	for _, p := range ps {
		r.Register(p)
	}
	return r
}

func (r *Registry) Register(p Provider) { r.byID[p.ID()] = p }

func (r *Registry) Get(id ID) (Provider, bool) {
	p, ok := r.byID[id]
	return p, ok
}

// All devuelve los proveedores ordenados por id, para respuestas estables.
func (r *Registry) All() []Provider {
	out := make([]Provider, 0, len(r.byID))
	for _, p := range r.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

func (r *Registry) AllCapabilities() map[ID]Capabilities {
	out := make(map[ID]Capabilities, len(r.byID))
	for id, p := range r.byID {
		out[id] = p.Capabilities()
	}
	return out
}
