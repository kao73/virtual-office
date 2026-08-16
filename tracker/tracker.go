// Package tracker описывает трекер задач с точки зрения раннера: интерфейс,
// модель задачи, правило владения и протокол комментариев.
//
// Реализации живут в подпакетах (`tracker/mock`, `tracker/jira`). Всё, что
// одинаково для них, лежит здесь — иначе две реализации разъедутся в мелочах,
// а расхождение вылезет на живой доске.
//
// Про роли трекер не знает ничего: сопоставление «роль → колонка» делает раннер
// по `workflow.yaml`. Трекеру достаются проект и статус.
package tracker

import (
	"errors"
	"fmt"
	"time"
)

// Ошибки контракта. Реализации обязаны возвращать именно их, обёрнутыми через %w.
var (
	// ErrClaimLost — захват не удался: после записи владельцем оказались не мы.
	// Откатывать ничего не нужно, задачу забрал кто-то другой.
	ErrClaimLost = errors.New("захват потерян: задачей владеет другой прогон")

	// ErrNotOwner — у актора нет права менять эту задачу, см. CheckOwner.
	ErrNotOwner = errors.New("нет права менять задачу")

	// ErrNotFound — такой задачи в трекере нет.
	ErrNotFound = errors.New("задача не найдена")
)

// Comment — комментарий к задаче. Агентские отличаются от человеческих учёткой
// автора, а не текстом: см. Whoami и agent_accounts в tracker.yaml.
type Comment struct {
	ID      string
	Author  string
	Created time.Time
	Body    string
}

// Task — задача со всем, что нужно раннеру для решения.
type Task struct {
	Key         string
	Project     string
	Summary     string
	Description string
	Status      string
	Labels      []string

	// Поля аренды. Owner — человекочитаемый владелец (имя роли), RunID — то,
	// по чему сверяется право на мутацию.
	Owner      string
	RunID      string
	LeaseUntil time.Time

	Attempts  int
	HumanFlag bool

	Comments []Comment
}

// TaskRef — задача в списке кандидатов: столько, сколько нужно раннеру, чтобы
// выбрать одну и не тянуть остальные целиком.
type TaskRef struct {
	Key      string
	Project  string
	Status   string
	Attempts int
}

// LeaseAlive — жива ли аренда на момент now.
//
// Аренда — lease, а не lock: истёкшая считается свободной, иначе смерть раннера
// заперла бы задачу навсегда. Запись без run_id арендой не является вовсе:
// сверять право на мутацию было бы не с чем.
func (t Task) LeaseAlive(now time.Time) bool {
	return t.RunID != "" && now.Before(t.LeaseUntil)
}

// Actor — от чьего имени идёт мутация. Видов ровно два, и они противоположны
// по требованию к аренде, поэтому нулевое значение не годится ни в один:
// забытое поле должно ломаться громко, а не молча становиться системным.
type Actor struct {
	runID  string
	system bool
}

// ByRun — актор-прогон: у него есть run_id, и он требует свою живую аренду.
func ByRun(runID string) Actor { return Actor{runID: runID} }

// BySystem — системная операция (reap, разбор ответа человека). Аренды у неё нет,
// и она требует, чтобы живой аренды не было и у задачи.
func BySystem() Actor { return Actor{system: true} }

// RunID — идентификатор прогона; у системной операции пуст.
func (a Actor) RunID() string { return a.runID }

// IsSystem — системная ли это операция.
func (a Actor) IsSystem() bool { return a.system }

// Validate отвергает актора, вид которого не задан.
func (a Actor) Validate() error {
	if a.system {
		return nil
	}
	if a.runID == "" {
		return errors.New("актор не задан: нужен либо run_id прогона, либо системная операция")
	}
	return nil
}

func (a Actor) String() string {
	if a.system {
		return "системная операция"
	}
	if a.runID == "" {
		return "актор не задан"
	}
	return "прогон " + a.runID
}

// CheckOwner решает, вправе ли актор менять задачу. Правило общее для всех
// реализаций, поэтому живёт здесь: разъехавшись, они дали бы гонку, которую
// не поймать тестами одной из них.
//
//   - прогон вправе, только если аренда его и живая. «Аренды нет» для прогона
//     означает «её отобрали»: reap уже вернул задачу, и её мог взять другой tick;
//   - системная операция вправе, только если живой аренды нет. Иначе она перебила бы
//     работающий прогон.
//
// Так зависший прогон, доживший до конца после reap, ничего в трекер не пишет,
// а reap и разбор ответа человека обходятся без захвата.
func CheckOwner(t Task, a Actor, now time.Time) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrNotOwner, err)
	}

	alive := t.LeaseAlive(now)
	switch {
	case a.IsSystem() && alive:
		return fmt.Errorf("%w: %s не может тронуть %s — аренда прогона %s жива до %s",
			ErrNotOwner, a, t.Key, t.RunID, t.LeaseUntil.Format(time.RFC3339))
	case a.IsSystem():
		return nil
	case !alive:
		return fmt.Errorf("%w: у %s нет живой аренды на %s — её отобрали", ErrNotOwner, a, t.Key)
	case t.RunID != a.RunID():
		return fmt.Errorf("%w: %s владеет %s", ErrNotOwner, t.RunID, t.Key)
	}
	return nil
}

// Tracker — всё, что раннеру нужно от трекера задач.
//
// Методы, меняющие задачу, принимают Actor и обязаны проверять право через
// CheckOwner. Метод, ничего не меняющий, актора не требует.
type Tracker interface {
	// Whoami — учётка, под которой ходит сам раннер. Всё, написанное не ею
	// и не другими учётками из agent_accounts, считается словами человека.
	Whoami() (string, error)

	// ListReady — кандидаты в колонке проекта: без живой аренды, отсортированы
	// так, как решает реализация (приоритет, дата).
	ListReady(project, status string) ([]TaskRef, error)

	// ListExpired — задачи проекта с истёкшей арендой; сырьё для reaper.
	ListExpired(project string, now time.Time) ([]TaskRef, error)

	// Get — задача целиком, включая все комментарии: резать их по маркеру
	// будет раннер.
	Get(key string) (Task, error)

	// Claim — захват: записать владельца, run_id и срок аренды, перевести
	// в рабочий статус и перечитать. Если после перечитывания владелец не мы —
	// ErrClaimLost, ничего не откатывая. expectStatus — статус, в котором задача
	// должна была быть: захват задачи, уже уехавшей в другую колонку, не наш.
	Claim(key, runID, owner string, leaseUntil time.Time, expectStatus string) error

	// Renew — продление своей аренды.
	Renew(key, runID string, leaseUntil time.Time) error

	// Release — снять аренду, не трогая статус.
	Release(key, runID string) error

	// Transition — сменить колонку.
	Transition(key string, by Actor, toStatus string) error

	// Comment — написать комментарий по протоколу (см. marker.go).
	Comment(key string, by Actor, body string) error

	// SetHumanFlag — выставить или снять атрибут «ждёт человека».
	SetHumanFlag(key string, by Actor, on bool) error

	// SetAttempts — записать счётчик попыток.
	SetAttempts(key string, by Actor, n int) error
}
