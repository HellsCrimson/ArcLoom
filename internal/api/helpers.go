package api

import (
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
)

// findProviderSchema locates a provider's schema in the providers-schema output
// by matching the requested source ("hashicorp/random") against the
// fully-qualified keys ("registry.terraform.io/hashicorp/random"). It returns
// the fully-qualified address and the schema.
func findProviderSchema(schemas *tfjson.ProviderSchemas, source string) (string, *tfjson.ProviderSchema) {
	if schemas == nil {
		return "", nil
	}
	// Exact match first.
	if ps, ok := schemas.Schemas[source]; ok {
		return source, ps
	}
	// Suffix match on "/<source>" (handles the registry host prefix).
	want := "/" + strings.Trim(source, "/")
	for fq, ps := range schemas.Schemas {
		if strings.HasSuffix(fq, want) || fq == source {
			return fq, ps
		}
	}
	// Single-provider fallback.
	if len(schemas.Schemas) == 1 {
		for fq, ps := range schemas.Schemas {
			return fq, ps
		}
	}
	return "", nil
}
