package config

import (
	"fmt"
	"os"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// envFunc returns the env(name) HCL function backed by lookup
// (os.LookupEnv when nil). Unlike hclkit's funcs.Env, an unset or
// empty variable is an error, not an empty string: secrets reach the
// config only as env() references (DESIGN-0001 OQ-4), and a missing
// export must fail loudly at load time. The error surfaces as a
// decode diagnostic anchored at the env() call, naming the variable.
func envFunc(lookup func(string) (string, bool)) function.Function {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return function.New(&function.Spec{
		Description: "Returns the value of the named environment variable; unset or empty is an error.",
		Params: []function.Parameter{
			{Name: "name", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			name := args[0].AsString()
			val, ok := lookup(name)
			if !ok {
				return cty.NilVal, fmt.Errorf("environment variable %s is not set", name)
			}
			if val == "" {
				return cty.NilVal, fmt.Errorf("environment variable %s is set but empty", name)
			}
			return cty.StringVal(val), nil
		},
	})
}
