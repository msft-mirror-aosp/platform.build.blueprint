// Copyright 2014 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package blueprint

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A Deps value indicates the dependency file format that Ninja should expect to
// be output by a compiler.
type Deps int

const (
	DepsNone Deps = iota
	DepsGCC
	DepsMSVC
)

func (d Deps) String() string {
	switch d {
	case DepsNone:
		return "none"
	case DepsGCC:
		return "gcc"
	case DepsMSVC:
		return "msvc"
	default:
		panic(fmt.Sprintf("unknown deps value: %d", d))
	}
}

// A PoolParams object contains the set of parameters that make up a Ninja pool
// definition.
type PoolParams struct {
	Comment string // The comment that will appear above the definition.
	Depth   int    // The Ninja pool depth.
}

// A RuleParams object contains the set of parameters that make up a Ninja rule
// definition.
type RuleParams struct {
	// These fields correspond to a Ninja variable of the same name.
	Command        string // The command that Ninja will run for the rule.
	Depfile        string // The dependency file name.
	Deps           Deps   // The format of the dependency file.
	Description    string // The description that Ninja will print for the rule.
	Generator      bool   // Whether the rule generates the Ninja manifest file.
	Pool           Pool   // The Ninja pool to which the rule belongs.
	Restat         bool   // Whether Ninja should re-stat the rule's outputs.
	Rspfile        string // The response file.
	RspfileContent string // The response file content.

	// These fields are used internally in Blueprint
	CommandDeps      []string // Command-specific implicit dependencies to prepend to builds
	CommandOrderOnly []string // Command-specific order-only dependencies to prepend to builds
	Comment          string   // The comment that will appear above the definition.
}

// A BuildParams object contains the set of parameters that make up a Ninja
// build statement.  Each field except for Args corresponds with a part of the
// Ninja build statement.  The Args field contains variable names and values
// that are set within the build statement's scope in the Ninja file.
type BuildParams struct {
	Comment         string            // The comment that will appear above the definition.
	Depfile         string            // The dependency file name.
	Deps            Deps              // The format of the dependency file.
	Description     string            // The description that Ninja will print for the build.
	Rule            Rule              // The rule to invoke.
	Outputs         []string          // The list of explicit output targets.
	ImplicitOutputs []string          // The list of implicit output targets.
	Inputs          []string          // The list of explicit input dependencies.
	Implicits       []string          // The list of implicit input dependencies.
	OrderOnly       []string          // The list of order-only dependencies.
	Validations     []string          // The list of validations to run when this rule runs.
	Args            map[string]string // The variable/value pairs to set.
	Default         bool              // Output a ninja default statement
}

// A poolDef describes a pool definition.  It does not include the name of the
// pool.
type poolDef struct {
	Comment string
	Depth   int
}

func parsePoolParams(scope scope, params *PoolParams) (*poolDef,
	error) {

	def := &poolDef{
		Comment: params.Comment,
		Depth:   params.Depth,
	}

	return def, nil
}

func (p *poolDef) WriteTo(nw *ninjaWriter, name string) error {
	if p.Comment != "" {
		err := nw.Comment(p.Comment)
		if err != nil {
			return err
		}
	}

	err := nw.Pool(name)
	if err != nil {
		return err
	}

	return nw.ScopedAssign("depth", strconv.Itoa(p.Depth))
}

// A ruleDef describes a rule definition.  It does not include the name of the
// rule.
type ruleDef struct {
	CommandDeps      []*ninjaString
	CommandOrderOnly []*ninjaString
	Comment          string
	Pool             Pool
	Variables        map[string]*ninjaString
}

