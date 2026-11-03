package schema

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lighthousenz/scraps/internal/config"
	"github.com/lighthousenz/scraps/internal/model"
)

// M is a shorthand for an ordered JSON object. We use a slice of kv pairs
// because Go maps don't preserve insertion order.
type M = orderedMap

type kv struct {
	key   string
	value any
}

type orderedMap struct {
	items []kv
}

func obj(items ...kv) M {
	return M{items: items}
}

func prop(key string, value any) kv {
	return kv{key: key, value: value}
}

func (m M) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, item := range m.items {
		if i > 0 {
			buf = append(buf, ',')
		}
		kb, err := json.Marshal(item.key)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(item.value)
		if err != nil {
			return nil, err
		}
		buf = append(buf, kb...)
		buf = append(buf, ':')
		buf = append(buf, vb...)
	}
	buf = append(buf, '}')
	return buf, nil
}

// Generate produces a JSON Schema 2020-12 document for scenario.yaml files.
func Generate(lp *config.LoadedProject) ([]byte, error) {
	schema := buildSchema(lp)
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling schema: %w", err)
	}
	data = append(data, '\n')
	return data, nil
}

func buildSchema(lp *config.LoadedProject) M {
	return obj(
		prop("$schema", "https://json-schema.org/draft/2020-12/schema"),
		prop("$id", ".scraps/schema.json"),
		prop("title", "Scraps scenario"),
		prop("description", "Schema for scenario.yaml files, generated from the project's scraps.yaml and actor definitions."),
		prop("type", "object"),
		prop("required", []string{"id", "title", "actors", "steps", "outcomes"}),
		prop("additionalProperties", false),
		prop("properties", buildTopLevelProperties(lp)),
	)
}

func buildTopLevelProperties(lp *config.LoadedProject) M {
	props := []kv{
		prop("id", obj(
			prop("type", "string"),
			prop("description", "Unique scenario identifier."),
		)),
		prop("title", obj(
			prop("type", "string"),
			prop("description", "Human-readable title describing the scenario."),
		)),
		prop("symptom", obj(
			prop("type", "string"),
			prop("description", "Longer description of the symptom being tested."),
		)),
		prop("links", buildLinksSchema(lp)),
		prop("tags", obj(
			prop("type", "array"),
			prop("items", obj(prop("type", "string"))),
			prop("description", "Tags for filtering scenarios."),
		)),
		prop("fixtures", obj(
			prop("type", "object"),
			prop("description", "Named fixture directories."),
			prop("additionalProperties", obj(prop("type", "string"))),
		)),
		prop("actors", buildActorsSchema(lp)),
		prop("steps", buildStepsSchema(lp)),
		prop("outcomes", buildOutcomesSchema(lp)),
		prop("history", buildHistorySchema(lp)),
	}
	return M{items: props}
}

func buildLinksSchema(lp *config.LoadedProject) M {
	if len(lp.Project.Links) == 0 {
		return obj(
			prop("type", "object"),
			prop("description", "Scenario links."),
			prop("additionalProperties", obj(prop("type", "string"))),
		)
	}

	linkProps := make([]kv, 0, len(lp.Project.Links))
	names := sortedKeys(lp.Project.Links)
	for _, name := range names {
		link := lp.Project.Links[name]
		desc := fmt.Sprintf("Link value matching pattern: %s", link.Pattern)
		linkProps = append(linkProps, prop(name, obj(
			prop("type", "string"),
			prop("pattern", link.Pattern),
			prop("description", desc),
		)))
	}

	return obj(
		prop("type", "object"),
		prop("description", "Scenario links."),
		prop("properties", M{items: linkProps}),
		prop("additionalProperties", false),
	)
}

func buildActorsSchema(lp *config.LoadedProject) M {
	actorNames := sortedMapKeys(lp.Project.Actors)

	// Collect all inputs across all actor definitions.
	allInputs := collectAllInputs(lp)

	actorProps := []kv{
		prop("uses", obj(
			prop("type", "string"),
			prop("enum", actorNames),
			prop("description", "Which actor definition this participant uses."),
		)),
		prop("with", buildWithSchema(allInputs)),
		prop("start", obj(
			prop("type", "string"),
			prop("enum", []string{"auto", "on-demand"}),
			prop("description", "When to start the actor: auto (default) or on-demand."),
		)),
	}

	return obj(
		prop("type", "object"),
		prop("description", "Actors participating in this scenario."),
		prop("additionalProperties", obj(
			prop("type", "object"),
			prop("required", []string{"uses"}),
			prop("additionalProperties", false),
			prop("properties", M{items: actorProps}),
		)),
	)
}

