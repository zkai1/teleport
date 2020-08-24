package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSliceMatchesRegexp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc        string
		input       string
		expressions []string
		wantMatch   bool
		assertErr   assert.ErrorAssertionFunc
	}{
		{
			desc:        "exact match",
			input:       "foo",
			expressions: []string{`foo`},
			wantMatch:   true,
			assertErr:   assert.NoError,
		},
		{
			desc:        "exact match, multiple expressions",
			input:       "foo",
			expressions: []string{`bar`, `foo`, `baz`},
			wantMatch:   true,
			assertErr:   assert.NoError,
		},
		{
			desc:        "no match",
			input:       "foo",
			expressions: []string{`bar`},
			wantMatch:   false,
			assertErr:   assert.NoError,
		},
		{
			desc:        "wildcard match",
			input:       "foo",
			expressions: []string{`f*`},
			wantMatch:   true,
			assertErr:   assert.NoError,
		},
		{
			desc:        "wildcard no match",
			input:       "foo",
			expressions: []string{`b*`},
			wantMatch:   false,
			assertErr:   assert.NoError,
		},
		{
			desc:        "regexp match",
			input:       "foo",
			expressions: []string{`^f.*$`},
			wantMatch:   true,
			assertErr:   assert.NoError,
		},
		{
			desc:        "regexp no match",
			input:       "foo",
			expressions: []string{`^bar$`},
			wantMatch:   false,
			assertErr:   assert.NoError,
		},
		{
			desc:        "invalid regexp",
			input:       "foo",
			expressions: []string{`^?+$`},
			wantMatch:   false,
			assertErr:   assert.Error,
		},
		{
			desc:        "negated regexp match",
			input:       "foo",
			expressions: []string{`+regexp.not(bar)`},
			wantMatch:   true,
			assertErr:   assert.NoError,
		},
		{
			desc:        "negated regexp no match",
			input:       "bar",
			expressions: []string{`+regexp.not(bar)`},
			wantMatch:   false,
			assertErr:   assert.NoError,
		},
		{
			desc:        "incomplete negated regexp no match",
			input:       "foo",
			expressions: []string{`+regexp.not(bar`},
			wantMatch:   false,
			assertErr:   assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			match, err := SliceMatchesRegex(tt.input, tt.expressions)
			tt.assertErr(t, err)
			assert.Equal(t, match, tt.wantMatch)
		})
	}
}
