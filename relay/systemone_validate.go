package relay

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/systemone"
	"github.com/QuantumNous/new-api/tokenkit"
)

// Limits the upstream publishes for one System One request. They live in
// https://docs.typesafe.ai/api/errors.md and https://docs.typesafe.ai/models.md.
const (
	// systemOnePerRequestContext and systemOnePerQuestionContext are the default
	// context budgets, in tokens.
	systemOnePerRequestContext  = 64_000
	systemOnePerQuestionContext = 32_000
	// systemOneLongContextBudget is what both budgets become with the
	// TypeSafe-Danger-Zone-Long-Context header.
	systemOneLongContextBudget = 262_000
	// systemOneMaxNestingDepth is the deepest object or array the upstream reads
	// in each of state, instructions and criteria.
	systemOneMaxNestingDepth = 64
	// systemOneMaxChoiceOptions is the ceiling on the options of one choice
	// question; the upstream validates 256 as too many.
	systemOneMaxChoiceOptions = 255
	// systemOneMaxScoreLevels is the ceiling on the levels of one score question.
	systemOneMaxScoreLevels = 10
)

// Header names the System One API reads.
const (
	SystemOneLongContextHeader = "TypeSafe-Danger-Zone-Long-Context"
	SystemOneImagesHeader      = "TypeSafe-Danger-Zone-Images"
)

// systemOneQuestionTypes are the question types the upstream discriminates on.
var systemOneQuestionTypes = map[string]struct{}{
	"noul":   {},
	"choice": {},
	"score":  {},
	"box":    {},
}

// SystemOneValidationProblem is one rejected field, in the vocabulary the
// upstream uses for a "detail" array element.
type SystemOneValidationProblem = systemone.ValidationError

// HeadersEnabled reports whether a danger-zone header asks for its feature. The
// upstream documents the value "true"; a request that carries any other value
// did not turn the feature on.
func HeadersEnabled(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

// ValidateSystemOneBody checks a request body against the schema the upstream
// publishes and answers the fields it rejects. An empty result means the body
// passes every local check; the upstream still validates what it receives.
func ValidateSystemOneBody(rawBody []byte, longContext, imagesEnabled bool) []SystemOneValidationProblem {
	if len(rawBody) == 0 {
		return []SystemOneValidationProblem{
			systemone.FieldValidationError(nil, "Field required", "missing"),
		}
	}

	// Numbers that do not fit a float64 (1e400 and the like) are rejected by the
	// upstream as a request rule rather than as a schema failure, and decoding
	// them into a typed struct turns them into a decoder error that says nothing
	// about the field. They are read as json.Number first so the check happens
	// before any float conversion.
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return []SystemOneValidationProblem{
			systemone.FieldValidationError(nil, "Invalid JSON body", "json_invalid"),
		}
	}

	var problems []SystemOneValidationProblem
	problems = append(problems, validateFiniteNumbers(document, nil)...)

	model, hasModel := document["model"]
	if !hasModel || isNull(model) {
		problems = append(problems, systemone.FieldValidationError([]any{"model"}, "Field required", "missing"))
	} else if _, ok := model.(string); !ok {
		problems = append(problems, systemone.FieldValidationError([]any{"model"}, "Input should be a valid string", "string_type"))
	}

	state, hasState := document["state"]
	switch {
	case !hasState || isNull(state):
		problems = append(problems, systemone.FieldValidationError([]any{"state"}, "Field required", "missing"))
	case !isStateShape(state):
		// The upstream accepts a string, an object or an array; a number or a
		// boolean is neither.
		problems = append(problems, systemone.FieldValidationError([]any{"state"}, "Input should be a valid string, object or array", "type_error"))
	default:
		if depth := nestingDepth(state); depth > systemOneMaxNestingDepth {
			problems = append(problems, systemone.FieldValidationError([]any{"state"}, "Nesting exceeds the maximum of 64 levels", "too_deep"))
		}
	}

	questions, hasQuestions := document["questions"]
	questionNames := []string{}
	switch {
	case !hasQuestions || isNull(questions):
		problems = append(problems, systemone.FieldValidationError([]any{"questions"}, "Field required", "missing"))
	default:
		entries, ok := questions.(map[string]any)
		if !ok {
			problems = append(problems, systemone.FieldValidationError([]any{"questions"}, "Input should be a valid object", "dict_type"))
			break
		}
		if len(entries) == 0 {
			problems = append(problems, systemone.FieldValidationError([]any{"questions"}, "At least one question is required", "too_short"))
			break
		}
		names := sortedKeys(entries)
		questionNames = names
		for _, name := range names {
			problems = append(problems, validateQuestion(name, entries[name])...)
		}
	}

	assets, hasAssets := document["assets"]
	assetCount := 0
	switch {
	case hasAssets && !isNull(assets):
		entries, ok := assets.(map[string]any)
		if !ok {
			problems = append(problems, systemone.FieldValidationError([]any{"assets"}, "Input should be a valid object", "dict_type"))
			break
		}
		assetCount = len(entries)
		names := sortedKeys(entries)
		for _, name := range names {
			problems = append(problems, validateAsset(name, entries[name])...)
		}
	}

	// Images and box questions need the danger-zone header. A request that
	// carries either without it is refused rather than sent upstream to be
	// rejected there.
	if !imagesEnabled {
		if assetCount > 0 {
			problems = append(problems, systemone.FieldValidationError([]any{"assets"}, SystemOneImagesHeader+" must be true to send assets", "header_missing"))
		}
		if hasBoxQuestion(document2questions(document)) {
			problems = append(problems, systemone.FieldValidationError([]any{"questions"}, SystemOneImagesHeader+" must be true to ask a box question", "header_missing"))
		}
	}

	if len(problems) > 0 {
		return problems
	}

	// Context budgets are checked only once the shape is sound, because the
	// estimate reads the same fields the shape check rejected.
	if problems = validateContextBudget(document, questionNames, longContext); len(problems) > 0 {
		return problems
	}
	return nil
}

