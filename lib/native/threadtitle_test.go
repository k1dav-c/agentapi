package native

import "testing"

func TestTopicTitle(t *testing.T) {
	for _, tc := range []struct {
		prompt string
		want   string
	}{
		{"換個主題：檢查登入逾時\n請先看 server log", "檢查登入逾時"},
		{" New  task: \n\n  Review the billing API", "Review the billing API"},
		{"Let's switch to the billing API.", "the billing API."},
		{"新任務OAuth 憑證輪替", "OAuth 憑證輪替"},
		{"再補一個過期測試", ""},
		{"Also update the docs", ""},
		{"New taskforce needs a report", ""},
		{"Here is the output:\nnew topic: accidental title", ""},
		{"換個主題", ""},
	} {
		if got := explicitTopicTitle(tc.prompt); got != tc.want {
			t.Errorf("explicitTopicTitle(%q) = %q, want %q", tc.prompt, got, tc.want)
		}
	}
}

func TestTaskTopic(t *testing.T) {
	for _, prompts := range [][]string{
		{"Fix duplicate notification delivery", "Review notification delivery tests", "Implement OAuth token rotation"},
		{"修正通知重複發送", "重構通知接收流程", "實作登入逾時處理"},
	} {
		var topic TaskTopic
		for i, prompt := range prompts {
			want := ""
			if i == len(prompts)-1 {
				want = prompt
			}
			if got := topic.Observe(prompt); got != want {
				t.Errorf("Observe(%q) = %q, want %q", prompt, got, want)
			}
		}
	}
}
