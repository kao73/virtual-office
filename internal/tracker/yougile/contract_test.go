package yougile

import (
	"errors"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

// coreTracker — подмножество tracker.Tracker, которое реализует этот change:
// сигнатуры скопированы из internal/tracker/tracker.go дословно. Полная
// проверка `var _ tracker.Tracker = (*Tracker)(nil)` не скомпилируется, пока
// yougile-dependencies-attachments не добавит LinkDependsOn/AddAttachment/
// GetAttachment, — а расхождение сигнатур ловить надо уже сейчас.
type coreTracker interface {
	Whoami() (string, error)
	ListReady(project, status string) ([]tracker.TaskRef, error)
	ListExpired(project string, now time.Time) ([]tracker.TaskRef, error)
	List(project string, statuses []string) ([]tracker.TaskRef, error)
	Get(key string) (tracker.Task, error)
	Claim(req tracker.ClaimRequest) error
	Renew(key, runID string, leaseUntil time.Time) error
	Release(key string, by tracker.Actor) error
	Transition(key string, by tracker.Actor, toStatus string) error
	Comment(key string, by tracker.Actor, body string) error
	SetHumanFlag(key string, by tracker.Actor, on bool) error
	SetAttempts(key string, by tracker.Actor, n int) error
	CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error)
	FindByMarker(project, marker string) ([]tracker.TaskRef, error)
}

var _ coreTracker = (*Tracker)(nil)

// Правило владения общее для всех трекеров: ни одна мутация не проходит
// ни у системной операции поверх живой аренды, ни у прогона без аренды,
// и отказ случается до записи.
func TestEveryMutationFollowsOwnership(t *testing.T) {
	mutations := map[string]func(*Tracker, tracker.Actor) error{
		"Release":       func(tr *Tracker, a tracker.Actor) error { return tr.Release(testKey, a) },
		"Transition":    func(tr *Tracker, a tracker.Actor) error { return tr.Transition(testKey, a, "Review") },
		"Comment":       func(tr *Tracker, a tracker.Actor) error { return tr.Comment(testKey, a, "x") },
		"SetHumanFlag":  func(tr *Tracker, a tracker.Actor) error { return tr.SetHumanFlag(testKey, a, true) },
		"SetAttempts":   func(tr *Tracker, a tracker.Actor) error { return tr.SetAttempts(testKey, a, 1) },
		"LinkDependsOn": func(tr *Tracker, a tracker.Actor) error { return tr.LinkDependsOn(testKey, "task-dep", a) },
		"AddAttachment": func(tr *Tracker, a tracker.Actor) error {
			_, err := tr.AddAttachment(testKey, a, "x.txt", []byte("x"))
			return err
		},
	}
	for name, mutate := range mutations {
		t.Run(name+"/система поверх живой аренды", func(t *testing.T) {
			tr, fake := fixture(t)
			fake.setLease(testKey, "run-1", now.Add(time.Minute))
			if err := mutate(tr, tracker.BySystem()); !errors.Is(err, tracker.ErrNotOwner) {
				t.Errorf("дало %v", err)
			}
			if len(fake.puts)+len(fake.chatPosts)+len(fake.uploads) != 0 {
				t.Error("записано без права")
			}
		})
		t.Run(name+"/прогон без аренды", func(t *testing.T) {
			tr, fake := fixture(t)
			if err := mutate(tr, tracker.ByRun("run-1")); !errors.Is(err, tracker.ErrNotOwner) {
				t.Errorf("дало %v", err)
			}
			if len(fake.puts)+len(fake.chatPosts)+len(fake.uploads) != 0 {
				t.Error("записано без права")
			}
		})
		t.Run(name+"/актор не задан", func(t *testing.T) {
			tr, _ := fixture(t)
			if err := mutate(tr, tracker.Actor{}); !errors.Is(err, tracker.ErrNotOwner) {
				t.Errorf("нулевой актор дал %v", err)
			}
		})
	}
}
