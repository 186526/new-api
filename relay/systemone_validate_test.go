package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cases below are the rejections listed in the System One compatibility
// report, each one an input the upstream answers 422 for. The local validator
// refuses them before the request costs an upstream round trip.
func TestValidateSystemOneBodyRejections(t *testing.T) {
	validNoul := `"state":"x","questions":{"q":{"type":"noul","instructions":"y"}}`

	cases := []struct {
		name  string
		body  string
		field []any
	}{
		{
			name:  "missing model",
			body:  `{` + validNoul + `}`,
			field: []any{"body", "model"},
		},
		{
			name:  "missing state",
			body:  `{"model":"jev-latest","questions":{"q":{"type":"noul","instructions":"y"}}}`,
			field: []any{"body", "state"},
		},
		{
			name:  "state is a number",
			body:  `{"model":"jev-latest","state":42,"questions":{"q":{"type":"noul","instructions":"y"}}}`,
			field: []any{"body", "state"},
		},
		{
			name:  "state is null",
			body:  `{"model":"jev-latest","state":null,"questions":{"q":{"type":"noul","instructions":"y"}}}`,
			field: []any{"body", "state"},
		},
		{
			name:  "missing questions",
			body:  `{"model":"jev-latest","state":"x"}`,
			field: []any{"body", "questions"},
		},
		{
			name:  "empty questions",
			body:  `{"model":"jev-latest","state":"x","questions":{}}`,
			field: []any{"body", "questions"},
		},
		{
			name:  "choice without criteria",
			body:  `{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","instructions":"pick"}}}`,
			field: []any{"body", "questions", "q", "criteria"},
		},
		{
			name:  "choice with empty criteria",
			body:  `{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","criteria":{}}}}`,
			field: []any{"body", "questions", "q", "criteria"},
		},
		{
			name:  "score criteria is an object",
			body:  `{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","criteria":{}}}}`,
			field: []any{"body", "questions", "q", "criteria"},
		},
		{
			name:  "noul without criteria or instructions",
			body:  `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul"}}}`,
			field: []any{"body", "questions", "q"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			problems := ValidateSystemOneBody([]byte(testCase.body), false, false)
			require.NotEmpty(t, problems)
			found := false
			for _, problem := range problems {
				if assert.ObjectsAreEqual(testCase.field, problem.Loc) {
					found = true
				}
			}
			assert.True(t, found, "expected a problem at %v, got %+v", testCase.field, problems)
		})
	}
}

func TestValidateSystemOneBodyRejectsTooManyChoicesAndLevels(t *testing.T) {
	options := `{`
	for index := 0; index < 256; index++ {
		if index > 0 {
			options += ","
		}
		options += `"option` + itoa(index) + `":"label"`
	}
	options += `}`
	body := `{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","criteria":` + options + `}}}`
	problems := ValidateSystemOneBody([]byte(body), false, false)
	require.NotEmpty(t, problems)
	assert.Equal(t, []any{"body", "questions", "q", "criteria"}, problems[0].Loc)

	levels := `[`
	for index := 0; index < 11; index++ {
		if index > 0 {
			levels += ","
		}
		levels += `"level"`
	}
	levels += `]`
	body = `{"model":"jev-latest","state":"x","questions":{"q":{"type":"score","criteria":` + levels + `}}}`
	problems = ValidateSystemOneBody([]byte(body), false, false)
	require.NotEmpty(t, problems)
	assert.Equal(t, []any{"body", "questions", "q", "criteria"}, problems[0].Loc)
}

func TestValidateSystemOneBodyRejectsNonFiniteNumber(t *testing.T) {
	body := `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":1e400}}}`
	problems := ValidateSystemOneBody([]byte(body), false, false)
	require.NotEmpty(t, problems)
	assert.Equal(t, "finite_number", problems[0].Type)
}

func TestValidateSystemOneBodyRequiresTheImagesHeader(t *testing.T) {
	body := `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"y"}},` +
		`"assets":{"photo":{"type":"image","format":"png","data_base64":"AAAA"}}}`
	problems := ValidateSystemOneBody([]byte(body), false, false)
	require.NotEmpty(t, problems)
	assert.Equal(t, []any{"body", "assets"}, problems[0].Loc)

	assert.Empty(t, ValidateSystemOneBody([]byte(body), false, true))
}

func TestValidateSystemOneBodyAcceptsAValidRequest(t *testing.T) {
	body := `{"model":"jev-latest","state":{"text":"x"},"questions":{` +
		`"a":{"type":"noul","criteria":{"yes":"ok"}},` +
		`"b":{"type":"choice","criteria":{"first":"one","second":"two"}},` +
		`"c":{"type":"score","criteria":[{"label":"low"},{"label":"high"}]}}}`
	assert.Empty(t, ValidateSystemOneBody([]byte(body), false, false))
}

// itoa keeps the option-number formatting local to the test.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
