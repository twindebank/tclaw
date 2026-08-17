package garmintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	garminapi "github.com/twindebank/garmin-settings/garmin"
	garminsettings "github.com/twindebank/garmin-settings/settings"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
)

// Secret store keys. Agent-facing keys are validated against ^[a-z0-9_]+$ elsewhere in tclaw, so
// these deliberately avoid any separator that could escape the namespace.
const (
	EmailStoreKey    = "garmin_email"
	PasswordStoreKey = "garmin_password"
	TokenStoreKey    = "garmin_token"
)

// Tool names.
const (
	ToolLogin          = "garmin_login"
	ToolLoginMFA       = "garmin_login_mfa"
	ToolDevices        = "garmin_devices"
	ToolSettingsSearch = "garmin_settings_search"
	ToolSettingsGet    = "garmin_settings_get"
	ToolSettingsSet    = "garmin_settings_set"
	ToolScreensGet     = "garmin_screens_get"
	ToolScreensSet     = "garmin_screens_set"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolLogin, ToolLoginMFA, ToolDevices, ToolSettingsSearch,
		ToolSettingsGet, ToolSettingsSet, ToolScreensGet, ToolScreensSet,
	}
}

// Deps holds dependencies for the Garmin tools.
type Deps struct {
	SecretStore secret.Store

	// HTTPClient overrides the transport used for Garmin calls. Nil means the library default.
	// Tests set this so they never reach Garmin's servers — a sign-in attempt from CI would be
	// slow, flaky, and would count against the real account's rate limit.
	HTTPClient *http.Client

	// pendingLogin carries an MFA challenge between the login and login_mfa calls. It is a pointer
	// so every handler shares one instance; the MCP server is long-lived per user, which is what
	// makes this work at all — the challenge holds a live SSO session that cannot be serialised.
	pendingLogin *pendingLogin
}

// pendingLogin guards the in-flight MFA challenge.
type pendingLogin struct {
	mu      sync.Mutex
	current *garminapi.PendingLogin
}

func (p *pendingLogin) set(login *garminapi.PendingLogin) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = login
}

// take returns the pending challenge and clears it, so a code cannot be replayed against a
// challenge that has already been used.
func (p *pendingLogin) take() *garminapi.PendingLogin {
	p.mu.Lock()
	defer p.mu.Unlock()
	login := p.current
	p.current = nil
	return login
}

// RegisterTools registers the Garmin tools on the handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	deps.pendingLogin = &pendingLogin{}
	for _, def := range toolDefs {
		handler.Register(def, makeHandler(def.Name, deps))
	}
}

// UnregisterTools removes the Garmin tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	for _, def := range toolDefs {
		handler.Unregister(def.Name)
	}
}

