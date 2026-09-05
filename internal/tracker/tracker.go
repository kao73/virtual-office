// Package tracker описывает трекер задач с точки зрения раннера: интерфейс,
// модель задачи, правило владения и протокол комментариев.
//
// Реализации живут в подпакетах (`tracker/mock`, `tracker/jira`). Всё, что
// одинаково для них, лежит здесь — иначе две реализации разъедутся в мелочах,
// а расхождение вылезет на живой доске.
//
// Про роли трекер не знает ничего: сопоставление «роль → статус» делает раннер
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

	// ErrNoProject — трекер не знает проекта, описанного в projects.yaml.
	// Это ошибка конфигурации, а не работы: раннер пропускает такой проект
	// и берётся за следующий, вместо того чтобы бросить весь цикл. Возвращать
	// её реализация обязана только тогда, когда уверена; молчаливое «задач нет»
	// тоже допустимо — так ведёт себя файловый трекер, которому проект
	// не отдельная сущность.
	ErrNoProject = errors.New("трекер не знает такого проекта")
)

// SkipUnknownProject — что сказать о проекте, которого трекер не знает, и стоит
// ли идти к следующему. Пустая строка и false означают, что беда не в этом.
//
// Формулировка одна на всех, кто обходит проекты, и живёт здесь, а не у каждого
// из них: `tick` такой проект пропускал, а `ls` на нём падал — одна причина, два
// разных поведения. Ответ на вопрос «что делать» тоже общий: пропустить и сказать
// вслух. Молчать нельзя — строка в projects.yaml выглядит рабочей, а задач по ней
// не видно, и человеку нужно знать почему.
func SkipUnknownProject(project string, err error) (string, bool) {
	if !errors.Is(err, ErrNoProject) {
		return "", false
	}
	return fmt.Sprintf("%s: проект описан в %s, но трекер его не знает — пропускаю",
		project, ProjectsFile), true
}

// Comment — комментарий к задаче. Агентские отличаются от человеческих учёткой
// автора, а не текстом: см. Whoami, accounts и also_agents
// в ${OFFICE_HOME}/tracker.yaml.
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
	// DependsOn — ключи задач, от которых зависит эта (LinkDependsOn).
	// Пишется этой волной, не читается никаким кодом Change 1 — гейт
	// очерёдности по этому полю добавит Change 2.
	DependsOn []string

	// Поля аренды. Owner — человекочитаемый владелец (имя роли), RunID — то,
	// по чему сверяется право на мутацию.
	Owner      string
	RunID      string
	LeaseUntil time.Time

	Attempts  int
	HumanFlag bool

	Comments []Comment
}

// TaskRef — задача в списке: всё, кроме переписки.
//
// Переписки здесь нет намеренно, и это не «Task с пустым Comments»: комментарии
// тянутся отдельным запросом на задачу, и притвориться, что их просто нет,
// значило бы соврать тихо. Кому нужна история — берёт Get.
type TaskRef struct {
	Key     string
	Project string
	Summary string
	Status  string

	// Поля аренды: по ним видно, кто работает над задачей прямо сейчас.
	Owner      string
	RunID      string
	LeaseUntil time.Time

	Attempts  int
	HumanFlag bool

	// Updated — когда задачу трогали в последний раз. Нужен `ls`, чтобы показать
	// возраст: задача, висящая в статусе неделю, — то, что человек ищет глазами.
	Updated time.Time
}

// Ref — та же задача без переписки.
func (t Task) Ref() TaskRef {
	return TaskRef{
		Key: t.Key, Project: t.Project, Summary: t.Summary, Status: t.Status,
		Owner: t.Owner, RunID: t.RunID, LeaseUntil: t.LeaseUntil,
		Attempts: t.Attempts, HumanFlag: t.HumanFlag,
	}
}

// LeaseAlive — жива ли аренда на момент now.
//
// Аренда — lease, а не lock: истёкшая считается свободной, иначе смерть раннера
// заперла бы задачу навсегда. Запись без run_id арендой не является вовсе:
// сверять право на мутацию было бы не с чем.
func (t Task) LeaseAlive(now time.Time) bool { return t.Ref().LeaseAlive(now) }

// LeaseAlive — жива ли аренда на момент now.
func (t TaskRef) LeaseAlive(now time.Time) bool {
	return t.RunID != "" && now.Before(t.LeaseUntil)
}

