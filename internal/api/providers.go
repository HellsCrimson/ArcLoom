package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/cache"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/schema"
)

// writeJSON encodes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr writes a JSON error envelope.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// handleEngines reports which engines are available with their versions.
func (s *Server) handleEngines(w http.ResponseWriter, r *http.Request) {
	out := make([]engineDTO, 0, len(s.runners))
	for eng, run := range s.runners {
		ver, err := run.Version(r.Context())
		if err != nil {
			ver = "unknown"
		}
		out = append(out, engineDTO{Engine: string(eng), ExecPath: run.ExecPath(), Version: ver})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Engine < out[j].Engine })
	writeJSON(w, http.StatusOK, map[string]any{
		"defaultEngine": s.cfg.DefaultEngine,
		"engines":       out,
	})
}

// handleAddProvider fetches (and caches) a provider's schema, returning its
// resource types. This is the point where init + providers schema runs.
func (s *Server) handleAddProvider(w http.ResponseWriter, r *http.Request) {
	var req providerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.Source == "" {
		writeErr(w, http.StatusBadRequest, "source is required")
		return
	}
	if !validProviderSource(req.Source) {
		writeErr(w, http.StatusBadRequest, "invalid provider source")
		return
	}
	run, ok := s.runnerFor(req.Engine)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown engine")
		return
	}

	prov := model.ProviderInstance{
		Key:     providerKeyFromSource(req.Source),
		Source:  req.Source,
		Version: req.Version,
	}

	schemas, err := run.ProvidersSchema(r.Context(), []model.ProviderInstance{prov})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "schema fetch failed: "+err.Error())
		return
	}

	fq, ps := findProviderSchema(schemas, req.Source)
	if ps == nil {
		writeErr(w, http.StatusBadGateway, "provider schema not found in output")
		return
	}

	// Normalize and cache every resource type for later schema requests. The
	// cache is keyed on the version constraint the schemas were fetched under
	// ("" = latest), so differently-pinned providers don't collide.
	types := make([]string, 0, len(ps.ResourceSchemas))
	for rt, rs := range ps.ResourceSchemas {
		types = append(types, rt)
		ns := schema.Normalize(fq, rt, rs)
		key := cache.Key(string(run.Engine()), req.Source, req.Version, rt)
		s.cache.Put(key, ns)
	}
	sort.Strings(types)

	// Same for data sources. A type name can exist as both a resource and a
	// data source (e.g. aws_instance), so data schemas get their own key prefix.
	dataTypes := make([]string, 0, len(ps.DataSourceSchemas))
	for dt, ds := range ps.DataSourceSchemas {
		dataTypes = append(dataTypes, dt)
		ns := schema.Normalize(fq, dt, ds)
		key := cache.Key(string(run.Engine()), req.Source, req.Version, dataSchemaPrefix+dt)
		s.cache.Put(key, ns)
	}
	sort.Strings(dataTypes)

	writeJSON(w, http.StatusOK, providerResponse{Provider: fq, ResourceTypes: types, DataSourceTypes: dataTypes})
}

// dataSchemaPrefix namespaces data-source entries in the schema cache so they
// cannot collide with a same-named resource type.
const dataSchemaPrefix = "data:"

// handleDataSourceSchema returns the normalized schema for one data source
// type, mirroring handleResourceSchema.
func (s *Server) handleDataSourceSchema(w http.ResponseWriter, r *http.Request) {
	source, err := url.PathUnescape(chi.URLParam(r, "source"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid source")
		return
	}
	if !validProviderSource(source) {
		writeErr(w, http.StatusBadRequest, "invalid provider source")
		return
	}
	dataType := chi.URLParam(r, "type")
	engine := r.URL.Query().Get("engine")
	version := r.URL.Query().Get("version")

	run, ok := s.runnerFor(engine)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown engine")
		return
	}

	if ns, ok := s.lookupCached(string(run.Engine()), source, version, dataSchemaPrefix+dataType); ok {
		writeJSON(w, http.StatusOK, ns)
		return
	}

	prov := model.ProviderInstance{Key: providerKeyFromSource(source), Source: source, Version: version}
	schemas, err := run.ProvidersSchema(r.Context(), []model.ProviderInstance{prov})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "schema fetch failed: "+err.Error())
		return
	}
	fq, ps := findProviderSchema(schemas, source)
	if ps == nil {
		writeErr(w, http.StatusBadGateway, "provider schema not found")
		return
	}
	ds, ok := ps.DataSourceSchemas[dataType]
	if !ok {
		writeErr(w, http.StatusNotFound, "data source type not found")
		return
	}
	ns := schema.Normalize(fq, dataType, ds)
	s.cache.Put(cache.Key(string(run.Engine()), source, version, dataSchemaPrefix+dataType), ns)
	writeJSON(w, http.StatusOK, ns)
}