func parseRuleParams(scope scope, params *RuleParams) (*ruleDef,
	error) {

	r := &ruleDef{
		Comment:   params.Comment,
		Pool:      params.Pool,
		Variables: make(map[string]*ninjaString),
	}

	if params.Command == "" {
		return nil, fmt.Errorf("encountered rule params with no command " +
			"specified")
	}

	if r.Pool != nil && !scope.IsPoolVisible(r.Pool) {
		return nil, fmt.Errorf("Pool %s is not visible in this scope", r.Pool)
	}

	value, err := parseNinjaString(scope, params.Command)
	if err != nil {
		return nil, fmt.Errorf("error parsing Command param: %s", err)
	}
	r.Variables["command"] = value

	if params.Depfile != "" {
		value, err = parseNinjaString(scope, params.Depfile)
		if err != nil {
			return nil, fmt.Errorf("error parsing Depfile param: %s", err)
		}
		r.Variables["depfile"] = value
	}

	if params.Deps != DepsNone {
		r.Variables["deps"] = simpleNinjaString(params.Deps.String())
	}

	if params.Description != "" {
		value, err = parseNinjaString(scope, params.Description)
		if err != nil {
			return nil, fmt.Errorf("error parsing Description param: %s", err)
		}
		r.Variables["description"] = value
	}

	if params.Generator {
		r.Variables["generator"] = simpleNinjaString("true")
	}

	if params.Restat {
		r.Variables["restat"] = simpleNinjaString("true")
	}

	if params.Rspfile != "" {
		value, err = parseNinjaString(scope, params.Rspfile)
		if err != nil {
			return nil, fmt.Errorf("error parsing Rspfile param: %s", err)
		}
		r.Variables["rspfile"] = value
	}

	if params.RspfileContent != "" {
		value, err = parseNinjaString(scope, params.RspfileContent)
		if err != nil {
			return nil, fmt.Errorf("error parsing RspfileContent param: %s",
				err)
		}
		r.Variables["rspfile_content"] = value
	}

	r.CommandDeps, err = parseNinjaStrings(scope, params.CommandDeps)
	if err != nil {
		return nil, fmt.Errorf("error parsing CommandDeps param: %s", err)
	}

	r.CommandOrderOnly, err = parseNinjaStrings(scope, params.CommandOrderOnly)
	if err != nil {
		return nil, fmt.Errorf("error parsing CommandDeps param: %s", err)
	}

	return r, nil
}

func (r *ruleDef) WriteTo(nw *ninjaWriter, name string, nameTracker *nameTracker) error {

	if r.Comment != "" {
		err := nw.Comment(r.Comment)
		if err != nil {
			return err
		}
	}

	err := nw.Rule(name)
	if err != nil {
		return err
	}

	if r.Pool != nil {
		err = nw.ScopedAssign("pool", nameTracker.Pool(r.Pool))
		if err != nil {
			return err
		}
	}

	err = writeVariables(nw, r.Variables, nameTracker)
	if err != nil {
		return err
	}

	return nil
}

// A buildDef describes a build target definition.
type buildDef struct {
	Comment               string
	Rule                  Rule
	RuleDef               *ruleDef
	Outputs               []*ninjaString
	OutputStrings         []string
	ImplicitOutputs       []*ninjaString
	ImplicitOutputStrings []string
	Inputs                []*ninjaString
	InputStrings          []string
	Implicits             []*ninjaString
	ImplicitStrings       []string
	OrderOnly             []*ninjaString
	OrderOnlyStrings      []string
	Validations           []*ninjaString
	ValidationStrings     []string
	Args                  map[Variable]*ninjaString
	Variables             map[string]*ninjaString
	Default               bool
}

func formatTags(tags map[string]string, rule Rule) string {
	// Maps in golang do not have a guaranteed iteration order, nor is there an
	// ordered map type in the stdlib, but we need to deterministically generate
	// the ninja file.
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+tags[k])
	}
	pairs = append(pairs, "rule_name="+rule.name())
	return strings.Join(pairs, ";")
}

