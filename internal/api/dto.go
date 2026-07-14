package api

import (
	"encoding/json"
	"fmt"

	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/matthiasrauline/arcloom/internal/model"
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
	DependsOn      []string                `json:"dependsOn,omitempty"`
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

// projectConfigRequest replaces a project's variables, locals, and outputs.
type projectConfigRequest struct {
	Variables []variableDTO `json:"variables"`
	Locals    []localDTO    `json:"locals"`
	Outputs   []outputDTO   `json:"outputs"`
}

type variableDTO struct {
	Name        string        `json:"name"`
	Type        string        `json:"type,omitempty"`
	Default     *attrValueDTO `json:"default,omitempty"`
	Description string        `json:"description,omitempty"`
	Sensitive   bool          `json:"sensitive,omitempty"`
}

type localDTO struct {
	Name  string       `json:"name"`
	Value attrValueDTO `json:"value"`
}

type outputDTO struct {
	Name        string       `json:"name"`
	Value       attrValueDTO `json:"value"`
	Description string       `json:"description,omitempty"`
	Sensitive   bool         `json:"sensitive,omitempty"`
}

func (r projectConfigRequest) toModel() (vars []model.Variable, locals []model.Local, outputs []model.Output, err error) {
	for _, v := range r.Variables {
		if v.Name == "" {
			continue
		}
		mv := model.Variable{Name: v.Name, Type: v.Type, Description: v.Description, Sensitive: v.Sensitive}
		if v.Default != nil {
			av, e := v.Default.toModel()
			if e != nil {
				return nil, nil, nil, fmt.Errorf("variable %q default: %w", v.Name, e)
			}
			mv.Default = &av
		}
		vars = append(vars, mv)
	}
	for _, l := range r.Locals {
		if l.Name == "" {
			continue
		}
		av, e := l.Value.toModel()
		if e != nil {
			return nil, nil, nil, fmt.Errorf("local %q: %w", l.Name, e)
		}
		locals = append(locals, model.Local{Name: l.Name, Value: av})
	}
	for _, o := range r.Outputs {
		if o.Name == "" {
			continue
		}
		av, e := o.Value.toModel()
		if e != nil {
			return nil, nil, nil, fmt.Errorf("output %q: %w", o.Name, e)
		}
		outputs = append(outputs, model.Output{Name: o.Name, Value: av, Description: o.Description, Sensitive: o.Sensitive})
	}
	return vars, locals, outputs, nil
}