// document2questions returns the questions map of a document, or nil.
func document2questions(document map[string]any) map[string]any {
	questions, ok := document["questions"].(map[string]any)
	if !ok {
		return nil
	}
	return questions
}

func hasBoxQuestion(questions map[string]any) bool {
	for _, question := range questions {
		if entry, ok := question.(map[string]any); ok {
			if kind, ok := entry["type"].(string); ok && kind == "box" {
				return true
			}
		}
	}
	return false
}

func validateQuestion(name string, raw any) []SystemOneValidationProblem {
	base := []any{"questions", name}
	question, ok := raw.(map[string]any)
	if !ok {
		return []SystemOneValidationProblem{
			systemone.FieldValidationError(base, "Input should be a valid object", "dict_type"),
		}
	}

	kind, hasKind := question["type"]
	if !hasKind || isNull(kind) {
		return []SystemOneValidationProblem{
			systemone.FieldValidationError(append(base, "type"), "Field required", "missing"),
		}
	}
	kindName, ok := kind.(string)
	if !ok {
		return []SystemOneValidationProblem{
			systemone.FieldValidationError(append(base, "type"), "Input should be a valid string", "string_type"),
		}
	}
	if _, known := systemOneQuestionTypes[kindName]; !known {
		return []SystemOneValidationProblem{
			systemone.FieldValidationError(append(base, "type"), "Input should be 'noul', 'choice', 'score' or 'box'", "literal_error"),
		}
	}

	var problems []SystemOneValidationProblem
	criteria, hasCriteria := question["criteria"]
	instructions, hasInstructions := question["instructions"]
	hasUsableInstructions := hasInstructions && !isNull(instructions)

	switch kindName {
	case "choice":
		if !hasCriteria || isNull(criteria) {
			problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "Field required", "missing"))
			break
		}
		options, ok := criteria.(map[string]any)
		if !ok {
			problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "Input should be a valid object", "dict_type"))
			break
		}
		if len(options) == 0 {
			problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "At least one choice is required", "too_short"))
		}
		if len(options) > systemOneMaxChoiceOptions {
			problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "At most "+strconv.Itoa(systemOneMaxChoiceOptions)+" choices are allowed", "too_long"))
		}
	case "score":
		if !hasCriteria || isNull(criteria) {
			problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "Field required", "missing"))
			break
		}
		levels, ok := criteria.([]any)
		if !ok {
			problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "Input should be a valid array", "list_type"))
			break
		}
		if len(levels) == 0 {
			problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "At least one level is required", "too_short"))
		}
		if len(levels) > systemOneMaxScoreLevels {
			problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "At most "+strconv.Itoa(systemOneMaxScoreLevels)+" levels are allowed", "too_long"))
		}
	case "noul":
		if hasCriteria {
			if _, ok := criteria.(map[string]any); !ok && !isNull(criteria) {
				problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "Input should be a valid object", "dict_type"))
			}
		}
		if (!hasCriteria || isNull(criteria)) && !hasUsableInstructions {
			problems = append(problems, systemone.FieldValidationError(base, "Noul question must have criteria or instructions", "value_error"))
		}
	case "box":
		if !hasUsableInstructions {
			problems = append(problems, systemone.FieldValidationError(append(base, "instructions"), "Field required", "missing"))
		}
	}

	if depth := nestingDepth(instructions); depth > systemOneMaxNestingDepth {
		problems = append(problems, systemone.FieldValidationError(append(base, "instructions"), "Nesting exceeds the maximum of 64 levels", "too_deep"))
	}
	if depth := nestingDepth(criteria); depth > systemOneMaxNestingDepth {
		problems = append(problems, systemone.FieldValidationError(append(base, "criteria"), "Nesting exceeds the maximum of 64 levels", "too_deep"))
	}
	return problems
}

