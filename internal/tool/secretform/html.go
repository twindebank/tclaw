package secretform

import "html/template"

// Shared CSS used across all templates to keep things DRY.
const sharedCSS = `
  *, *::before, *::after { box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    background: #f5f5f5; color: #1a1a1a; margin: 0; padding: 24px 16px;
    line-height: 1.5;
  }
  .container { max-width: 480px; margin: 0 auto; }
  .card {
    background: #fff; border-radius: 12px; padding: 32px 24px;
    box-shadow: 0 1px 3px rgba(0,0,0,0.1);
  }
`

var formTemplate = template.Must(template.New("form").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>` + sharedCSS + `
  h1 { font-size: 20px; font-weight: 600; margin: 0 0 4px; }
  .description { color: #666; font-size: 14px; margin: 0 0 24px; }
  .field { margin-bottom: 20px; }
  label { display: block; font-size: 14px; font-weight: 500; margin-bottom: 6px; }
  .help { font-size: 12px; color: #888; margin-top: 4px; }
  input[type="text"], input[type="password"] {
    width: 100%; padding: 10px 12px; font-size: 14px;
    border: 1px solid #ddd; border-radius: 8px;
    background: #fafafa; transition: border-color 0.15s;
  }
  input:focus { outline: none; border-color: #333; background: #fff; }
  .required-mark { color: #e53935; }
  .verify-section {
    background: #f0f4ff; border: 1px solid #c7d2fe; border-radius: 8px;
    padding: 16px; margin-bottom: 20px;
  }
  .verify-section label { color: #3730a3; }
  .verify-section input {
    text-align: center; font-size: 20px; letter-spacing: 4px;
    font-family: monospace;
  }
  .verify-help { font-size: 12px; color: #6366f1; margin-top: 4px; }
  button {
    width: 100%; padding: 12px; font-size: 15px; font-weight: 500;
    background: #1a1a1a; color: #fff; border: none; border-radius: 8px;
    cursor: pointer; margin-top: 8px; transition: background 0.15s;
  }
  button:hover { background: #333; }
  button:disabled { background: #999; cursor: not-allowed; }
  .footer {
    text-align: center; font-size: 12px; color: #aaa; margin-top: 16px;
  }
  .lock { display: inline-block; margin-right: 4px; }
</style>
</head>
<body>
<div class="container">
  <div class="card">
    <h1>{{.Title}}</h1>
    {{if .Description}}<p class="description">{{.Description}}</p>{{end}}
    <form method="POST" id="secret-form" autocomplete="off">
      <div class="verify-section">
        <label>
          Verification Code <span class="required-mark">*</span>
        </label>
        <input
          type="text"
          name="_verify_code"
          required
          autocomplete="off"
          inputmode="numeric"
          pattern="[0-9]{6}"
          maxlength="6"
          placeholder="000000"
        >
        <div class="verify-help">Enter the 6-digit code from your chat</div>
      </div>
      {{range .Fields}}
      <div class="field">
        <label>
          {{.Label}}
          {{if .IsRequired}}<span class="required-mark">*</span>{{end}}
        </label>
        <input
          type="{{if .IsSecret}}password{{else}}text{{end}}"
          name="{{.Key}}"
          {{if .IsRequired}}required{{end}}
          autocomplete="off"
          spellcheck="false"
        >
        {{if .Description}}<div class="help">{{.Description}}</div>{{end}}
      </div>
      {{end}}
      <button type="submit" id="submit-btn">Submit</button>
    </form>
  </div>
  <div class="footer"><span class="lock">&#x1f512;</span>Values are encrypted and stored securely</div>
</div>
<script>
  document.getElementById('secret-form').addEventListener('submit', function() {
    var btn = document.getElementById('submit-btn');
    btn.disabled = true;
    btn.textContent = 'Submitting\u2026';
  });
</script>
</body>
</html>`))

var verifyErrorTemplate = template.Must(template.New("verify-error").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Invalid Code</title>
<style>` + sharedCSS + `
  h1 { font-size: 20px; font-weight: 600; margin: 0 0 8px; }
  p { color: #666; font-size: 14px; margin: 0 0 16px; }
  .error-icon { font-size: 48px; margin-bottom: 16px; text-align: center; }
  a {
    display: inline-block; color: #1a1a1a; font-weight: 500; font-size: 14px;
    text-decoration: underline;
  }
  .center { text-align: center; }
</style>
</head>
<body>
<div class="container">
  <div class="card center">
    <div class="error-icon">&#x26d4;</div>
    <h1>Invalid verification code</h1>
    <p>The code you entered doesn't match. Check the code in your chat and try again.</p>
    <a href="" onclick="history.back(); return false;">Go back</a>
  </div>
</div>
</body>
</html>`))

var confirmationTemplate = template.Must(template.New("confirmation").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Submitted</title>
<style>` + sharedCSS + `
  body { text-align: center; }
  .card { padding: 48px 24px; }
  .check { font-size: 48px; margin-bottom: 16px; }
  h1 { font-size: 20px; font-weight: 600; margin: 0 0 8px; }
  p { color: #666; font-size: 14px; margin: 0; }
</style>
</head>
<body>
<div class="container">
  <div class="card">
    <div class="check">&#x2705;</div>
    <h1>Submitted</h1>
    <p>Your information has been stored securely. You can close this tab and return to your chat.</p>
  </div>
</div>
</body>
</html>`))

var notFoundTemplate = template.Must(template.New("notfound").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Not Found</title>
<style>` + sharedCSS + `
  body { text-align: center; }
  .card { padding: 48px 24px; }
  h1 { font-size: 20px; font-weight: 600; margin: 0 0 8px; }
  p { color: #666; font-size: 14px; margin: 0; }
</style>
</head>
<body>
<div class="container">
  <div class="card">
    <h1>Form not found</h1>
    <p>This link may have expired or already been used. Please request a new one from your chat.</p>
  </div>
</div>
</body>
</html>`))
