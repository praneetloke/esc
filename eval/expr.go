// Copyright 2023, Pulumi Corporation.  All rights reserved.

package eval

import (
	"fmt"

	"github.com/pulumi/environments"
	"github.com/pulumi/environments/ast"
	"github.com/pulumi/environments/schema"
)

const (
	exprDeclared int = iota
	exprEvaluating
	exprDone
)

// An expr represents a single expression in an environment definition.
//
// Each expr holds additional state relative to its corresponding syntax. In particular, an expr holds the expression's
// base value, schema, state, secretness, and its memoized value after evaluation.
type expr struct {
	path string   // The path of the expression, if any. Used when reporting cyclic references.
	repr exprRepr // The expression's representation.
	base *value   // The base value of the expression. This is the imported value the expression overrides, if any.

	schema *schema.Schema // The expression's schema. May not be fully-determined until after evaluation.

	state int // The expression's state.

	secret bool // Whether or not to treat the expression's value as secret.

	value *value // The memoized result of evaluating this expression.
}

// newExpr creates a new expression.
func newExpr(path string, repr exprRepr, s *schema.Schema, base *value) *expr {
	return &expr{path: path, repr: repr, schema: s, base: base}
}

// defRange returns the source range for the expression. If the expression does not have source information, it
// returns a range that only refers to the given environment.
func (x *expr) defRange(environment string) environments.Range {
	rng := environments.Range{Environment: environment}
	if r := x.repr.syntax().Syntax().Syntax().Range(); r != nil {
		rng.Environment = r.Filename
		rng.Begin = environments.Pos{Line: r.Start.Line, Column: r.Start.Column, Byte: r.Start.Byte}
		rng.End = environments.Pos{Line: r.End.Line, Column: r.End.Column, Byte: r.End.Byte}
	}
	return rng
}

func exportAccessor(accessor ast.PropertyAccessor) environments.Accessor {
	switch a := accessor.(type) {
	case *ast.PropertyName:
		return environments.Accessor{Key: &a.Name}
	case *ast.PropertySubscript:
		switch index := a.Index.(type) {
		case string:
			return environments.Accessor{Key: &index}
		case int:
			return environments.Accessor{Index: &index}
		}
	}
	panic(fmt.Errorf("invalid property accessor %#v", accessor))
}

// export transforms an expr into its exported, serializable representation.
func (x *expr) export(environment string) environments.Expr {
	var base *environments.Expr
	if x.base != nil {
		b := x.base.def.export(environment)
		base = &b
	}

	ex := environments.Expr{
		Range:  x.defRange(environment),
		Schema: x.schema,
		Base:   base,
	}

	switch repr := x.repr.(type) {
	case *literalExpr:
		switch syntax := x.repr.syntax().(type) {
		case *ast.BooleanExpr:
			ex.Literal = syntax.Value
		case *ast.NumberExpr:
			ex.Literal = syntax.Value
		case *ast.StringExpr:
			ex.Literal = syntax.Value
		}
	case *interpolateExpr:
		interp := make([]environments.Interpolation, len(repr.parts))
		for i, p := range repr.parts {
			var value []environments.PropertyAccessor
			if p.value != nil {
				value = make([]environments.PropertyAccessor, len(p.value.accessors))
				for i, a := range p.value.accessors {
					value[i] = environments.PropertyAccessor{
						Accessor: exportAccessor(a.accessor),
						Value:    a.value.def.defRange(environment),
					}
				}
			}
			interp[i] = environments.Interpolation{
				Text:  p.syntax.Text,
				Value: value,
			}
		}
		ex.Interpolate = interp
	case *symbolExpr:
		value := make([]environments.PropertyAccessor, len(repr.property.accessors))
		for i, a := range repr.property.accessors {
			value[i] = environments.PropertyAccessor{
				Accessor: exportAccessor(a.accessor),
				Value:    a.value.def.defRange(environment),
			}
		}
		ex.Symbol = value
	case *accessExpr:
		accessor := exportAccessor(repr.accessor)
		if _, ok := repr.receiver.def.repr.(*accessExpr); ok {
			ex = repr.receiver.def.export(environment)
			ex.Access.Accessors = append(ex.Access.Accessors, accessor)
		} else {
			ex.Access = &environments.AccessExpr{
				Receiver:  repr.receiver.def.defRange(environment),
				Accessors: []environments.Accessor{accessor},
			}
		}
	case *joinExpr:
		ex.Builtin = &environments.BuiltinExpr{
			ArgSchema: schema.Tuple(schema.String(), schema.Array().Items(schema.String())).Schema(),
			Arg:       environments.Expr{List: []environments.Expr{repr.delimiter.export(environment), repr.values.export(environment)}},
		}
	case *openExpr:
		ex.Builtin = &environments.BuiltinExpr{
			ArgSchema: schema.Record(map[string]schema.Builder{
				"provider": schema.String(),
				"inputs":   repr.inputSchema,
			}).Schema(),
			Arg: environments.Expr{
				Object: map[string]environments.Expr{
					"provider": repr.provider.export(environment),
					"inputs":   repr.inputs.export(environment),
				},
			},
		}
	case *secretExpr:
		ex.Builtin = &environments.BuiltinExpr{
			ArgSchema: schema.Always().Schema(),
			Arg:       repr.value.export(environment),
		}
	case *toBase64Expr:
		ex.Builtin = &environments.BuiltinExpr{
			ArgSchema: schema.String().Schema(),
			Arg:       repr.value.export(environment),
		}
	case *toJSONExpr:
		ex.Builtin = &environments.BuiltinExpr{
			ArgSchema: schema.Always().Schema(),
			Arg:       repr.value.export(environment),
		}
	case *toStringExpr:
		ex.Builtin = &environments.BuiltinExpr{
			ArgSchema: schema.Always().Schema(),
			Arg:       repr.value.export(environment),
		}
	case *listExpr:
		ex.List = make([]environments.Expr, len(repr.elements))
		for i, el := range repr.elements {
			ex.List[i] = el.export(environment)
		}
	case *objectExpr:
		ex.Object = make(map[string]environments.Expr, len(repr.properties))
		for k, v := range repr.properties {
			ex.Object[k] = v.export(environment)
		}
	default:
		panic(fmt.Sprintf("fatal: invalid expr type %T", repr))
	}

	return ex
}

