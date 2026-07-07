package google

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildModifyBody(t *testing.T) {
	t.Run("add labels only", func(t *testing.T) {
		body := buildModifyBody([]string{"Label_36"}, nil)
		require.Equal(t, map[string]any{"addLabelIds": []string{"Label_36"}}, body)
	})

	t.Run("remove labels only", func(t *testing.T) {
		body := buildModifyBody(nil, []string{"UNREAD"})
		require.Equal(t, map[string]any{"removeLabelIds": []string{"UNREAD"}}, body)
	})

	t.Run("add and remove together", func(t *testing.T) {
		body := buildModifyBody([]string{"Label_36"}, []string{"UNREAD"})
		require.Equal(t, map[string]any{
			"addLabelIds":    []string{"Label_36"},
			"removeLabelIds": []string{"UNREAD"},
		}, body)
	})

	t.Run("both empty produces empty body", func(t *testing.T) {
		body := buildModifyBody(nil, nil)
		require.Empty(t, body)
	})
}
