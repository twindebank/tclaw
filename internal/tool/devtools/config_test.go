package devtools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckProtectedSections(t *testing.T) {
	const base = `prod:
  credential_slots:
    - type: git
      label: default
  users:
    - id: theo
      tool_groups: [core_tools]
      repos:
        - name: ha-config
          repo: owner/ha-config
          access: read_only
      channels:
        - name: admin
          description: admin channel
`

	t.Run("allows a change outside the protected sections", func(t *testing.T) {
		proposed := `prod:
  credential_slots:
    - type: git
      label: default
  users:
    - id: theo
      tool_groups: [core_tools]
      repos:
        - name: ha-config
          repo: owner/ha-config
          access: read_only
      channels:
        - name: admin
          description: renamed description
`
		require.NoError(t, checkProtectedSections([]byte(base), []byte(proposed)))
	})

	t.Run("ignores reformatting and key order", func(t *testing.T) {
		proposed := `prod:
  users:
    - id: theo
      channels:
        - description: admin channel
          name: admin
      repos:
        - access: read_only
          name: ha-config
          repo: owner/ha-config
      tool_groups: [core_tools]
  credential_slots:
    - label: default
      type: git
`
		require.NoError(t, checkProtectedSections([]byte(base), []byte(proposed)))
	})

	t.Run("refuses a self-granted repo access change", func(t *testing.T) {
		proposed := `prod:
  credential_slots:
    - type: git
      label: default
  users:
    - id: theo
      tool_groups: [core_tools]
      repos:
        - name: ha-config
          repo: owner/ha-config
          access: full_write
      channels:
        - name: admin
          description: admin channel
`
		err := checkProtectedSections([]byte(base), []byte(proposed))
		require.Error(t, err)
		require.Contains(t, err.Error(), "repos")
		require.Contains(t, err.Error(), "repo_request_access")
	})

	t.Run("refuses widened tool groups", func(t *testing.T) {
		proposed := `prod:
  credential_slots:
    - type: git
      label: default
  users:
    - id: theo
      tool_groups: [all_tools]
      repos:
        - name: ha-config
          repo: owner/ha-config
          access: read_only
      channels:
        - name: admin
          description: admin channel
`
		err := checkProtectedSections([]byte(base), []byte(proposed))
		require.Error(t, err)
		require.Contains(t, err.Error(), "tool_groups")
	})

	t.Run("refuses a new credential slot", func(t *testing.T) {
		proposed := `prod:
  credential_slots:
    - type: git
      label: default
    - type: git
      label: sneaky
  users:
    - id: theo
      tool_groups: [core_tools]
      repos:
        - name: ha-config
          repo: owner/ha-config
          access: read_only
      channels:
        - name: admin
          description: admin channel
`
		err := checkProtectedSections([]byte(base), []byte(proposed))
		require.Error(t, err)
		require.Contains(t, err.Error(), "credential_slots")
	})

	t.Run("refuses an added or removed user", func(t *testing.T) {
		proposed := base + `    - id: someone-else
      tool_groups: [all_tools]
      channels:
        - name: theirs
          description: theirs
`
		err := checkProtectedSections([]byte(base), []byte(proposed))
		require.Error(t, err)
		require.Contains(t, err.Error(), "users")
	})

	t.Run("refuses a whole environment being dropped", func(t *testing.T) {
		// Removing an environment would otherwise take its permissions with it
		// unexamined.
		err := checkProtectedSections([]byte(base), []byte("local:\n  users: []\n"))
		require.Error(t, err)
	})

	t.Run("refuses config it cannot parse", func(t *testing.T) {
		err := checkProtectedSections([]byte(base), []byte("prod: [this is not a mapping]"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse proposed config")
	})
}
