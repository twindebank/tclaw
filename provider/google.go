package provider

import "strings"

const GoogleProviderID ProviderID = "google"

// googleScopeServices maps Google OAuth scope path segments to human-readable
// service names. The key is matched against the last path segment of the scope
// URL (e.g. "gmail.modify" matches "gmail").
var googleScopeServices = map[string]string{
	"gmail":         "Gmail",
	"drive":         "Google Drive",
	"calendar":      "Google Calendar",
	"documents":     "Google Docs",
	"spreadsheets":  "Google Sheets",
	"presentations": "Google Slides",
	"tasks":         "Google Tasks",
}

// NewGoogleProvider creates the Google Workspace provider definition.
// Services are derived automatically from the OAuth scopes.
// OAuth2 config is populated from the tclaw config file.
func NewGoogleProvider(clientID, clientSecret string) *Provider {
	scopes := []string{
		"https://www.googleapis.com/auth/gmail.modify",
		"https://www.googleapis.com/auth/drive",
		"https://www.googleapis.com/auth/calendar",
		"https://www.googleapis.com/auth/documents",
		"https://www.googleapis.com/auth/spreadsheets",
		"https://www.googleapis.com/auth/presentations",
		"https://www.googleapis.com/auth/tasks",
	}

	return &Provider{
		ID:       GoogleProviderID,
		Name:     "Google Workspace",
		Auth:     AuthOAuth2,
		Services: servicesFromGoogleScopes(scopes),
		OAuth2: &OAuth2Config{
			AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     "https://oauth2.googleapis.com/token",
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       scopes,
			ExtraParams: map[string]string{
				"access_type": "offline",
				"prompt":      "consent",
			},
		},
	}
}

// servicesFromGoogleScopes extracts human-readable service names from Google
// OAuth scope URLs. Unrecognized scopes are included as their raw path segment.
func servicesFromGoogleScopes(scopes []string) []string {
	var services []string
	for _, scope := range scopes {
		// Extract the last path segment: "https://...googleapis.com/auth/gmail.modify" → "gmail.modify"
		segment := scope
		if idx := strings.LastIndex(scope, "/"); idx >= 0 {
			segment = scope[idx+1:]
		}
		// Match on the first dotted part: "gmail.modify" → "gmail"
		base := segment
		if idx := strings.IndexByte(segment, '.'); idx >= 0 {
			base = segment[:idx]
		}
		if name, ok := googleScopeServices[base]; ok {
			services = append(services, name)
		} else {
			services = append(services, segment)
		}
	}
	return services
}