func buildWithSchema(allInputs map[string][]inputInfo) M {
	if len(allInputs) == 0 {
		return obj(
			prop("type", "object"),
			prop("description", "Inputs for the actor."),
			prop("additionalProperties", true),
		)
	}

	inputProps := make([]kv, 0, len(allInputs))
	names := make([]string, 0, len(allInputs))
	for name := range allInputs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		infos := allInputs[name]
		// If all infos agree on the schema, use one; otherwise use anyOf.
		schemas := deduplicateInputSchemas(infos)
		var schema any
		if len(schemas) == 1 {
			schema = schemas[0]
		} else {
			schema = obj(prop("anyOf", schemas))
		}
		inputProps = append(inputProps, prop(name, schema))
	}

	return obj(
		prop("type", "object"),
		prop("description", "Inputs for the actor. The union of all actor definitions' inputs."),
		prop("properties", M{items: inputProps}),
		prop("additionalProperties", true),
	)
}

type inputInfo struct {
	def        model.InputDef
	actorName  string
	inputName  string
	schemaJSON string // for dedup
}

func collectAllInputs(lp *config.LoadedProject) map[string][]inputInfo {
	result := make(map[string][]inputInfo)
	for name, actor := range lp.Actors {
		for inputName, input := range actor.Inputs {
			schema := inputDefToSchema(input)
			schemaBytes, _ := json.Marshal(schema)
			result[inputName] = append(result[inputName], inputInfo{
				def:        input,
				actorName:  name,
				inputName:  inputName,
				schemaJSON: string(schemaBytes),
			})
		}
	}
	return result
}

func deduplicateInputSchemas(infos []inputInfo) []any {
	seen := make(map[string]bool)
	var result []any
	for _, info := range infos {
		if !seen[info.schemaJSON] {
			seen[info.schemaJSON] = true
			result = append(result, inputDefToSchema(info.def))
		}
	}
	return result
}

func inputDefToSchema(input model.InputDef) M {
	items := make([]kv, 0, 3)
	if input.Type == "actor" {
		items = append(items, prop("type", "string"))
		if input.Kind != "" {
			items = append(items, prop("description", fmt.Sprintf("Actor reference (kind: %s).", input.Kind)))
		} else {
			items = append(items, prop("description", "Actor reference."))
		}
	} else {
		items = append(items, prop("type", mapType(input.Type)))
		if input.Default != nil {
			items = append(items, prop("default", input.Default))
		}
	}
	return M{items: items}
}

func buildStepsSchema(lp *config.LoadedProject) M {
	// Collect all operation names across all actors, plus built-ins.
	allOps := collectAllOperations(lp)
	allConditions := collectAllConditions(lp)

	// Build the step value schema: either a string (operation name) or an object.
	opEnum := append([]string{"start", "stop"}, allOps...)

	// Build operation object schemas.
	opSchemas := buildOperationObjectSchemas(lp, allConditions)

	// Step value: string enum OR an operation object.
	stepValueChoices := []any{
		obj(prop("type", "string"), prop("enum", opEnum)),
	}
	stepValueChoices = append(stepValueChoices, opSchemas...)

	stepValue := obj(prop("anyOf", stepValueChoices))

	return obj(
		prop("type", "array"),
		prop("description", "The timeline of steps."),
		prop("items", obj(
			prop("type", "object"),
			prop("minProperties", 1),
			prop("maxProperties", 1),
			prop("additionalProperties", stepValue),
		)),
	)
}

func collectAllOperations(lp *config.LoadedProject) []string {
	seen := make(map[string]bool)
	var ops []string
	for _, actor := range lp.Actors {
		for name := range actor.Operations {
			if !seen[name] {
				seen[name] = true
				ops = append(ops, name)
			}
		}
	}
	sort.Strings(ops)
	return ops
}

func collectAllConditions(lp *config.LoadedProject) []string {
	seen := make(map[string]bool)
	var conds []string
	for _, actor := range lp.Actors {
		for name := range actor.Conditions {
			if !seen[name] {
				seen[name] = true
				conds = append(conds, name)
			}
		}
	}
	sort.Strings(conds)
	return conds
}

