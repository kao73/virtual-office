package tracker

import (
	"errors"
	"slices"
	"testing"
	"time"
)

var now = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

// leased — задача с арендой, живой или истёкшей.
func leased(runID string, until time.Time) Task {
	return Task{Key: "OFF-1", Project: "OFF", Status: "In Progress", Owner: "implementer", RunID: runID, LeaseUntil: until}
}

// Нулевой Actor не должен молча оказаться системным: системная операция — это
// осознанный выбор, а не забытое поле.
func TestActorRequiresExplicitKind(t *testing.T) {
	if err := (Actor{}).Validate(); err == nil {
		t.Error("нулевой актор признан годным")
	}
	if err := ByRun("").Validate(); err == nil {
		t.Error("прогон без run_id признан годным")
	}
	if err := ByRun("id").Validate(); err != nil {
		t.Errorf("прогон с run_id отвергнут: %v", err)
	}
	if err := BySystem().Validate(); err != nil {
		t.Errorf("системная операция отвергнута: %v", err)
	}
	if ByRun("id").IsSystem() {
		t.Error("прогон принят за системную операцию")
	}
	if !BySystem().IsSystem() {
		t.Error("системная операция не распознана")
	}
}

// Правило владения — то место, где этап 2 ломается тише всего. Прогон, доживший
// до конца после reap, обязан получить отказ, иначе задача побывает в Ready
// и в Review за один запуск, а то и обзаведётся двумя владельцами.
func TestCheckOwner(t *testing.T) {
	alive := now.Add(10 * time.Minute)
	expired := now.Add(-time.Minute)

	cases := []struct {
		name string
		task Task
		who  Actor
		want error
	}{
		{"прогон со своей живой арендой", leased("наш", alive), ByRun("наш"), nil},
		{"прогон с чужой живой арендой", leased("чужой", alive), ByRun("наш"), ErrNotOwner},
		{"прогон со своей истёкшей арендой", leased("наш", expired), ByRun("наш"), ErrNotOwner},
		// Для прогона «аренды нет» означает «её отобрали»: reap уже вернул задачу,
		// и её мог взять кто-то другой.
		{"прогон без аренды", Task{Key: "OFF-1"}, ByRun("наш"), ErrNotOwner},

		{"системная операция без аренды", Task{Key: "OFF-1"}, BySystem(), nil},
		{"системная операция при истёкшей аренде", leased("чужой", expired), BySystem(), nil},
		{"системная операция при живой аренде", leased("чужой", alive), BySystem(), ErrNotOwner},

		{"нулевой актор", leased("наш", alive), Actor{}, ErrNotOwner},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckOwner(tc.task, tc.who, now)
			if !errors.Is(err, tc.want) {
				t.Errorf("получено %v, ожидалось %v", err, tc.want)
			}
		})
	}
}

// Аренда — lease, а не lock: истёкшая считается свободной. Запись без run_id
// арендой не является вовсе, чем бы ни было заполнено поле срока.
func TestLeaseAlive(t *testing.T) {
	cases := []struct {
		name string
		task Task
		want bool
	}{
		{"живая", leased("id", now.Add(time.Minute)), true},
		{"истёкшая", leased("id", now.Add(-time.Minute)), false},
		{"срок ровно сейчас", leased("id", now), false},
		{"без run_id", leased("", now.Add(time.Minute)), false},
		{"без срока", Task{RunID: "id"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.task.LeaseAlive(now); got != tc.want {
				t.Errorf("LeaseAlive = %v, ожидалось %v", got, tc.want)
			}
		})
	}
}

// TestTaskRefCopiesDependsOn доказывает, что Ref() отдаёт зависимости
// так же, как Task: гейт очерёдности (internal/pipeline.claim()) и
// видимость в runner ls читают DependsOn из TaskRef, полученного через
// ListReady/List, а не через Get() — без этого поля в Ref() оба пути
// видели бы кандидата без единой зависимости, даже когда Task.DependsOn
// на нём заполнен.
func TestTaskRefCopiesDependsOn(t *testing.T) {
	task := Task{Key: "OFF-2", DependsOn: []string{"OFF-1", "OFF-0"}}
	ref := task.Ref()
	if !slices.Equal(ref.DependsOn, []string{"OFF-1", "OFF-0"}) {
		t.Errorf("TaskRef.DependsOn = %v, ожидалось [OFF-1 OFF-0]", ref.DependsOn)
	}
}

func TestTaskRefDependsOnEmptyWhenTaskHasNone(t *testing.T) {
	ref := Task{Key: "OFF-1"}.Ref()
	if len(ref.DependsOn) != 0 {
		t.Errorf("TaskRef.DependsOn = %v, ожидался пустой список", ref.DependsOn)
	}
}
