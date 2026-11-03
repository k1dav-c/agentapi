package util

import (
	"fmt"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// based on https://github.com/danielgtaylor/huma/issues/621#issuecomment-2456588788
func OpenAPISchema[T ~string](r huma.Registry, enumName string, values []T) *huma.Schema {
	if r.Map()[enumName] == nil {
		schemaRef := r.Schema(reflect.TypeOf(""), true, enumName)
		schemaRef.Title = enumName
		schemaRef.Examples = []any{values[0]}
		for _, v := range values {
			schemaRef.Enum = append(schemaRef.Enum, string(v))
		}
		r.Map()[enumName] = schemaRef
	}
	return &huma.Schema{Ref: fmt.Sprintf("#/components/schemas/%s", enumName)}
}
