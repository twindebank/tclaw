package garmin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Garmin's SSO endpoints. Login happens on sso.garmin.com and the resulting CAS service ticket is
// exchanged for an OAuth token on diauth.garmin.com — a different host from both the SSO and the API.
const (
	ssoBaseURL      = "https://sso.garmin.com/sso"
	ssoEmbedURL     = ssoBaseURL + "/embed"
	ssoSignInURL    = ssoBaseURL + "/signin"
	ssoMFACodeURL   = ssoBaseURL + "/verifyMFA/mfaCode"
	ssoMFAVerifyURL = ssoBaseURL + "/verifyMFA/loginEnterMfaCode"
)

// diGrantType is the OAuth grant Garmin defines for redeeming a CAS service ticket.
const diGrantType = "https://connectapi.garmin.com/di-oauth2-service/oauth/grant/service_ticket"

// diClientIDs are tried in order when redeeming a ticket. Garmin retires client ids over time and
// an unrecognised one is refused, so falling through the list keeps login working across changes.
var diClientIDs = []string{
	"GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2",
	"GARMIN_CONNECT_MOBILE_ANDROID_DI_2024Q4",
	"GARMIN_CONNECT_MOBILE_ANDROID_DI",
	"GARMIN_CONNECT_MOBILE_IOS_DI",
}

// desktopUserAgent is sent on the SSO flow. The API uses the mobile client headers instead; the SSO
// widget is a web page and is served differently to a non-browser agent.
const desktopUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// antiWAFDelay separates the signin GET from the POST. Posting credentials immediately after
// loading the form looks automated and Garmin's WAF blocks it.
const antiWAFDelay = 3 * time.Second

var (
	csrfPattern    = regexp.MustCompile(`name="_csrf"\s+value="(.+?)"`)
	titlePattern   = regexp.MustCompile(`(?s)<title>(.+?)</title>`)
	ticketPattern  = regexp.MustCompile(`\?ticket=(ST-[^"&\s]+)`)
	mfaVarsPattern = regexp.MustCompile(`var\s+(customerGuid|mfaMethod|locale|clientId|codeSentTo)\s*=\s*"([^"]*)"\s*;`)
)

// MFAMethod is how Garmin delivers the second factor.
type MFAMethod string

const (
	MFAEmail MFAMethod = "email"
	MFASMS   MFAMethod = "sms"
	// MFAAuthenticator covers TOTP apps, where there is no code for Garmin to send.
	MFAAuthenticator MFAMethod = "authenticator"
)

// Credentials is a Garmin account login.
type Credentials struct {
	Email    string
	Password string
}

// LoginOptions configures Login.
type LoginOptions struct {
	Credentials Credentials

	// HTTPClient overrides the transport. A cookie jar is added if the client has none: the SSO
	// flow is entirely cookie-driven and fails without one.
	HTTPClient *http.Client
}

// LoginResult is the outcome of a login attempt. Exactly one of Token and Pending is meaningful:
// when MFA is required Pending is non-nil and Token is zero.
type LoginResult struct {
	Token   Token
	Pending *PendingLogin
}

// MFARequired reports whether the caller must supply a second factor.
func (r LoginResult) MFARequired() bool { return r.Pending != nil }

// PendingLogin is a login paused at the MFA challenge.
//
// It holds the live SSO session — cookies and the challenge page — which is why it cannot be
// serialised or sent to another process. Whatever collects the code must be able to call Complete
// on *this value*. A long-lived process (an MCP server, a CLI waiting on stdin) can hold it across
// turns; a short-lived one cannot resume a login it did not start.
type PendingLogin struct {
	// Method is how the code was delivered.
	Method MFAMethod

	// SentTo is the masked destination Garmin reported, when it reported one (e.g. "t***@g***.com").
	SentTo string

	client     *http.Client
	signInURL  string
	referer    string
	csrf       string
	serviceURL string
}