func buildOperationObjectSchemas(lp *config.LoadedProject, conditions []string) []any {
	opMap := make(map[string][]M)

	for _, actor := range lp.Actors {
		for opName, opDef := range actor.Operations {
			schema := operationParamsToSchema(opDef)
			opMap[opName] = append(opMap[opName], schema)
		}
	}

	var schemas []any

	// await condition
	if len(conditions) > 0 {
		schemas = append(schemas, obj(
			prop("type", "object"),
			prop("properties", obj(
				prop("await", obj(
					prop("type", "string"),
					prop("enum", conditions),
					prop("description", "Wait for a condition to hold."),
				)),
			)),
			prop("required", []string{"await"}),
			prop("additionalProperties", false),
		))
	}

	// Each operation as object property.
	opNames := make([]string, 0, len(opMap))
	for name := range opMap {
		opNames = append(opNames, name)
	}
	sort.Strings(opNames)

	for _, name := range opNames {
		paramSchemas := opMap[name]
		deduped := deduplicateSchemas(paramSchemas)

		var paramsSchema any
		if len(deduped) == 1 {
			paramsSchema = deduped[0]
		} else {
			paramsSchema = obj(prop("anyOf", schemasToAny(deduped)))
		}

		schemas = append(schemas, obj(
			prop("type", "object"),
			prop("properties", obj(
				prop(name, paramsSchema),
			)),
			prop("required", []string{name}),
			prop("additionalProperties", false),
		))
	}

	return schemas
}

func operationParamsToSchema(op model.OperationDef) M {
	if len(op.Params) == 0 {
		return obj(
			prop("type", "object"),
			prop("additionalProperties", false),
		)
	}

	paramProps := make([]kv, 0, len(op.Params))
	names := make([]string, 0, len(op.Params))
	for name := range op.Params {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		paramSchema := op.Params[name]
		paramProps = append(paramProps, prop(name, paramValueToSchema(paramSchema)))
	}

	return obj(
		prop("type", "object"),
		prop("properties", M{items: paramProps}),
		prop("additionalProperties", false),
	)
}

func paramValueToSchema(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return convertJSONSchemaMap(val)
	default:
		return obj(prop("description", fmt.Sprintf("%v", v)))
	}
}

func convertJSONSchemaMap(m map[string]any) M {
	items := make([]kv, 0, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := m[k]
		switch vv := v.(type) {
		case map[string]any:
			items = append(items, prop(k, convertJSONSchemaMap(vv)))
		default:
			items = append(items, prop(k, vv))
		}
	}
	return M{items: items}
}

func buildOutcomesSchema(lp *config.LoadedProject) M {
	// Build check schemas: built-in checks + checker operations.
	checkSchemas := buildCheckSchemas(lp)

	return obj(
		prop("type", "object"),
		prop("description", "Named outcomes: sets of checks against artefacts."),
		prop("additionalProperties", obj(
			prop("anyOf", []any{
				// Object form: artefact checks.
				obj(
					prop("type", "object"),
					prop("additionalProperties", checkSchemas),
				),
				// step-error form.
				obj(
					prop("type", "object"),
					prop("properties", obj(
						prop("step-error", buildStepErrorSchema()),
					)),
					prop("required", []string{"step-error"}),
					prop("additionalProperties", false),
				),
			}),
		)),
	)
}

func buildCheckSchemas(lp *config.LoadedProject) M {
	oneOfSchemas := []any{
		// file-exists
		obj(
			prop("type", "object"),
			prop("properties", obj(
				prop("file-exists", obj(
					prop("type", "string"),
					prop("description", "Check that a file exists in the artefact directory."),
				)),
			)),
			prop("additionalProperties", false),
		),
		// json check
		obj(
			prop("type", "object"),
			prop("properties", obj(
				prop("json", obj(
					prop("type", "object"),
					prop("description", "Check a JSON value at a path."),
					prop("properties", obj(
						prop("file", obj(prop("type", "string"))),
						prop("path", obj(prop("type", "string"))),
						prop("equals", obj()),
					)),
					prop("required", []string{"path", "equals"}),
					prop("additionalProperties", false),
				)),
			)),
			prop("additionalProperties", false),
		),
		// text check
		obj(
			prop("type", "object"),
			prop("properties", obj(
				prop("text", obj(
					prop("type", "object"),
					prop("description", "Check file text content with a regex."),
					prop("properties", obj(
						prop("file", obj(prop("type", "string"))),
						prop("matches", obj(prop("type", "string"))),
					)),
					prop("required", []string{"file", "matches"}),
					prop("additionalProperties", false),
				)),
			)),
			prop("additionalProperties", false),
		),
	}

	// Add checker operations.
	for _, checker := range sortedCheckerDefs(lp) {
		for _, opName := range sortedMapKeysFromOperations(checker.Operations) {
			opDef := checker.Operations[opName]
			paramSchema := checkerOperationSchema(opName, opDef)
			oneOfSchemas = append(oneOfSchemas, paramSchema)
		}
	}

	return obj(prop("anyOf", oneOfSchemas))
}

