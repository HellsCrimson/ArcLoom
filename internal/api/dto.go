package api

import (
	"encoding/json"
	"fmt"

	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// attrValueDTO is the wire form of a single attribute value. For literals the
// concrete value arrives as raw JSON and is converted to a cty.Value; its type
// is implied from the JSON shape (string/number/bool/array/object).
type attrValueDTO struct {
	Mode      model.ValueMode `json:"mode"`
	Literal   json.RawMessage `json:"literal,omitempty"`
	Ref       string          `json:"ref,omitempty"`
	Expr      string          `json:"expr,omitempty"`
	SecretRef string          `json:"secretRef,omitempty"`
}

func (d attrValueDTO) toModel() (model.AttrValue, error) {
	av := model.AttrValue{
		Mode:      d.Mode,
		Ref:       d.Ref,
		Expr:      d.Expr,
		SecretRef: d.SecretRef,
	}
	if d.Mode == model.ModeLiteral {
		if len(d.Literal) == 0 {
			return av, fmt.Errorf("literal value missing")
		}
		t, err := ctyjson.ImpliedType(d.Literal)
		if err != nil {
			return av, fmt.Errorf("infer literal type: %w", err)
		}
		v, err := ctyjson.Unmarshal(d.Literal, t)
		if err != nil {
			return av, fmt.Errorf("decode literal: %w", err)
		}
		av.Literal = v
	}
	return av, nil
}

func attrsToModel(in map[string]attrValueDTO) (map[string]model.AttrValue, error) {
	out := make(map[string]model.AttrValue, len(in))
	for name, dto := range in {
		av, err := dto.toModel()
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", name, err)
		}
		out[name] = av
	}
	return out, nil
}

// resourceRequest is the body for creating/updating a resource.
type resourceRequest struct {
	ID             string                  `json:"id,omitempty"`
	Type           string                  `json:"type"`
	Name           string                  `json:"name"`
	ProviderSource string                  `json:"providerSource,omitempty"`
	Attrs          map[string]attrValueDTO `json:"attrs"`
}

// engineDTO describes an available CLI engine.
type engineDTO struct {
	Engine   string `json:"engine"`
	ExecPath string `json:"execPath"`
	Version  string `json:"version"`
}

// providerRequest is the body for registering a provider (fetches schema).
type providerRequest struct {
	Engine  string `json:"engine,omitempty"`
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
}

// providerConfigRequest sets a provider block's configuration on a project.
type providerConfigRequest struct {
	Source  string                  `json:"source,omitempty"`
	Version string                  `json:"version,omitempty"`
	Config  map[string]attrValueDTO `json:"config"`
}

// providerResponse lists a provider's resource types after schema fetch.
type providerResponse struct {
	Provider      string   `json:"provider"`
	ResourceTypes []string `json:"resourceTypes"`
}

// projectRequest creates a project.
type projectRequest struct {
	Name   string `json:"name"`
	Engine string `json:"engine,omitempty"`
}