// Login begins the SSO flow, returning either a token or a pending MFA challenge.
func Login(ctx context.Context, opts LoginOptions) (LoginResult, error) {
	if opts.Credentials.Email == "" || opts.Credentials.Password == "" {
		return LoginResult{}, fmt.Errorf("garmin: email and password are required")
	}

	client, err := sessionClient(opts.HTTPClient)
	if err != nil {
		return LoginResult{}, err
	}

	embedParams := url.Values{
		"id":          {"gauth-widget"},
		"embedWidget": {"true"},
		"gauthHost":   {ssoBaseURL},
	}
	signInParams := url.Values{
		"id":                              {"gauth-widget"},
		"embedWidget":                     {"true"},
		"gauthHost":                       {ssoEmbedURL},
		"service":                         {ssoEmbedURL},
		"source":                          {ssoEmbedURL},
		"redirectAfterAccountLoginUrl":    {ssoEmbedURL},
		"redirectAfterAccountCreationUrl": {ssoEmbedURL},
	}

	// Step 1: load the embed page so the session cookies exist.
	if _, err := ssoGet(ctx, client, ssoEmbedURL, embedParams, ""); err != nil {
		return LoginResult{}, fmt.Errorf("load SSO embed page: %w", err)
	}

	// Step 2: load the sign-in form and take its CSRF token.
	signInPage, err := ssoGet(ctx, client, ssoSignInURL, signInParams, ssoEmbedURL)
	if err != nil {
		return LoginResult{}, fmt.Errorf("load sign-in page: %w", err)
	}
	csrf := firstSubmatch(csrfPattern, signInPage.body)
	if csrf == "" {
		return LoginResult{}, fmt.Errorf("sign-in page contained no CSRF token")
	}

	select {
	case <-time.After(antiWAFDelay):
	case <-ctx.Done():
		return LoginResult{}, ctx.Err()
	}

	// Step 3: post the credentials.
	form := url.Values{
		"username": {opts.Credentials.Email},
		"password": {opts.Credentials.Password},
		"embed":    {"true"},
		"_csrf":    {csrf},
	}
	posted, err := ssoPostForm(ctx, client, ssoSignInURL, signInParams, form, signInPage.finalURL)
	if err != nil {
		return LoginResult{}, fmt.Errorf("submit credentials: %w", err)
	}

	title := strings.TrimSpace(firstSubmatch(titlePattern, posted.body))
	if err := classifySignInTitle(title); err != nil {
		return LoginResult{}, err
	}

	// Step 4: MFA, or straight to a ticket.
	//
	// The MFA page carries the same title as the sign-in page, so the inline JS variables Garmin
	// emits are the reliable signal rather than the title.
	mfaVars := parseMFAVars(posted.body)
	if method, ok := mfaVars["mfaMethod"]; ok && method != "" {
		pending := &PendingLogin{
			Method:     MFAMethod(strings.ToLower(method)),
			SentTo:     mfaVars["codeSentTo"],
			client:     client,
			signInURL:  ssoSignInURL,
			referer:    posted.finalURL,
			csrf:       firstSubmatch(csrfPattern, posted.body),
			serviceURL: ssoEmbedURL,
		}
		if pending.csrf == "" {
			return LoginResult{}, fmt.Errorf("MFA page contained no CSRF token")
		}
		if err := pending.requestCode(ctx, mfaVars); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{Pending: pending}, nil
	}

	if title != "Success" {
		return LoginResult{}, fmt.Errorf("sign-in returned unexpected page %q", title)
	}
	ticket := firstSubmatch(ticketPattern, posted.body)
	if ticket == "" {
		return LoginResult{}, fmt.Errorf("sign-in succeeded but returned no service ticket")
	}

	token, err := exchangeServiceTicket(ctx, client, ticket, ssoEmbedURL)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token}, nil
}