func parseBuildParams(scope scope, params *BuildParams,
	tags map[string]string) (*buildDef, error) {

	comment := params.Comment
	rule := params.Rule

	b := &buildDef{
		Comment: comment,
		Rule:    rule,
	}

	setVariable := func(name string, value *ninjaString) {
		if b.Variables == nil {
			b.Variables = make(map[string]*ninjaString)
		}
		b.Variables[name] = value
	}

	if !scope.IsRuleVisible(rule) {
		return nil, fmt.Errorf("Rule %s is not visible in this scope", rule)
	}

	if len(params.Outputs) == 0 {
		return nil, errors.New("Outputs param has no elements")
	}

	var err error
	b.Outputs, b.OutputStrings, err = parseNinjaOrSimpleStrings(scope, params.Outputs)
	if err != nil {
		return nil, fmt.Errorf("error parsing Outputs param: %s", err)
	}

	b.ImplicitOutputs, b.ImplicitOutputStrings, err = parseNinjaOrSimpleStrings(scope, params.ImplicitOutputs)
	if err != nil {
		return nil, fmt.Errorf("error parsing ImplicitOutputs param: %s", err)
	}

	b.Inputs, b.InputStrings, err = parseNinjaOrSimpleStrings(scope, params.Inputs)
	if err != nil {
		return nil, fmt.Errorf("error parsing Inputs param: %s", err)
	}

	b.Implicits, b.ImplicitStrings, err = parseNinjaOrSimpleStrings(scope, params.Implicits)
	if err != nil {
		return nil, fmt.Errorf("error parsing Implicits param: %s", err)
	}

	b.OrderOnly, b.OrderOnlyStrings, err = parseNinjaOrSimpleStrings(scope, params.OrderOnly)
	if err != nil {
		return nil, fmt.Errorf("error parsing OrderOnly param: %s", err)
	}

	b.Validations, b.ValidationStrings, err = parseNinjaOrSimpleStrings(scope, params.Validations)
	if err != nil {
		return nil, fmt.Errorf("error parsing Validations param: %s", err)
	}

	b.Default = params.Default

	if params.Depfile != "" {
		value, err := parseNinjaString(scope, params.Depfile)
		if err != nil {
			return nil, fmt.Errorf("error parsing Depfile param: %s", err)
		}
		setVariable("depfile", value)
	}

	if params.Deps != DepsNone {
		setVariable("deps", simpleNinjaString(params.Deps.String()))
	}

	if params.Description != "" {
		value, err := parseNinjaString(scope, params.Description)
		if err != nil {
			return nil, fmt.Errorf("error parsing Description param: %s", err)
		}
		setVariable("description", value)
	}

	if len(tags) > 0 {
		setVariable("tags", simpleNinjaString(formatTags(tags, rule)))
	}

	argNameScope := rule.scope()

	if len(params.Args) > 0 {
		b.Args = make(map[Variable]*ninjaString)
		for name, value := range params.Args {
			if !rule.isArg(name) {
				return nil, fmt.Errorf("unknown argument %q", name)
			}

			argVar, err := argNameScope.LookupVariable(name)
			if err != nil {
				// This shouldn't happen.
				return nil, fmt.Errorf("argument lookup error: %s", err)
			}

			ninjaValue, err := parseNinjaString(scope, value)
			if err != nil {
				return nil, fmt.Errorf("error parsing variable %q: %s", name,
					err)
			}

			b.Args[argVar] = ninjaValue
		}
	}

	return b, nil
}

func (b *buildDef) WriteTo(nw *ninjaWriter, nameTracker *nameTracker) error {
	var (
		comment             = b.Comment
		rule                = nameTracker.Rule(b.Rule)
		outputs             = b.Outputs
		implicitOuts        = b.ImplicitOutputs
		explicitDeps        = b.Inputs
		implicitDeps        = b.Implicits
		orderOnlyDeps       = b.OrderOnly
		validations         = b.Validations
		outputStrings       = b.OutputStrings
		implicitOutStrings  = b.ImplicitOutputStrings
		explicitDepStrings  = b.InputStrings
		implicitDepStrings  = b.ImplicitStrings
		orderOnlyDepStrings = b.OrderOnlyStrings
		validationStrings   = b.ValidationStrings
	)

	if b.RuleDef != nil {
		implicitDeps = append(b.RuleDef.CommandDeps, implicitDeps...)
		orderOnlyDeps = append(b.RuleDef.CommandOrderOnly, orderOnlyDeps...)
	}

	err := nw.Build(comment, rule, outputs, implicitOuts, explicitDeps, implicitDeps, orderOnlyDeps, validations,
		outputStrings, implicitOutStrings, explicitDepStrings,
		implicitDepStrings, orderOnlyDepStrings, validationStrings,
		nameTracker)
	if err != nil {
		return err
	}

	err = writeVariables(nw, b.Variables, nameTracker)
	if err != nil {
		return err
	}

	type nameValuePair struct {
		name, value string
	}

	args := make([]nameValuePair, 0, len(b.Args))

	for argVar, value := range b.Args {
		fullName := nameTracker.Variable(argVar)
		args = append(args, nameValuePair{fullName, value.Value(nameTracker)})
	}
	sort.Slice(args, func(i, j int) bool { return args[i].name < args[j].name })

	for _, pair := range args {
		err = nw.ScopedAssign(pair.name, pair.value)
		if err != nil {
			return err
		}
	}

	if b.Default {
		err = nw.Default(nameTracker, outputs, outputStrings)
		if err != nil {
			return err
		}
	}

	return nw.BlankLine()
}

func writeVariables(nw *ninjaWriter, variables map[string]*ninjaString, nameTracker *nameTracker) error {
	var keys []string
	for k := range variables {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		err := nw.ScopedAssign(name, variables[name].Value(nameTracker))
		if err != nil {
			return err
		}
	}
	return nil
}
