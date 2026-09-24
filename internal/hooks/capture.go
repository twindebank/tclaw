package hooks

import (
	"os"
	"regexp"
	"strings"

	"tclaw/internal/memorylayout"
)

// A verdict on the work itself. Nothing in a task brief sounds like this, so
// these skip the brief filter below however long the message is.
var hardCorrectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[^a-z])ffs([^a-z]|$)`),
	regexp.MustCompile(`(?i)(^|[^a-z])wtf([^a-z]|$)`),
	regexp.MustCompile(`(?i)(^|[^a-z])fuck`),
	regexp.MustCompile(`(?i)for (?:god'?s|christ'?s|pete'?s) sake`),
	regexp.MustCompile(`(?i)(?:utter|absolute|complete|total)(?:ly)? (?:shit|garbage|rubbish|crap|nonsense)`),
	regexp.MustCompile(`(?i)(?:is|it'?s|its|that'?s|thats|this is) ` +
		`(?:shit|garbage|rubbish|crap|awful|terrible|appalling|unacceptable)(?:[^a-z]|$)`),
	regexp.MustCompile(`(?i)what (?:kind|sort) of .{0,40}(?:are|is) you(?:[^a-z]|$)`),
	// The accusation, not the bare noun: a brief describing rule violations to fix
	// is work being assigned, not a complaint about work already done.
	regexp.MustCompile(`(?i)you(?:'?ve| have|'?re| are)? ?violat(?:ed|es|ing)`),
}

// Complaints about work already done. Over-capture is the safe direction here: a
// spurious row costs the retro one discard, a missed correction is lost for good.
var complaintPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)you keep|how many times|i told you|i said|again[!?]`),
	regexp.MustCompile(`(?i)stop (doing|adding|using|writing|making|putting)`),
	regexp.MustCompile(`(?i)why (did|did ?n'?t|would|would ?n'?t|are|are ?n'?t|have ?n'?t) you`),
	regexp.MustCompile(`(?i)that'?s not |thats not |that is not `),
	regexp.MustCompile(`(?i)wrong|incorrect`),
	regexp.MustCompile(`(?i)should ?n'?t have|should not have`),
	regexp.MustCompile(`(?i)jargon|claudy|verbose`),
	regexp.MustCompile(`(?i)opaque|unreadable|makes? (?:no|any) sense|means nothing`),
	regexp.MustCompile(`(?i)(?:comments?|code|wording|docs?|tickets?|explanations?|this|that|these|it)` +
		`(?: is| are|'?s)(?: so| really| very)? (?:hard|difficult|impossible) to (?:read|follow|understand)`),
	regexp.MustCompile(`(?i)(?:this|that|it)(?:'?s| is| was) (?:so |really |very )?(?:frustrating|annoying|painful|confusing)`),
	regexp.MustCompile(`(?i)(?:that'?s|thats|this is|it'?s|its) a violation|in violation of`),
	regexp.MustCompile(`(?i)^(no|nope|nah)[,. !]`),
	regexp.MustCompile(`(?i)you (missed|forgot|broke|ignored)`),
	regexp.MustCompile(`(?i)not what i (asked|wanted|said)`),
	// Work nobody requested. A correction of scope reads nothing like one of
	// quality, so the patterns above all miss it.
	regexp.MustCompile(`(?i)\b(?:i )?(?:never|did ?n'?t) (?:ask|want|say|said)`),
	regexp.MustCompile(`(?i)nobody asked|who asked (?:you|for)`),
}

// Constraints and ordinary work verbs, which a brief carries in passing. These
// are gated by the brief filter; the same words with nothing around them are an
// objection.
var constraintPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)do not |don'?t (do|add|use|write|make|put|create|say|forget|stop)`),
	regexp.MustCompile(`(?i)revert|undo that`),
	// Stopping before the work is done. A brief can carry "don't stop until it
	// is green", so this stays gated rather than sitting with the complaints.
	regexp.MustCompile(`(?i)(?:have ?n'?t|did ?n'?t|before you) finish`),
}

// Prompts the harness writes on the user's behalf. These are not the user's
// words, so a trigger matching one is evidence of nothing.
var injectedPromptPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^your (claude\.ai|claude code) usage limit has reset`),
	regexp.MustCompile(`(?i)^[<\[(]?system.?reminder`),
	regexp.MustCompile(`(?i)^this session is being continued from a previous`),
	regexp.MustCompile(`(?i)^caveat: the messages below were generated`),
	regexp.MustCompile(`(?i)^<task-notification>`),
}

// Every ResumeNotice/prepended-notice text the agent package glues to the
// front of a real prompt (see agent.go and router.go), stripped rather than
// rejected so a real correction after one is still captured on its own text.
var injectedPreamblePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)^\[SYSTEM: Session resumed after restart\..*?\]\s*`),
	regexp.MustCompile(`(?is)^\[SYSTEM: You were interrupted mid-turn by a restart\..*?\]\s*`),
	regexp.MustCompile(`(?is)^\[SYSTEM: The previous task on this channel was stopped by the user\..*?\]\s*`),
}

// stripInjectedPreamble removes one recognized harness-injected preamble from
// the front of prompt, if present, so every check that follows sees only what
// the user actually wrote.
func stripInjectedPreamble(prompt string) string {
	for _, pattern := range injectedPreamblePatterns {
		if loc := pattern.FindStringIndex(prompt); loc != nil && loc[0] == 0 {
			return prompt[loc[1]:]
		}
	}
	return prompt
}