// Complete finishes a login that stopped at the MFA challenge.
func (p *PendingLogin) Complete(ctx context.Context, code string) (Token, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return Token{}, fmt.Errorf("garmin: MFA code is required")
	}

	form := url.Values{
		"mfa-code": {code},
		"embed":    {"true"},
		"_csrf":    {p.csrf},
		"fromPage": {"setupEnterMfaCode"},
	}
	params := url.Values{
		"id":          {"gauth-widget"},
		"embedWidget": {"true"},
		"gauthHost":   {ssoEmbedURL},
		"service":     {ssoEmbedURL},
		"source":      {ssoEmbedURL},
	}

	verified, err := ssoPostForm(ctx, p.client, ssoMFAVerifyURL, params, form, p.referer)
	if err != nil {
		return Token{}, fmt.Errorf("verify MFA code: %w", err)
	}

	title := strings.TrimSpace(firstSubmatch(titlePattern, verified.body))
	if title != "Success" {
		// An incorrect or expired code lands here; Garmin re-renders the challenge page.
		return Token{}, fmt.Errorf("MFA verification failed (page %q) — the code may be wrong or expired", title)
	}

	ticket := firstSubmatch(ticketPattern, verified.body)
	if ticket == "" {
		return Token{}, fmt.Errorf("MFA verification succeeded but returned no service ticket")
	}
	return exchangeServiceTicket(ctx, p.client, ticket, p.serviceURL)
}

// requestCode asks Garmin to deliver an email or SMS code.
//
// Garmin does not always send one during the credential POST, and there is nothing to request for
// an authenticator app, so this is conditional. Without it the caller can end up waiting for a code
// that was never sent.
func (p *PendingLogin) requestCode(ctx context.Context, mfaVars map[string]string) error {
	if p.Method != MFAEmail && p.Method != MFASMS {
		return nil
	}
	if mfaVars["codeSentTo"] != "" {
		// Already delivered during sign-in; asking again would send a second code and invalidate
		// the one the user is about to read.
		return nil
	}

	payload := map[string]string{
		"customerGuid": mfaVars["customerGuid"],
		"mfaMethod":    mfaVars["mfaMethod"],
		"locale":       mfaVars["locale"],
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode MFA code request: %w", err)
	}

	target := ssoMFACodeURL + "?" + url.Values{"clientId": {mfaVars["clientId"]}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(encoded)))
	if err != nil {
		return fmt.Errorf("build MFA code request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Referer", p.referer)
	request.Header.Set("User-Agent", desktopUserAgent)

	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("request MFA code: %w", err)
	}
	defer closeBody(response)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read MFA code response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &APIError{
			StatusCode: response.StatusCode, Method: http.MethodPost,
			Path: ssoMFACodeURL, Body: string(body),
		}
	}
	return nil
}