// validateAsset checks one entry of the top-level assets map against the Asset
// schema: an image, a declared format with its inline data, or the name of a
// multipart part.
func validateAsset(name string, raw any) []SystemOneValidationProblem {
	base := []any{"assets", name}
	asset, ok := raw.(map[string]any)
	if !ok {
		return []SystemOneValidationProblem{
			systemone.FieldValidationError(base, "Input should be a valid object", "dict_type"),
		}
	}

	var problems []SystemOneValidationProblem
	if kind, exists := asset["type"]; exists && !isNull(kind) {
		value, ok := kind.(string)
		if !ok || value != "image" {
			problems = append(problems, systemone.FieldValidationError(append(base, "type"), "Input should be 'image'", "literal_error"))
		}
	}

	format, hasFormat := asset["format"]
	dataBase64, hasData := asset["data_base64"]
	part, hasPart := asset["part"]
	hasUsableData := hasData && !isNull(dataBase64)
	hasUsablePart := hasPart && !isNull(part)

	if hasUsableData {
		switch {
		case !hasFormat || isNull(format):
			problems = append(problems, systemone.FieldValidationError(append(base, "format"), "Field required", "missing"))
		default:
			value, ok := format.(string)
			switch {
			case !ok:
				problems = append(problems, systemone.FieldValidationError(append(base, "format"), "Input should be a valid string", "string_type"))
			case value != "png" && value != "jpg" && value != "jpeg":
				problems = append(problems, systemone.FieldValidationError(append(base, "format"), "Input should be 'png', 'jpg' or 'jpeg'", "literal_error"))
			}
		}
	} else if !hasUsablePart {
		problems = append(problems, systemone.FieldValidationError(base, "An asset needs data_base64 or part", "value_error"))
	}
	return problems
}

// validateFiniteNumbers walks the document and rejects a number the upstream
// refuses: text that names infinity or NaN, or a magnitude too large for a
// float64 such as 1e400. json.Number keeps the text, so the check runs before
// anything converts it.
func validateFiniteNumbers(value any, path []any) []SystemOneValidationProblem {
	switch typed := value.(type) {
	case json.Number:
		if _, err := strconv.ParseFloat(typed.String(), 64); err != nil {
			return []SystemOneValidationProblem{
				systemone.FieldValidationError(path, "Input should be a finite number", "finite_number"),
			}
		}
	case map[string]any:
		var problems []SystemOneValidationProblem
		for _, key := range sortedKeys(typed) {
			problems = append(problems, validateFiniteNumbers(typed[key], append(path, key))...)
		}
		return problems
	case []any:
		var problems []SystemOneValidationProblem
		for index, item := range typed {
			problems = append(problems, validateFiniteNumbers(item, append(path, index))...)
		}
		return problems
	}
	return nil
}

// validateContextBudget estimates the two context budgets the upstream applies:
// state plus every question for the per-request budget, and state plus one
// question for the per-question budget. The estimate is a token count, so it
// refuses only what is clearly past the ceiling.
func validateContextBudget(document map[string]any, questionNames []string, longContext bool) []SystemOneValidationProblem {
	perRequest := systemOnePerRequestContext
	perQuestion := systemOnePerQuestionContext
	if longContext {
		perRequest = systemOneLongContextBudget
		perQuestion = systemOneLongContextBudget
	}

	stateTokens := estimateNodeTokens(document["state"])
	questions, _ := document["questions"].(map[string]any)

	total := stateTokens
	widestQuestion := 0
	for _, name := range questionNames {
		tokens := estimateNodeTokens(questions[name])
		total += tokens
		if tokens > widestQuestion {
			widestQuestion = tokens
		}
	}

	if total > perRequest {
		return []SystemOneValidationProblem{
			systemone.FieldValidationError(nil, "Request is longer than the model's context length (max_tokens_exceeded)", "max_tokens_exceeded"),
		}
	}
	if stateTokens+widestQuestion > perQuestion {
		return []SystemOneValidationProblem{
			systemone.FieldValidationError([]any{"questions"}, "State plus one question is longer than the per-question context length (max_tokens_exceeded)", "max_tokens_exceeded"),
		}
	}
	return nil
}

// estimateNodeTokens counts the tokens of a JSON value by reading its own text.
// The upstream bills the request as it receives it, so the textual size of the
// field is the closest local estimate, and tokenkit needs a model name it does
// not have here: the unknown family weights are what an unrecognised model is
// measured with.
func estimateNodeTokens(value any) int {
	if value == nil {
		return 0
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return tokenkit.EstimateFamily(tokenkit.FamilyUnknown, string(encoded))
}

// nestingDepth answers how many object and array levels a value holds. Scalars
// are depth zero.
func nestingDepth(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		deepest := 0
		for _, item := range typed {
			if depth := nestingDepth(item); depth > deepest {
				deepest = depth
			}
		}
		return deepest + 1
	case []any:
		deepest := 0
		for _, item := range typed {
			if depth := nestingDepth(item); depth > deepest {
				deepest = depth
			}
		}
		return deepest + 1
	default:
		return 0
	}
}

func isStateShape(value any) bool {
	switch value.(type) {
	case string, map[string]any, []any:
		return true
	default:
		return false
	}
}

func isNull(value any) bool {
	return value == nil
}

func sortedKeys(entries map[string]any) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