func checkerOperationSchema(name string, op model.OperationDef) M {
	paramSchema := operationParamsToSchema(op)

	items := []kv{
		prop(name, paramSchema),
	}

	// A check whose operation has exactly one param also accepts that param's schema directly.
	if len(op.Params) == 1 {
		for _, v := range op.Params {
			return obj(
				prop("type", "object"),
				prop("properties", obj(
					prop(name, obj(
						prop("anyOf", []any{
							paramSchema,
							paramValueToSchema(v),
						}),
					)),
				)),
				prop("additionalProperties", false),
			)
		}
	}

	return obj(
		prop("type", "object"),
		prop("properties", M{items: items}),
		prop("additionalProperties", false),
	)
}

func buildStepErrorSchema() M {
	return obj(
		prop("type", "object"),
		prop("description", "Match a step failure."),
		prop("properties", obj(
			prop("actor", obj(prop("type", "string"))),
			prop("operation", obj(prop("type", "string"))),
			prop("message", obj(
				prop("anyOf", []any{
					obj(prop("type", "string")),
					obj(
						prop("type", "object"),
						prop("properties", obj(
							prop("matches", obj(prop("type", "string"))),
						)),
						prop("required", []string{"matches"}),
						prop("additionalProperties", false),
					),
				}),
			)),
		)),
		prop("additionalProperties", false),
	)
}

func buildHistorySchema(lp *config.LoadedProject) M {
	// Build when schema: one property per declared subject.
	subjectProps := make([]kv, 0, len(lp.Project.Subjects))
	subjectNames := sortedKeys(lp.Project.Subjects)
	for _, name := range subjectNames {
		subjectProps = append(subjectProps, prop(name, obj(
			prop("type", "string"),
			prop("description", fmt.Sprintf("Version constraint for subject %q.", name)),
		)))
	}

	whenSchema := obj(
		prop("type", "object"),
		prop("description", "Version conditions that must all match."),
		prop("properties", M{items: subjectProps}),
		prop("additionalProperties", false),
	)

	return obj(
		prop("type", "array"),
		prop("description", "Version-conditioned expected outcomes, checked in order."),
		prop("items", obj(
			prop("type", "object"),
			prop("required", []string{"when", "expect"}),
			prop("additionalProperties", false),
			prop("properties", obj(
				prop("when", whenSchema),
				prop("expect", obj(
					prop("type", "string"),
					prop("description", "The outcome expected when all conditions in 'when' match."),
				)),
			)),
		)),
	)
}

// Helper functions.

func mapType(t string) string {
	switch t {
	case "string":
		return "string"
	case "integer":
		return "integer"
	case "boolean":
		return "boolean"
	case "number":
		return "number"
	case "array":
		return "array"
	case "object":
		return "object"
	default:
		return "string"
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeysFromOperations(m map[string]model.OperationDef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedCheckerDefs(lp *config.LoadedProject) []*model.ActorDefinition {
	names := make([]string, 0, len(lp.Checkers))
	for name := range lp.Checkers {
		names = append(names, name)
	}
	sort.Strings(names)
	var result []*model.ActorDefinition
	for _, name := range names {
		result = append(result, lp.Checkers[name])
	}
	return result
}

func deduplicateSchemas(schemas []M) []M {
	seen := make(map[string]bool)
	var result []M
	for _, s := range schemas {
		b, _ := json.Marshal(s)
		key := string(b)
		if !seen[key] {
			seen[key] = true
			result = append(result, s)
		}
	}
	return result
}

func schemasToAny(schemas []M) []any {
	result := make([]any, len(schemas))
	for i, s := range schemas {
		result[i] = s
	}
	return result
}