var toolDefs = []mcp.ToolDef{
	{
		Name: ToolLogin,
		Description: "Sign in to Garmin Connect and cache an OAuth token. Only needed once — the token " +
			"refreshes itself afterwards. Uses the stored email and password unless overridden. " +
			"If the account has MFA, this returns status 'mfa_required' with the masked destination " +
			"the code was sent to; call garmin_login_mfa with the code to finish. The challenge is " +
			"held in memory, so the code must be supplied without restarting tclaw.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"email": {"type": "string", "description": "Garmin Connect email. Only needed on first use — stored encrypted afterwards."},
				"password": {"type": "string", "description": "Garmin Connect password. Only needed on first use — stored encrypted afterwards."}
			}
		}`),
	},
	{
		Name: ToolLoginMFA,
		Description: "Finish a Garmin sign-in that returned 'mfa_required', using the code the user " +
			"supplies. Must follow a garmin_login call in the same tclaw process.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"code": {"type": "string", "description": "The multi-factor code the user received."}
			},
			"required": ["code"]
		}`),
	},
	{
		Name:        ToolDevices,
		Description: "List the Garmin devices registered to the account, with their device IDs. Every other Garmin tool needs a device_id from here.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	},
	{
		Name: ToolSettingsSearch,
		Description: "Find Garmin setting names and their value types without touching the account. " +
			"Use this before garmin_settings_set to get the exact setting name and what type it wants. " +
			"Returns whether each setting is per-activity (needs a sport) or read-only.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"term": {"type": "string", "description": "Substring to match, e.g. 'backlight', 'unit', 'zone'. Omit to list every known setting."}
			}
		}`),
	},
	{
		Name: ToolSettingsGet,
		Description: "Read the current settings for a Garmin device. Only settings that have been set " +
			"appear; an absent setting is not the same as the device being unable to hold it.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"device_id": {"type": "integer", "description": "Device ID from garmin_devices."},
				"filter": {"type": "string", "description": "Only show settings whose name contains this substring."}
			},
			"required": ["device_id"]
		}`),
	},
	{
		Name: ToolSettingsSet,
		Description: "Change one Garmin device setting. Confirm the exact setting name with " +
			"garmin_settings_search first. Two cautions: writing a setting that is currently unset " +
			"cannot be undone (Garmin has no way to return it to unset), and string settings are not " +
			"validated server-side, so a nonsense value is stored happily. The change reaches the " +
			"device on its next sync.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"device_id": {"type": "integer", "description": "Device ID from garmin_devices."},
				"setting": {"type": "string", "description": "Setting name, e.g. 'TIME_FORMAT' or the fully-qualified 'DeviceSettingId.TIME_FORMAT'. A bare name that exists in more than one namespace is rejected."},
				"value": {"type": "string", "description": "New value as text; it is converted to the setting's type (int, bool, double, string)."},
				"sport": {"type": "string", "description": "Required for per-activity settings, e.g. 'running', 'cycling', 'swimming'."}
			},
			"required": ["device_id", "setting", "value"]
		}`),
	},
	{
		Name:        ToolScreensGet,
		Description: "Show the activity data screens configured on a Garmin device: which activity, which page, the layout, and the field in each slot.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"device_id": {"type": "integer", "description": "Device ID from garmin_devices."}
			},
			"required": ["device_id"]
		}`),
	},
	{
		Name: ToolScreensSet,
		Description: "Set one activity data screen on a Garmin device — the fields shown while recording " +
			"that activity. Overwrites the screen at that activity and page if it already exists; there " +
			"is no way to delete a screen. Field names are validated by Garmin, so an invalid one fails " +
			"the whole write. The change reaches the device on its next sync.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"device_id": {"type": "integer", "description": "Device ID from garmin_devices."},
				"sport": {"type": "string", "description": "Activity, e.g. 'running', 'cycling', 'swimming', 'hiking'."},
				"sub_sport": {"type": "integer", "description": "FIT sub-sport number. Defaults to 0 (generic)."},
				"page": {"type": "integer", "description": "Screen number within the activity, starting at 1."},
				"fields": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Field for each slot in order, e.g. ['PACE','HEART_RATE','DISTANCE']. Use 'NONE' for an empty slot."
				}
			},
			"required": ["device_id", "sport", "page", "fields"]
		}`),
	},
}

func makeHandler(name string, deps Deps) mcp.ToolHandler {
	switch name {
	case ToolLogin:
		return loginHandler(deps)
	case ToolLoginMFA:
		return loginMFAHandler(deps)
	case ToolDevices:
		return devicesHandler(deps)
	case ToolSettingsSearch:
		return settingsSearchHandler()
	case ToolSettingsGet:
		return settingsGetHandler(deps)
	case ToolSettingsSet:
		return settingsSetHandler(deps)
	case ToolScreensGet:
		return screensGetHandler(deps)
	case ToolScreensSet:
		return screensSetHandler(deps)
	default:
		// Unreachable unless a tool is added to toolDefs without a handler; fail loudly rather than
		// registering a tool that silently does nothing.
		return func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("garmin: no handler registered for tool %q", name)
		}
	}
}

func loginHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var args struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}

		credentials, err := resolveCredentials(ctx, deps, args.Email, args.Password)
		if err != nil {
			return nil, err
		}

		result, err := garminapi.Login(ctx, garminapi.LoginOptions{
			Credentials: credentials,
			HTTPClient:  deps.HTTPClient,
		})
		if err != nil {
			return nil, fmt.Errorf("garmin sign-in: %w", err)
		}

		if result.MFARequired() {
			deps.pendingLogin.set(result.Pending)
			return marshalResult(map[string]any{
				"status":          "mfa_required",
				"mfa_method":      string(result.Pending.Method),
				"mfa_destination": result.Pending.SentTo,
				"next_step": "Ask the user for the code, then call " + ToolLoginMFA +
					". Do not guess the code.",
			})
		}

		if err := saveToken(ctx, deps, result.Token); err != nil {
			return nil, err
		}
		return marshalResult(map[string]any{"status": "signed_in"})
	}
}

func loginMFAHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var args struct {
			Code string `json:"code"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Code) == "" {
			return nil, errors.New("code is required")
		}

		pending := deps.pendingLogin.take()
		if pending == nil {
			return nil, fmt.Errorf("no sign-in is waiting for a code; call %s first", ToolLogin)
		}

		token, err := pending.Complete(ctx, args.Code)
		if err != nil {
			return nil, fmt.Errorf("garmin MFA: %w", err)
		}
		if err := saveToken(ctx, deps, token); err != nil {
			return nil, err
		}
		return marshalResult(map[string]any{"status": "signed_in"})
	}
}

func devicesHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		api, err := apiClient(deps)
		if err != nil {
			return nil, err
		}
		devices, err := api.Devices(ctx)
		if err != nil {
			return nil, describeAPIError(err)
		}

		summaries := make([]map[string]any, 0, len(devices))
		for _, device := range devices {
			summaries = append(summaries, map[string]any{
				"device_id":       int64(device.DeviceID),
				"name":            device.ProductName,
				"application_key": string(device.ApplicationKey),
				"firmware":        device.FirmwareVersion,
			})
		}
		return marshalResult(map[string]any{"devices": summaries})
	}
}

func settingsSearchHandler() mcp.ToolHandler {
	return func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var args struct {
			Term string `json:"term"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}

		matches := garminsettings.Search(args.Term)
		results := make([]map[string]any, 0, len(matches))
		for _, definition := range matches {
			results = append(results, map[string]any{
				"setting":      string(definition.ID),
				"type":         string(definition.Kind),
				"per_activity": definition.Scoped,
				"read_only":    definition.ReadOnly,
			})
		}
		return marshalResult(map[string]any{"count": len(results), "settings": results})
	}
}

func settingsGetHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var args struct {
			DeviceID int64  `json:"device_id"`
			Filter   string `json:"filter"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.DeviceID == 0 {
			return nil, errors.New("device_id is required")
		}

		client, err := settingsClient(deps)
		if err != nil {
			return nil, err
		}
		document, err := client.Get(ctx, garminapi.DeviceID(args.DeviceID))
		if err != nil {
			return nil, describeAPIError(err)
		}

		filter := strings.ToUpper(args.Filter)
		values := make([]map[string]any, 0, len(document.Values))
		for _, value := range document.Values {
			if filter != "" && !strings.Contains(strings.ToUpper(string(value.ID)), filter) {
				continue
			}
			values = append(values, map[string]any{
				"setting": string(value.ID),
				"value":   describeValue(value),
			})
		}
		return marshalResult(map[string]any{
			"device_id": args.DeviceID,
			"count":     len(values),
			"settings":  values,
		})
	}
}

func settingsSetHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var args struct {
			DeviceID int64  `json:"device_id"`
			Setting  string `json:"setting"`
			Value    string `json:"value"`
			Sport    string `json:"sport"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.DeviceID == 0 {
			return nil, errors.New("device_id is required")
		}
		if args.Setting == "" {
			return nil, errors.New("setting is required")
		}

		definition, err := garminsettings.Resolve(args.Setting)
		if err != nil {
			return nil, err
		}
		value, err := garminsettings.ParseValue(definition, args.Value)
		if err != nil {
			return nil, err
		}

		switch {
		case definition.Scoped && args.Sport == "":
			return nil, fmt.Errorf("%s is a per-activity setting; supply a sport", definition.ID)
		case definition.Scoped:
			sport, err := garminsettings.ParseSport(args.Sport)
			if err != nil {
				return nil, err
			}
			value = garminsettings.ScopeToActivity(value, garminsettings.Activity{Sport: sport})
		case args.Sport != "":
			return nil, fmt.Errorf("%s is not a per-activity setting; omit sport", definition.ID)
		}

		api, err := apiClient(deps)
		if err != nil {
			return nil, err
		}
		deviceID := garminapi.DeviceID(args.DeviceID)
		device, err := api.Device(ctx, deviceID)
		if err != nil {
			return nil, describeAPIError(err)
		}

		client := garminsettings.NewClient(api)
		before, err := client.Get(ctx, deviceID)
		if err != nil {
			return nil, describeAPIError(err)
		}
		_, wasSet := before.Find(definition.ID)

		result, err := client.Set(ctx, garminsettings.SetParams{
			DeviceID:       deviceID,
			ApplicationKey: device.ApplicationKey,
			Values:         []garminsettings.Value{value},
		})
		if err != nil {
			return nil, describeAPIError(err)
		}

		written, ok := result.Find(definition.ID)
		if !ok {
			return nil, fmt.Errorf("wrote %s but Garmin did not echo it back", definition.ID)
		}

		response := map[string]any{
			"setting": string(definition.ID),
			"value":   describeValue(written),
			"note":    "The change reaches the device on its next sync; there is no way to force one.",
		}
		if !wasSet {
			// Worth telling the user: Garmin cannot return a setting to unset once written.
			response["warning"] = "This setting was previously unset. Garmin cannot restore it to " +
				"unset, so this change is not fully reversible."
		}
		return marshalResult(response)
	}
}

func screensGetHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var args struct {
			DeviceID int64 `json:"device_id"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.DeviceID == 0 {
			return nil, errors.New("device_id is required")
		}

		client, err := settingsClient(deps)
		if err != nil {
			return nil, err
		}
		document, err := client.Get(ctx, garminapi.DeviceID(args.DeviceID))
		if err != nil {
			return nil, describeAPIError(err)
		}

		screens := garminsettings.DataScreens(document)
		results := make([]map[string]any, 0, len(screens))
		for _, screen := range screens {
			results = append(results, map[string]any{
				"sport":     screen.Activity.Sport.String(),
				"sub_sport": int(screen.Activity.SubSport),
				"page":      screen.Page,
				"zones":     screen.Zones,
				"fields":    fieldNames(screen.Fields),
			})
		}
		return marshalResult(map[string]any{
			"device_id": args.DeviceID,
			"count":     len(results),
			"screens":   results,
		})
	}
}

func screensSetHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var args struct {
			DeviceID int64    `json:"device_id"`
			Sport    string   `json:"sport"`
			SubSport int      `json:"sub_sport"`
			Page     int      `json:"page"`
			Fields   []string `json:"fields"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.DeviceID == 0 {
			return nil, errors.New("device_id is required")
		}
		if len(args.Fields) == 0 {
			return nil, errors.New("fields is required")
		}

		sport, err := garminsettings.ParseSport(args.Sport)
		if err != nil {
			return nil, err
		}

		fields := make([]garminsettings.Field, 0, len(args.Fields))
		for _, name := range args.Fields {
			fields = append(fields, garminsettings.Field(strings.ToUpper(strings.TrimSpace(name))))
		}
		screen := garminsettings.DataScreen{
			Activity: garminsettings.Activity{
				Sport:    sport,
				SubSport: garminsettings.SubSport(args.SubSport),
			},
			Page:   args.Page,
			Fields: fields,
			Zones:  len(fields),
		}
		if err := screen.Validate(); err != nil {
			return nil, err
		}

		api, err := apiClient(deps)
		if err != nil {
			return nil, err
		}
		deviceID := garminapi.DeviceID(args.DeviceID)
		device, err := api.Device(ctx, deviceID)
		if err != nil {
			return nil, describeAPIError(err)
		}

		written, err := garminsettings.NewClient(api).SetDataScreen(ctx, garminsettings.SetDataScreenParams{
			DeviceID:       deviceID,
			ApplicationKey: device.ApplicationKey,
			Screen:         screen,
		})
		if err != nil {
			return nil, describeAPIError(err)
		}

		response := map[string]any{
			"sport":     written.Activity.Sport.String(),
			"sub_sport": int(written.Activity.SubSport),
			"page":      written.Page,
			"zones":     written.Zones,
			"fields":    fieldNames(written.Fields),
			"note":      "The change reaches the device on its next sync; there is no way to force one.",
		}
		if unknown := screen.UnknownFields(); len(unknown) > 0 {
			response["unrecognised_fields"] = fieldNames(unknown)
		}
		return marshalResult(response)
	}
}

// --- helpers ---

// secretTokenStore adapts tclaw's encrypted secret store to the library's TokenStore, so the
// Garmin OAuth token never lands on the filesystem.
type secretTokenStore struct {
	store secret.Store
}

func (s secretTokenStore) Load(ctx context.Context) (garminapi.Token, error) {
	raw, err := s.store.Get(ctx, TokenStoreKey)
	if err != nil {
		return garminapi.Token{}, fmt.Errorf("read stored Garmin token: %w", err)
	}
	if raw == "" {
		return garminapi.Token{}, garminapi.ErrNoToken
	}
	var token garminapi.Token
	if err := json.Unmarshal([]byte(raw), &token); err != nil {
		return garminapi.Token{}, fmt.Errorf("parse stored Garmin token: %w", err)
	}
	return token, nil
}

func (s secretTokenStore) Save(ctx context.Context, token garminapi.Token) error {
	encoded, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("encode Garmin token: %w", err)
	}
	if err := s.store.Set(ctx, TokenStoreKey, string(encoded)); err != nil {
		return fmt.Errorf("store Garmin token: %w", err)
	}
	return nil
}

func apiClient(deps Deps) (*garminapi.Client, error) {
	store := secretTokenStore{store: deps.SecretStore}
	api, err := garminapi.New(garminapi.Options{
		TokenSource: garminapi.NewRefreshingTokenSource(store, deps.HTTPClient),
		HTTPClient:  deps.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("build Garmin client: %w", err)
	}
	return api, nil
}

func settingsClient(deps Deps) (*garminsettings.Client, error) {
	api, err := apiClient(deps)
	if err != nil {
		return nil, err
	}
	return garminsettings.NewClient(api), nil
}

func saveToken(ctx context.Context, deps Deps, token garminapi.Token) error {
	return secretTokenStore{store: deps.SecretStore}.Save(ctx, token)
}

// resolveCredentials prefers arguments over stored values and persists anything new, so the user
// only ever supplies them once.
func resolveCredentials(ctx context.Context, deps Deps, email, password string) (garminapi.Credentials, error) {
	if email != "" {
		if err := deps.SecretStore.Set(ctx, EmailStoreKey, email); err != nil {
			return garminapi.Credentials{}, fmt.Errorf("store Garmin email: %w", err)
		}
	} else {
		stored, err := deps.SecretStore.Get(ctx, EmailStoreKey)
		if err != nil {
			return garminapi.Credentials{}, fmt.Errorf("read stored Garmin email: %w", err)
		}
		email = stored
	}

	if password != "" {
		if err := deps.SecretStore.Set(ctx, PasswordStoreKey, password); err != nil {
			return garminapi.Credentials{}, fmt.Errorf("store Garmin password: %w", err)
		}
	} else {
		stored, err := deps.SecretStore.Get(ctx, PasswordStoreKey)
		if err != nil {
			return garminapi.Credentials{}, fmt.Errorf("read stored Garmin password: %w", err)
		}
		password = stored
	}

	if email == "" || password == "" {
		return garminapi.Credentials{}, errors.New(
			"no Garmin credentials stored; supply email and password, or set them with a secret form")
	}
	return garminapi.Credentials{Email: email, Password: password}, nil
}

// describeAPIError turns a bare token error into an instruction the agent can act on, and leaves
// everything else alone.
func describeAPIError(err error) error {
	if errors.Is(err, garminapi.ErrNoToken) {
		return fmt.Errorf("not signed in to Garmin; call %s first: %w", ToolLogin, err)
	}
	return err
}

// describeValue renders whichever value slot is populated.
func describeValue(value garminsettings.Value) any {
	if text, ok := value.String(); ok {
		return text
	}
	if number, ok := value.Int(); ok {
		return number
	}
	if flag, ok := value.Bool(); ok {
		return flag
	}
	if number, ok := value.Float(); ok {
		return number
	}
	if value.PageDTO != nil {
		return fieldNames(value.PageDTO.DisplayFields)
	}
	if len(value.StringMap) > 0 {
		return value.StringMap
	}
	return nil
}

func fieldNames(fields []garminsettings.Field) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, string(field))
	}
	return names
}

func decodeArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse arguments: %w", err)
	}
	return nil
}

func marshalResult(payload any) (json.RawMessage, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode result: %w", err)
	}
	return encoded, nil
}