// TaskInput — данные для создания новой задачи. Отдельный тип, а не Task
// целиком: у только что создаваемой задачи нет ни ключа, ни аренды, ни
// статуса — их назначает сам трекер.
type TaskInput struct {
	Summary     string
	Description string
	Labels      []string
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

// ClaimRequest — заявка на захват задачи.
//
// Рабочий статус входит в заявку потому, что захват — одна операция, а не две:
// в JIRA это `PUT fields` + `POST transitions` + сверка, и разрывать её нельзя,
// иначе между записью аренды и переводом остаётся состояние, которого граф
// не описывает.
type ClaimRequest struct {
	Key        string
	RunID      string
	Owner      string // человекочитаемый владелец: имя роли
	LeaseUntil time.Time

	// ExpectStatus — статус, в котором задача должна быть сейчас. Захват задачи,
	// успевшей уехать в другой статус, не наш: ErrClaimLost.
	ExpectStatus string
	// WorkingStatus — куда перевести задачу, захватив.
	WorkingStatus string
}

// Tracker — всё, что раннеру нужно от трекера задач.
//
// Методы, меняющие задачу, принимают Actor и обязаны проверять право через
// CheckOwner. Метод, ничего не меняющий, актора не требует.
type Tracker interface {
	// Whoami — учётка, под которой ходит сам раннер. Всё, написанное не ею
	// и не другими агентскими учётками (accounts и also_agents
	// в ${OFFICE_HOME}/tracker.yaml), считается словами человека.
	Whoami() (string, error)

	// ListReady — кандидаты в статусе проекта: без живой аренды, отсортированы
	// так, как решает реализация (приоритет, дата).
	ListReady(project, status string) ([]TaskRef, error)

	// ListExpired — задачи проекта с истёкшей арендой; сырьё для reaper.
	ListExpired(project string, now time.Time) ([]TaskRef, error)

	// List — задачи проекта в названных статусах, как есть: и свободные,
	// и захваченные. Это не очередь, а доска — тем и отличается от ListReady,
	// который живую аренду отбрасывает. Переписку не тянет: она стоит запроса
	// на задачу, а показывать её `ls` всё равно негде.
	List(project string, statuses []string) ([]TaskRef, error)

	// Get — задача целиком, включая все комментарии: резать их по маркеру
	// будет раннер.
	Get(key string) (Task, error)

	// Claim — захват: записать владельца, run_id и срок аренды, перевести
	// в рабочий статус и перечитать. Если после перечитывания владелец не мы —
	// ErrClaimLost, ничего не откатывая.
	Claim(req ClaimRequest) error

	// Renew — продление своей аренды. Продлевает только прогон и только живую:
	// продлевать истёкшую поздно, её уже мог забрать другой.
	Renew(key, runID string, leaseUntil time.Time) error

	// Release — снять аренду, не трогая статус. Актор нужен потому, что снимает
	// её не только сам прогон: reaper снимает чужую истёкшую как системная операция.
	Release(key string, by Actor) error

	// Transition — сменить статус.
	Transition(key string, by Actor, toStatus string) error

	// Comment — написать комментарий по протоколу (см. marker.go).
	Comment(key string, by Actor, body string) error

	// SetHumanFlag — выставить или снять атрибут «ждёт человека».
	SetHumanFlag(key string, by Actor, on bool) error

	// SetAttempts — записать счётчик попыток.
	SetAttempts(key string, by Actor, n int) error

	// CreateTask заводит новую задачу. Без Actor: создавать нечего "владеть" —
	// как у Add в mock (не из контракта) и List/ListReady в самом контракте.
	CreateTask(project string, input TaskInput) (TaskRef, error)

	// FindByMarker — задачи проекта с данной меткой. Источник идемпотентности
	// пакетного создания: спрашивает трекер, не хранимую запись о нём.
	FindByMarker(project, marker string) ([]TaskRef, error)

	// AddAttachment сохраняет сырые данные вложением к существующей, уже
	// захваченной задаче — Actor и CheckOwner нужны, как у Comment.
	AddAttachment(key string, by Actor, name string, data []byte) (id string, err error)

	// GetAttachment читает вложение обратно. Без Actor — как Get, чтение
	// не требует владения.
	GetAttachment(key, id string) ([]byte, error)

	// LinkDependsOn связывает только что созданную задачу (key) с её
	// зависимостью (dependsOnKey). by обычно BySystem() — тем же приёмом,
	// что reap и разбор ответа человека используют для мутаций вне аренды
	// какой-либо роли.
	LinkDependsOn(key, dependsOnKey string, by Actor) error
}

// WorkflowCheck — что раннер узнал о workflow проекта, заглянув в трекер.
type WorkflowCheck struct {
	// Sample — задача, на которой проверяли. Пусто означает, что проверять было
	// не на чем: переходы трекер показывает только у конкретной задачи, и без
	// задачи в рабочем статусе спросить нечего.
	Sample string
	// SelfEntry — в рабочий статус есть переход из него самого. Тогда захват
	// не становится CAS даже в workflow, и двое могут уйти работать над одной
	// задачей: см. «Сколько раннеров на проект» в контракте.
	SelfEntry bool
}

// WorkflowChecker — трекер, способный рассказать о собственном workflow.
//
// Интерфейс необязательный и в Tracker не входит: реализует его только jira,
// а у файлового трекера workflow нет вовсе. Раннер спрашивает того, кто умеет
// ответить, и молчит об остальных.
type WorkflowChecker interface {
	// CheckWorkflow отвечает, допускает ли workflow проекта вход в рабочий
	// статус из него самого.
	CheckWorkflow(project, workingStatus string) (WorkflowCheck, error)
}