// Openers that assign work. A brief starts by saying what to do.
var briefOpeners = []string{
	"add ", "build ", "can you ", "carry on", "continue ", "could you ", "do the following", "fix ",
	"go ahead", "have a look", "here's the plan", "heres the plan", "implement ", "look at ", "make ",
	"next:", "pick up ", "please ", "read ", "review ", "start ", "take a look", "trace ",
	"work through", "write ",
}

const (
	// softMatchWindow is how far into a prompt a soft trigger may sit and still
	// read as a complaint rather than a ground rule tacked onto a task brief.
	softMatchWindow = 300

	// briefLengthCutoff is the length past which a prompt reads as new work. A
	// correction is short and reactive.
	briefLengthCutoff = 800

	// promptLengthCutoff is the length past which a prompt is a paste of command
	// output rather than anything the user typed at us.
	promptLengthCutoff = 2000
)

var (
	explicitLogPattern = regexp.MustCompile(`(?i)(^|\s)!log\b`)

	// Quoted spans write about the marker rather than using it. Fences go first:
	// their three backticks do not pair up as inline spans, so a marker inside
	// one would otherwise escape.
	fencedBlockPattern  = regexp.MustCompile("(?s)```.*?```")
	backtickSpanPattern = regexp.MustCompile("`[^`]*`")
)

// lessonCapture queues the user's own words for a later retro. The only thing it
// puts into the model's context is what the marker means; judging stays out.
func lessonCapture() {
	p := readPayload()
	prompt := stripInjectedPreamble(p.Prompt)
	if prompt == "" || injectedPrompt(prompt) {
		pass()
	}

	notes := []string{}
	if explicitLogMarker(prompt) {
		queueFeedback(feedbackEntry{
			SessionID: p.SessionID,
			Kind:      KindUserCorrection,
			Trigger:   "!log",
			Detail:    prompt,
		})
		// The one judgement-free fact worth injecting. What `!log` means is a
		// fact about the tooling that the model otherwise infers, and infers
		// wrong; an opinion about the correction is what would bias the retro.
		notes = append(notes, "📝 Filed for the retro. `!log` means do not action, debate or write this up "+
			"now — say you have got it in one line, apply it from here on, and carry on with the task in hand.")
	} else if trigger := correctionTrigger(prompt); trigger != "" {
		queueFeedback(feedbackEntry{
			SessionID: p.SessionID,
			Kind:      KindUserCorrection,
			Trigger:   trigger,
			Detail:    prompt,
		})
	}

	if configDir := os.Getenv(memorylayout.EnvConfigDir); configDir != "" {
		if nudge := retroNudge(configDir); nudge != "" {
			notes = append(notes, nudge)
		}
	}
	if len(notes) == 0 {
		pass()
	}
	advise(advice{Event: eventUserPromptSubmit, Context: strings.Join(notes, "\n\n")})
}

// correctionTrigger returns the pattern that made a prompt read as pushback, or
// "" when it reads as new work.
func correctionTrigger(prompt string) string {
	if len(prompt) > promptLengthCutoff {
		// A paste this long is command output, not something typed at us.
		return ""
	}
	if trigger := firstMatch(hardCorrectionPatterns, prompt); trigger != "" {
		return trigger
	}
	// A soft match has to sit near the top either way: a correction opens with
	// the objection, while a brief that ends "do not touch the tests" is setting
	// ground rules for the work it just described.
	if trigger := firstMatchNear(complaintPatterns, prompt); trigger != "" {
		return trigger
	}
	if looksLikeTaskBrief(prompt) {
		return ""
	}
	return firstMatchNear(constraintPatterns, prompt)
}

// firstMatch returns the text of the first pattern the prompt matches.
func firstMatch(patterns []*regexp.Regexp, prompt string) string {
	for _, pattern := range patterns {
		if pattern.MatchString(prompt) {
			return pattern.String()
		}
	}
	return ""
}

// firstMatchNear is firstMatch restricted to the start of the prompt.
func firstMatchNear(patterns []*regexp.Regexp, prompt string) string {
	for _, pattern := range patterns {
		if at := pattern.FindStringIndex(prompt); at != nil && at[0] < softMatchWindow {
			return pattern.String()
		}
	}
	return ""
}

// looksLikeTaskBrief reports whether a prompt reads as new work. Deliberately
// conservative, since only a bare constraint is gated by it.
func looksLikeTaskBrief(prompt string) bool {
	trimmed := strings.TrimSpace(prompt)
	if len(trimmed) > briefLengthCutoff {
		return true
	}
	// Nothing here tests whitespace. A dictated or pasted brief arrives as one
	// unbroken line, so a rule keyed on newlines or paragraph counts waves it
	// through while wrapping a short complaint makes it look like a brief.
	low := strings.ToLower(trimmed)
	for _, opener := range briefOpeners {
		if strings.HasPrefix(low, opener) {
			return true
		}
	}
	return false
}

// explicitLogMarker reports whether the user used the marker, as opposed to
// writing about it.
func explicitLogMarker(prompt string) bool {
	unquoted := backtickSpanPattern.ReplaceAllString(fencedBlockPattern.ReplaceAllString(prompt, " "), " ")
	return explicitLogPattern.MatchString(unquoted)
}

// injectedPrompt reports whether the harness wrote the prompt rather than the user.
func injectedPrompt(prompt string) bool {
	trimmed := strings.TrimSpace(prompt)
	for _, pattern := range injectedPromptPatterns {
		if pattern.MatchString(trimmed) {
			return true
		}
	}
	return false
}
