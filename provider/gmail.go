package provider

const GmailProviderID ProviderID = "gmail"

// NewGmailProvider creates the Gmail provider definition.
// OAuth2 config is populated from the tclaw config file.
func NewGmailProvider(clientID, clientSecret string) *Provider {
	return &Provider{
		ID:   GmailProviderID,
		Name: "Gmail",
		Auth: AuthOAuth2,
		OAuth2: &OAuth2Config{
			AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     "https://oauth2.googleapis.com/token",
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes: []string{
				"https://www.googleapis.com/auth/gmail.readonly",
				"https://www.googleapis.com/auth/gmail.send",
				"https://www.googleapis.com/auth/gmail.modify",
			},
			ExtraParams: map[string]string{
				"access_type": "offline",
				"prompt":      "consent",
			},
		},
	}
}
