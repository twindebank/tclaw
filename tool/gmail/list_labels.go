package gmail

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
)

func listLabelsHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		svc, err := gmailClient(ctx, deps)
		if err != nil {
			return nil, err
		}

		rsp, err := svc.Users.Labels.List("me").Do()
		if err != nil {
			return nil, fmt.Errorf("gmail list labels: %w", err)
		}

		type labelInfo struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}

		var labels []labelInfo
		for _, l := range rsp.Labels {
			labels = append(labels, labelInfo{
				ID:   l.Id,
				Name: l.Name,
				Type: l.Type,
			})
		}

		return json.Marshal(labels)
	}
}
