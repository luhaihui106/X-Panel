package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

// Register jobs with the runtime's actual parser, but never start the scheduler.
func TestTelegramCronDocumentation(t *testing.T) {
	source, err := os.ReadFile("../web/web.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "cron.New(cron.WithLocation(loc), cron.WithSeconds())") {
		t.Fatal("runtime scheduler options changed; review documentation parser contract")
	}
	doc, err := os.ReadFile("telegram-admin-bot.md")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, candidate := range strings.Split(string(doc), "\n") {
		if strings.HasPrefix(candidate, "- 摘要使用 `tgRunTime`") {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatal("missing summary schedule guidance")
	}
	if strings.Contains(line, "五字段 cron") || strings.Contains(line, "不含秒") {
		t.Error("guidance contradicts the six-field runtime parser")
	}
	for _, marker := range []string{"默认 `@daily`", "六字段 cron", "含秒", "读取失败或空值回退每日", "非空但语法错误会注册失败", "不能承诺自动回退"} {
		if !strings.Contains(line, marker) {
			t.Errorf("missing scheduling boundary: %s", marker)
		}
	}
	var specs []string
	for _, match := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(line, -1) {
		if match[1] == "@daily" || strings.Contains(match[1], " ") {
			specs = append(specs, match[1])
		}
	}
	want := []string{"@daily", "0 0 8 * * *", "0 0 8 * * 1", "0 0 8 1 * *"}
	if len(specs) != len(want) {
		t.Fatalf("schedule example count = %d, want %d", len(specs), len(want))
	}
	from := time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC)
	next := []time.Time{
		time.Date(2026, time.January, 3, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.January, 3, 8, 0, 0, 0, time.UTC),
		time.Date(2026, time.January, 5, 8, 0, 0, 0, time.UTC),
		time.Date(2026, time.February, 1, 8, 0, 0, 0, time.UTC),
	}
	for i, spec := range specs {
		t.Run(spec, func(t *testing.T) {
			scheduler := cron.New(cron.WithLocation(time.UTC), cron.WithSeconds())
			id, err := scheduler.AddFunc(spec, func() { t.Error("scheduler must not execute jobs") })
			if err != nil {
				t.Fatalf("documented expression rejected by runtime parser: %v", err)
			}
			if spec != want[i] {
				t.Errorf("expression = %q, want %q", spec, want[i])
			}
			if got := scheduler.Entry(id).Schedule.Next(from); !got.Equal(next[i]) {
				t.Errorf("next occurrence = %s, want %s", got, next[i])
			}
		})
	}
}

func TestTelegramCronRejectsFiveFields(t *testing.T) {
	for _, spec := range []string{"0 8 * * *", "0 8 * * 1", "0 8 1 * *"} {
		t.Run(spec, func(t *testing.T) {
			scheduler := cron.New(cron.WithLocation(time.UTC), cron.WithSeconds())
			if _, err := scheduler.AddFunc(spec, func() { t.Error("scheduler must not execute jobs") }); err == nil {
				t.Fatal("invalid nonempty five-field expression unexpectedly registered")
			} else {
				t.Logf("expected registration failure: %v", err)
			}
			if len(scheduler.Entries()) != 0 {
				t.Fatal("invalid expression registered a job; no @daily fallback expected")
			}
		})
	}
}
