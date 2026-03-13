package provider

const MonzoProviderID ProviderID = "monzo"

// NewMonzoProvider creates the Monzo banking provider definition.
// Monzo uses OAuth2 with Strong Customer Authentication — the user must
// approve access via the Monzo app after the browser flow.
func NewMonzoProvider(clientID, clientSecret string) *Provider {
	return &Provider{
		ID:       MonzoProviderID,
		Name:     "Monzo",
		Auth:     AuthOAuth2,
		Services: []string{"Monzo Banking"},
		OAuth2: &OAuth2Config{
			AuthURL:      "https://auth.monzo.com/",
			TokenURL:     "https://api.monzo.com/oauth2/token",
			ClientID:     clientID,
			ClientSecret: clientSecret,
			// Monzo doesn't use scopes — the user approves full access via SCA.
		},
	}
}
