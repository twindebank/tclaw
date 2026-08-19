package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractImage(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{
			name:     "box-drawing table row",
			output:   "App\n Name     │ tclaw\n Image    │ tclaw:deployment-01M0DTD8KZ4NNQWJBFMYGM3BEM \n",
			expected: "deployment-01M0DTD8KZ4NNQWJBFMYGM3BEM",
		},
		{
			name:     "equals separator",
			output:   "Image    = tclaw:deployment-01KN73ZWZPWBPJ00C7RWT03ZW4\n",
			expected: "deployment-01KN73ZWZPWBPJ00C7RWT03ZW4",
		},
		{
			name:     "no image line",
			output:   "App\n Name │ tclaw\n Owner │ personal\n",
			expected: "",
		},
		{
			name:     "image line without a tag",
			output:   " Image │ tclaw\n",
			expected: "",
		},
		{
			name:     "empty output",
			output:   "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, extractImage(tt.output))
		})
	}
}
