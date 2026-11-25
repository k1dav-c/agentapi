package native

import (
	"slices"
	"strings"
	"unicode"
)

// TaskTopic tracks the main task across prompts in one agent session.
type TaskTopic struct {
	title string
}

// Reset starts tracking a new session, optionally with its existing title.
func (t *TaskTopic) Reset(title string) { t.title = title }

// Observe returns a title for an explicit pivot or a standalone task with
// a different subject. Follow-ups leave the current task as the baseline.
func (t *TaskTopic) Observe(prompt string) string {
	if title := explicitTopicTitle(prompt); title != "" {
		t.title = title
		return title
	}
	line := PromptTitle(prompt)
	if line == "" {
		return ""
	}
	if t.title == "" {
		t.title = line
		return ""
	}
	subject := taskSubject(line)
	if subject == "" {
		return ""
	}
	previous := taskSubject(t.title)
	if previous == "" {
		previous = t.title
	}
	oldTerms, newTerms := topicTerms(previous), topicTerms(subject)
	if len(oldTerms) == 0 || len(newTerms) < 2 {
		return ""
	}
	for term := range newTerms {
		if oldTerms[term] {
			return ""
		}
	}
	t.title = line
	return line
}

// PromptTitle uses the first nonblank line of a prompt as a session title.
func PromptTitle(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	name := strings.Join(strings.Fields(line), " ")
	const limit = 60
	if runes := []rune(name); len(runes) > limit {
		name = strings.TrimSpace(string(runes[:limit-1])) + "…"
	}
	return name
}

// explicitTopicTitle returns the task that follows a topic change phrase.
// Looking only at the opening of the prompt avoids treating quoted logs or
// ordinary follow-up requests as a topic change.
func explicitTopicTitle(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	line, rest, _ := strings.Cut(prompt, "\n")
	line = strings.Join(strings.Fields(line), " ")
	for _, prefix := range topicPrefixes {
		remainder, ok := cutTopicPrefix(line, prefix)
		if !ok {
			continue
		}
		task := strings.TrimLeftFunc(remainder, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune(":：,，.。;；—–-", r)
		})
		if task == "" {
			task = rest
		}
		return PromptTitle(task)
	}
	return ""
}

func cutTopicPrefix(line, prefix string) (string, bool) {
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", false
	}
	rest := line[len(prefix):]
	if rest != "" && prefix[0] < unicode.MaxASCII && isASCIIWord(rest[0]) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

func isASCIIWord(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_'
}

func taskSubject(line string) string {
	for _, prefix := range []string{"please", "can you", "could you", "請幫我", "幫我", "請"} {
		if rest, ok := cutTopicPrefix(line, prefix); ok {
			line = rest
			break
		}
	}
	for _, verb := range taskVerbs {
		if subject, ok := cutTopicPrefix(line, verb); ok {
			for _, reference := range []string{"it", "this", "that", "the same", "same", "這", "那個", "剛才", "上面", "前面", "原本"} {
				if _, referenced := cutTopicPrefix(subject, reference); referenced {
					return ""
				}
			}
			return subject
		}
	}
	return ""
}

// topicTerms compares words in Latin text and adjacent Han characters in
// unsegmented Chinese text, so the same subject can appear in a new phrase.
func topicTerms(text string) map[string]bool {
	terms := map[string]bool{}
	var prev rune
	var word strings.Builder
	flush := func() {
		if w := word.String(); len(w) > 1 && !slices.Contains(genericTopicWords, w) {
			terms[strings.TrimSuffix(w, "s")] = true
		}
		word.Reset()
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			if prev != 0 {
				terms[string([]rune{prev, r})] = true
			}
			prev = r
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
			prev = 0
		default:
			flush()
			prev = 0
		}
	}
	flush()
	return terms
}

var topicPrefixes = []string{
	"let's switch topics", "let’s switch topics", "let's switch to", "let’s switch to",
	"let's move on to", "let’s move on to", "switch topics", "switching topics",
	"switch gears", "changing topics", "change of topic", "new topic", "new task", "next task",
	"接下來改做", "現在改做", "接下來換做", "換個主題", "換個話題", "換一個主題", "換一個話題",
	"下一個任務", "新的任務", "另外一個問題", "新主題", "新任務", "改做", "換做",
}

var taskVerbs = []string{
	"audit", "build", "debug", "deploy", "design", "fix", "implement", "investigate",
	"migrate", "refactor", "remove", "replace", "review", "rewrite", "set up", "update",
	"修正", "修復", "實作", "重構", "調查", "排查", "檢查", "部署", "設計", "移除", "遷移", "重寫", "更新",
}

var genericTopicWords = []string{
	"again", "an", "and", "another", "changes", "code", "current", "details", "docs",
	"documentation", "existing", "for", "from", "implementation", "in", "issue", "it",
	"of", "on", "please", "problem", "same", "test", "tests", "that", "the", "this", "to", "unit", "with",
}