// exchangeServiceTicket redeems a CAS ticket for an OAuth token.
//
// serviceURL must match the one used during sign-in — Garmin binds the ticket to it and refuses a
// mismatch.
func exchangeServiceTicket(ctx context.Context, client *http.Client, ticket, serviceURL string) (Token, error) {
	var lastErr error
	for _, clientID := range diClientIDs {
		form := url.Values{
			"client_id":      {clientID},
			"service_ticket": {ticket},
			"grant_type":     {diGrantType},
			"service_url":    {serviceURL},
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, diTokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			return Token{}, fmt.Errorf("build ticket exchange request: %w", err)
		}
		request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(clientID+":")))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Accept", "application/json,text/html;q=0.9,*/*;q=0.8")
		request.Header.Set("Cache-Control", "no-cache")
		applyClientHeaders(request.Header)

		response, err := client.Do(request)
		if err != nil {
			return Token{}, fmt.Errorf("send ticket exchange request: %w", err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeBody(response)
		if readErr != nil {
			return Token{}, fmt.Errorf("read ticket exchange response: %w", readErr)
		}

		if response.StatusCode == http.StatusTooManyRequests {
			// Retrying other client ids would only deepen the rate limit.
			return Token{}, &APIError{
				StatusCode: response.StatusCode, Method: http.MethodPost,
				Path: diTokenURL, Body: string(body),
			}
		}
		if response.StatusCode != http.StatusOK {
			lastErr = &APIError{
				StatusCode: response.StatusCode, Method: http.MethodPost,
				Path: diTokenURL, Body: string(body),
			}
			continue
		}

		var payload struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			lastErr = fmt.Errorf("parse ticket exchange response for %s: %w", clientID, err)
			continue
		}
		if payload.AccessToken == "" {
			lastErr = fmt.Errorf("ticket exchange for %s returned no access token", clientID)
			continue
		}
		return Token{
			AccessToken:  payload.AccessToken,
			RefreshToken: payload.RefreshToken,
			ClientID:     clientID,
		}, nil
	}

	return Token{}, fmt.Errorf("no client id could redeem the service ticket: %w", lastErr)
}

// --- helpers ---

type ssoResponse struct {
	body     string
	finalURL string
}

// sessionClient returns a client with a cookie jar, since the whole SSO flow is cookie-driven.
func sessionClient(base *http.Client) (*http.Client, error) {
	if base == nil {
		base = &http.Client{Timeout: defaultTimeout}
	}
	if base.Jar != nil {
		return base, nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	copied := *base
	copied.Jar = jar
	return &copied, nil
}

func ssoGet(ctx context.Context, client *http.Client, target string, params url.Values, referer string) (ssoResponse, error) {
	return ssoDo(ctx, client, http.MethodGet, target, params, nil, referer)
}

func ssoPostForm(ctx context.Context, client *http.Client, target string, params, form url.Values, referer string) (ssoResponse, error) {
	return ssoDo(ctx, client, http.MethodPost, target, params, form, referer)
}

func ssoDo(
	ctx context.Context,
	client *http.Client,
	method, target string,
	params, form url.Values,
	referer string,
) (ssoResponse, error) {
	if len(params) > 0 {
		target += "?" + params.Encode()
	}

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return ssoResponse{}, fmt.Errorf("build %s %s: %w", method, target, err)
	}
	request.Header.Set("User-Agent", desktopUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	response, err := client.Do(request)
	if err != nil {
		return ssoResponse{}, fmt.Errorf("send %s %s: %w", method, target, err)
	}
	defer closeBody(response)

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return ssoResponse{}, fmt.Errorf("read %s %s response: %w", method, target, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ssoResponse{}, &APIError{
			StatusCode: response.StatusCode, Method: method, Path: target, Body: string(raw),
		}
	}

	final := target
	if response.Request != nil && response.Request.URL != nil {
		final = response.Request.URL.String()
	}
	return ssoResponse{body: string(raw), finalURL: final}, nil
}

// classifySignInTitle turns the sign-in page title into an error where it signals a definite
// outcome. Distinguishing bad credentials from infrastructure trouble matters: one is worth
// reporting to the user, the other is worth retrying.
func classifySignInTitle(title string) error {
	lower := strings.ToLower(title)
	for _, hint := range []string{"bad gateway", "service unavailable", "cloudflare", "502", "503"} {
		if strings.Contains(lower, hint) {
			return fmt.Errorf("Garmin sign-in is unavailable (page %q)", title)
		}
	}
	for _, hint := range []string{"locked", "invalid", "incorrect", "account error"} {
		if strings.Contains(lower, hint) {
			return fmt.Errorf("sign-in rejected the credentials (page %q)", title)
		}
	}
	if strings.Contains(lower, "unable to sign in") || strings.Contains(lower, "unable to login") {
		return fmt.Errorf("account cannot use web sign-in (page %q); child and family accounts are not supported", title)
	}
	return nil
}

func parseMFAVars(html string) map[string]string {
	vars := make(map[string]string)
	for _, match := range mfaVarsPattern.FindAllStringSubmatch(html, -1) {
		vars[match[1]] = match[2]
	}
	return vars
}

func firstSubmatch(pattern *regexp.Regexp, text string) string {
	match := pattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