// handleResourceSchema returns the normalized schema for one resource type.
// {source} is URL-encoded (e.g. "hashicorp%2Frandom").
func (s *Server) handleResourceSchema(w http.ResponseWriter, r *http.Request) {
	source, err := url.PathUnescape(chi.URLParam(r, "source"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid source")
		return
	}
	if !validProviderSource(source) {
		writeErr(w, http.StatusBadRequest, "invalid provider source")
		return
	}
	resourceType := chi.URLParam(r, "type")
	engine := r.URL.Query().Get("engine")
	version := r.URL.Query().Get("version")

	run, ok := s.runnerFor(engine)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown engine")
		return
	}

	if ns, ok := s.lookupCached(string(run.Engine()), source, version, resourceType); ok {
		writeJSON(w, http.StatusOK, ns)
		return
	}

	// Cache miss: fetch schemas now, pinned to the requested constraint.
	prov := model.ProviderInstance{Key: providerKeyFromSource(source), Source: source, Version: version}
	schemas, err := run.ProvidersSchema(r.Context(), []model.ProviderInstance{prov})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "schema fetch failed: "+err.Error())
		return
	}
	fq, ps := findProviderSchema(schemas, source)
	if ps == nil {
		writeErr(w, http.StatusBadGateway, "provider schema not found")
		return
	}
	rs, ok := ps.ResourceSchemas[resourceType]
	if !ok {
		writeErr(w, http.StatusNotFound, "resource type not found")
		return
	}
	ns := schema.Normalize(fq, resourceType, rs)
	s.cache.Put(cache.Key(string(run.Engine()), source, version, resourceType), ns)
	writeJSON(w, http.StatusOK, ns)
}

// providerSchemaKey is the sentinel resource-type used to cache the provider
// block's own configuration schema.
const providerSchemaKey = "__provider__"

// handleProviderSchema returns the normalized schema for a provider's own
// configuration block (host, api_key, etc.).
func (s *Server) handleProviderSchema(w http.ResponseWriter, r *http.Request) {
	source, err := url.PathUnescape(chi.URLParam(r, "source"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid source")
		return
	}
	if !validProviderSource(source) {
		writeErr(w, http.StatusBadRequest, "invalid provider source")
		return
	}
	engine := r.URL.Query().Get("engine")
	version := r.URL.Query().Get("version")
	run, ok := s.runnerFor(engine)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown engine")
		return
	}

	if ns, ok := s.lookupCached(string(run.Engine()), source, version, providerSchemaKey); ok {
		writeJSON(w, http.StatusOK, ns)
		return
	}

	prov := model.ProviderInstance{Key: providerKeyFromSource(source), Source: source, Version: version}
	schemas, err := run.ProvidersSchema(r.Context(), []model.ProviderInstance{prov})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "schema fetch failed: "+err.Error())
		return
	}
	fq, ps := findProviderSchema(schemas, source)
	if ps == nil {
		writeErr(w, http.StatusBadGateway, "provider schema not found")
		return
	}
	ns := schema.Normalize(fq, source, ps.ConfigSchema)
	s.cache.Put(cache.Key(string(run.Engine()), source, version, providerSchemaKey), ns)
	writeJSON(w, http.StatusOK, ns)
}

// lookupCached checks the cache under the requested version constraint. There
// is deliberately no cross-version fallback: serving a "latest" schema for a
// pinned provider is exactly the collision this keying fixes — an unseen
// constraint just refetches once.
func (s *Server) lookupCached(engine, source, version, resourceType string) (schema.NormalizedSchema, bool) {
	return s.cache.Get(cache.Key(engine, source, version, resourceType))
}

// providerKeyFromSource extracts the local provider name, e.g.
// "hashicorp/random" -> "random".
func providerKeyFromSource(source string) string {
	seg := source
	if i := strings.LastIndex(source, "/"); i >= 0 {
		seg = source[i+1:]
	}
	return seg
}
