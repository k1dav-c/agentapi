package msgfmt

import (
	"regexp"
	"strings"
)

// profile is how msgfmt reads one agent's terminal UI. A field left zero
// takes the generic behavior (see profileOf). Every agent type has a
// profile here: the exhaustive linter checks the map.
type profile struct {
	// removeInputBox removes the agent's input box from a screen; the
	// generic one finds a box drawn with ─ │ and rounded corners.
	removeInputBox func(message string) string
	// shownInput is the user's message as the agent echoes it.
	shownInput func(userInput string) string
	// afterInput moves past what the agent shows right after the echoed
	// message (the bottom of an input box, a timestamp), from the index of
	// the message's last line.
	afterInput func(lines []string, last int) int
	// ready reports whether a screen shows the agent ready for the initial
	// prompt; by default, when removeInputBox finds an input box.
	ready func(screen string) bool
	// inputEnd returns the index one past the agent's input box and what
	// belongs to it (a draft that spans lines), or -1 without one. Nil
	// for agents whose box isn't looked for (HasInputBox says so).
	inputEnd func(lines []string) int
	// reportTaskCalls removes the coder MCP server's report_task tool
	// calls from a message, and returns them.
	reportTaskCalls func(message string) (string, []string)
	// statusLines match the lines the agent redraws while it works (see
	// status_line.go); statusFollower a line that goes with one, right
	// after it; timers the elapsed time it appends to a running tool's
	// lines.
	statusLines    []*regexp.Regexp
	statusFollower *regexp.Regexp
	timers         *regexp.Regexp
}

var profiles = map[AgentType]profile{
	AgentTypeClaude: {
		removeInputBox:  removeClaudeMessageBox,
		ready:           isClaudeAgentReadyForInitialPrompt,
		inputEnd:        belowBox(findClaudeInputBox),
		reportTaskCalls: removeClaudeReportTaskToolCall,
		// "✻ Fermenting… (6s · ↓ 80 tokens)", "· Fermenting… (esc to
		// interrupt)", and before its timer shows, just "· Contemplating…"
		statusLines: []*regexp.Regexp{
			regexp.MustCompile(`^\s*[·✢✳✶✻✽*]\s+\S[^()]*…\s*\((?:\d+[smh]\b|[^)]*esc to interrupt)[^)]*\)\s*$`),
			regexp.MustCompile(`^\s*[·✢✳✶✻✽*]\s+[A-Z][a-z]+…\s*$`),
		},
		statusFollower: claudeSpinnerTipRe,
		timers:         claudeToolTimerRe,
	},
	AgentTypeCodex: {
		removeInputBox: removeCodexMessageBox,
		inputEnd: func(lines []string) int {
			if idx := findCodexInputLine(lines); idx != -1 {
				return codexComposerEnd(lines, idx)
			}
			return -1
		},
		reportTaskCalls: removeCodexReportTaskToolCall,
		// "◦ Working (19s • esc to interrupt) · 1 background terminal running · /ps to view…"
		statusLines: []*regexp.Regexp{regexp.MustCompile(`^\s*[•◦]\s+Working\s+\(\d+[smh][^)]*\).*$`)},
	},
	AgentTypePi: {
		removeInputBox: removePiMessageBox,
		// Pi drops the inline Markdown markers when it shows the message.
		shownInput: piMarkdownMarkers.Replace,
		ready: func(screen string) bool {
			top, _ := findPiInputBox(strings.Split(screen, "\n"))
			return top != -1
		},
		inputEnd: belowBox(findPiInputBox),
		// "── ⠼ Working ────", and "Elapsed 2.0s" under a command that is
		// running
		statusLines: []*regexp.Regexp{
			regexp.MustCompile(`^\s*─*\s*[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]\s+Working\b.*$`),
			regexp.MustCompile(`^\s*Elapsed \d+(?:\.\d+)?s\s*$`),
		},
	},
	AgentTypeOpencode: {
		removeInputBox: removeOpencodeMessageBox,
		// The two lines after the message are in its box:
		//   ┃  jkmr (08:46 PM)                                                     ┃
		//   ┃                                                                      ┃
		afterInput: func(lines []string, last int) int {
			if last+2 < len(lines) {
				return last + 2
			}
			return last
		},
	},
	AgentTypeAmp:     {removeInputBox: removeAmpMessageBox},
	AgentTypeGemini:  {afterInput: boxBottom("╯", "╰")},
	AgentTypeCopilot: {afterInput: boxBottom("╯", "╰")},
	AgentTypeCursor:  {afterInput: boxBottom("┘", "└")},
	AgentTypeGoose:   {},
	AgentTypeAider:   {},
	AgentTypeAuggie:  {},
	AgentTypeAmazonQ: {},
	AgentTypeKimi:    {},
	AgentTypeCustom:  {},
}

// profileOf returns agentType's profile with the generic behavior filled
// in, and whether msgfmt knows the agent type.
func profileOf(agentType AgentType) (profile, bool) {
	p, known := profiles[agentType]
	if p.removeInputBox == nil {
		p.removeInputBox = removeMessageBox
	}
	if p.shownInput == nil {
		p.shownInput = func(userInput string) string { return userInput }
	}
	if p.ready == nil {
		removeInputBox := p.removeInputBox
		p.ready = func(screen string) bool {
			screen = trimEmptyLines(screen)
			return len(removeInputBox(screen)) != len(screen)
		}
	}
	if p.reportTaskCalls == nil {
		p.reportTaskCalls = func(message string) (string, []string) { return message, []string{} }
	}
	return p, known
}

// belowBox makes an inputEnd from a function that finds an input box's top
// and bottom lines.
func belowBox(find func(lines []string) (top, bottom int)) func(lines []string) int {
	return func(lines []string) int {
		if _, bottom := find(lines); bottom != -1 {
			return bottom + 1
		}
		return -1
	}
}

// boxBottom makes an afterInput for an agent that echoes the message in a
// box whose bottom line has the markers.
func boxBottom(markers ...string) func(lines []string, last int) int {
	return func(lines []string, last int) int {
		if idx, found := skipTrailingInputBoxLine(lines, last, markers...); found {
			return idx
		}
		return last
	}
}
