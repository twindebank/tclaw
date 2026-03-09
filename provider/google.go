package provider

const GoogleProviderID ProviderID = "google"

// NewGoogleProvider creates the Google Workspace provider definition.
// Covers Gmail, Drive, Calendar, Docs, Sheets, Slides, and Tasks.
// OAuth2 config is populated from the tclaw config file.
func NewGoogleProvider(clientID, clientSecret string) *Provider {
	return &Provider{
		ID:   GoogleProviderID,
		Name: "Google Workspace",
		Auth: AuthOAuth2,
		OAuth2: &OAuth2Config{
			AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     "https://oauth2.googleapis.com/token",
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes: []string{
				"https://www.googleapis.com/auth/gmail.modify",
				"https://www.googleapis.com/auth/drive",
				"https://www.googleapis.com/auth/calendar",
				"https://www.googleapis.com/auth/documents",
				"https://www.googleapis.com/auth/spreadsheets",
				"https://www.googleapis.com/auth/presentations",
				"https://www.googleapis.com/auth/tasks",
			},
			ExtraParams: map[string]string{
				"access_type": "offline",
				"prompt":      "consent",
			},
		},
	}
}