type propertyAccess struct {
	accessors []*propertyAccessor
}

type propertyAccessor struct {
	accessor ast.PropertyAccessor
	value    *value
}

type interpolation struct {
	syntax ast.Interpolation
	value  *propertyAccess
}

type exprRepr interface {
	syntax() ast.Expr
}

// literalExpr represents a literal value.
type literalExpr struct {
	node ast.Expr
}

func (x *literalExpr) syntax() ast.Expr {
	return x.node
}

// interpolateExpr represents an interpolated string.
type interpolateExpr struct {
	node *ast.InterpolateExpr

	parts []interpolation
}

func (x *interpolateExpr) syntax() ast.Expr {
	return x.node
}

// symbolExpr represents a reference to another value.
type symbolExpr struct {
	node *ast.SymbolExpr

	property *propertyAccess
}

func (x *symbolExpr) syntax() ast.Expr {
	return x.node
}

// accessExpr represents a late-bound property access.
type accessExpr struct {
	node ast.Expr

	receiver *value
	accessor ast.PropertyAccessor
}

func (x *accessExpr) syntax() ast.Expr {
	return x.node
}

// listExpr represents a list literal.
type listExpr struct {
	node *ast.ListExpr

	elements []*expr
}

func (x *listExpr) syntax() ast.Expr {
	return x.node
}

// objectExpr represents an object literal.
type objectExpr struct {
	node *ast.ObjectExpr

	properties map[string]*expr
}

func (x *objectExpr) syntax() ast.Expr {
	return x.node
}

// openExpr represents a call to the fn::open builtin.
type openExpr struct {
	node *ast.OpenExpr

	provider *expr
	inputs   *expr

	inputSchema *schema.Schema
}

func (x *openExpr) syntax() ast.Expr {
	return x.node
}

// toJSONExpr represents a call to the fn::toJSON builtin.
type toJSONExpr struct {
	node *ast.ToJSONExpr

	value *expr
}

func (x *toJSONExpr) syntax() ast.Expr {
	return x.node
}

// toStringExpr represents a call to the fn::toString builtin.
type toStringExpr struct {
	node *ast.ToStringExpr

	value *expr
}

func (x *toStringExpr) syntax() ast.Expr {
	return x.node
}

// joinExpr represents a call to the fn::join builtin.
type joinExpr struct {
	node *ast.JoinExpr

	delimiter *expr
	values    *expr
}

func (x *joinExpr) syntax() ast.Expr {
	return x.node
}

// secretExpr represents a call to the fn::secret builtin.
type secretExpr struct {
	node *ast.SecretExpr

	value *expr
}

func (x *secretExpr) syntax() ast.Expr {
	return x.node
}

// toBase64Expr represents a call to the fn::toBase64 builtin.
type toBase64Expr struct {
	node *ast.ToBase64Expr

	value *expr
}

func (x *toBase64Expr) syntax() ast.Expr {
	return x.node
}
