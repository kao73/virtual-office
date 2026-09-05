package mock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
)

var now = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// fixture — трекер с одной задачей в Ready и остановленными часами.
func fixture(t *testing.T) *Tracker {
	t.Helper()
	tr := New(t.TempDir())
	tr.Now = func() time.Time { return now }

	err := tr.Add(tracker.Task{
		Key:         "OFF-1",
		Project:     "OFF",
		Summary:     "Первая задача",
		Description: "Сделай что-нибудь полезное.",
		Status:      "Ready",
		Labels:      []string{"demo"},
	})
	if err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	return tr
}

func claim(tr *Tracker, runID string) error {
	return tr.Claim(tracker.ClaimRequest{
		Key:           "OFF-1",
		RunID:         runID,
		Owner:         "implementer",
		LeaseUntil:    now.Add(30 * time.Minute),
		ExpectStatus:  "Ready",
		WorkingStatus: "InProgress",
	})
}

// Рабочий статус — опция роли: без него захват записывает аренду и оставляет
// задачу там, где она лежит. Файловый трекер обязан вести себя как JIRA
// и здесь — иначе расхождение вылезет на живой доске, а не в тестах.
func TestClaimWithoutWorkingStatusKeepsColumn(t *testing.T) {
	tr := fixture(t)

	err := tr.Claim(tracker.ClaimRequest{
		Key: "OFF-1", RunID: "прогон-1", Owner: "reviewer",
		LeaseUntil: now.Add(30 * time.Minute), ExpectStatus: "Ready", WorkingStatus: "",
	})
	if err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	task := get(t, tr)
	if task.Status != "Ready" {
		t.Errorf("статус %q, ожидался прежний Ready", task.Status)
	}
	if !task.LeaseAlive(now) || task.RunID != "прогон-1" {
		t.Errorf("аренда не записана: %+v", task)
	}
}

func get(t *testing.T, tr *Tracker) tracker.Task {
	t.Helper()
	task, err := tr.Get("OFF-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	return task
}

func TestAddAndGet(t *testing.T) {
	tr := fixture(t)
	task := get(t, tr)

	if task.Summary != "Первая задача" || task.Status != "Ready" || task.Project != "OFF" {
		t.Errorf("задача прочитана как %+v", task)
	}
	if len(task.Labels) != 1 || task.Labels[0] != "demo" {
		t.Errorf("метки прочитаны как %v", task.Labels)
	}
	if task.LeaseAlive(now) {
		t.Error("у новой задачи откуда-то аренда")
	}

	if _, err := tr.Get("НЕТ-1"); !errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("отсутствующая задача дала %v, ожидалось ErrNotFound", err)
	}
}

func TestClaimTakesLeaseAndMovesTask(t *testing.T) {
	tr := fixture(t)
	if err := claim(tr, "прогон-1"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	task := get(t, tr)
	if !task.LeaseAlive(now) {
		t.Error("после захвата аренда не жива")
	}
	if task.RunID != "прогон-1" || task.Owner != "implementer" {
		t.Errorf("владелец %q/%q", task.Owner, task.RunID)
	}
	if task.Status != "InProgress" {
		t.Errorf("статус %q, ожидался InProgress", task.Status)
	}
}

// Захват задачи, уже уехавшей в другой статус, не наш.
func TestClaimChecksExpectedStatus(t *testing.T) {
	tr := fixture(t)
	err := tr.Claim(tracker.ClaimRequest{
		Key: "OFF-1", RunID: "прогон-1", Owner: "implementer",
		LeaseUntil: now.Add(time.Minute), ExpectStatus: "Review", WorkingStatus: "InProgress",
	})
	if !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("захват из чужого статуса дал %v, ожидалось ErrClaimLost", err)
	}
}

// Главная проверка реализации: два tick'а, дерущиеся за одну задачу, должны
// разойтись ровно одним победителем. Захват идёт переименованием файла аренды,
// поэтому здесь проверяется настоящий CAS, а не его изображение.
func TestConcurrentClaimHasSingleWinner(t *testing.T) {
	tr := fixture(t)

	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	start := make(chan struct{})

	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = claim(tr, "прогон-"+string(rune('A'+i)))
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
		case !errors.Is(err, tracker.ErrClaimLost):
			t.Errorf("проигравший %d получил %v, ожидалось ErrClaimLost", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("победителей %d, ожидался ровно один", winners)
	}

	// И у задачи после драки ровно один владелец, а не последний записавший.
	task := get(t, tr)
	if !task.LeaseAlive(now) || task.RunID == "" {
		t.Errorf("после драки аренда выглядит как %+v", task)
	}
}

// То же окно, но раскрытое вручную: конкурент захватывает задачу между нашим
// чтением и переименованием.
//
// Тест выше это ловит примерно раз на сотню прогонов — окно микросекундное.
// Такой охраны починке мало: правку, возвращающую перечитывание каталога вместо
// уже прочитанной аренды, сотня зелёных прогонов пропустит. Поэтому здесь
// порядок задаётся, а не вытанцовывается.
//
// Точка вклинивания — часы: `Claim` зовёт их один раз, после чтения задачи
// и до переименования. У конкурента часы свои, иначе вышла бы рекурсия.
func TestClaimDoesNotStealLeaseTakenAfterRead(t *testing.T) {
	tr := fixture(t)

	rival := New(tr.Root())
	rival.Now = func() time.Time { return now }

	var jumped bool
	tr.Now = func() time.Time {
		if !jumped {
			jumped = true
			if err := claim(rival, "конкурент"); err != nil {
				t.Errorf("конкурент не смог захватить свободную задачу: %v", err)
			}
		}
		return now
	}

	// Мы прочитали задачу свободной и об аренде конкурента не знаем: проверки
	// проходят по устаревшему снимку, и всё решает переименование.
	err := claim(tr, "опоздавший")
	if !errors.Is(err, tracker.ErrClaimLost) {
		t.Fatalf("захват поверх чужой живой аренды дал %v, ожидалось ErrClaimLost", err)
	}
	if !jumped {
		t.Fatal("конкурент не вклинился: часы больше не зовутся между чтением и переименованием")
	}

	task := get(t, tr)
	if task.RunID != "конкурент" {
		t.Errorf("аренда досталась %q, а её брал конкурент", task.RunID)
	}
}

// Истёкшая аренда свободна: задача снова в кандидатах и одновременно видна
// reaper'у. Иначе смерть раннера заперла бы задачу навсегда.
func TestExpiredLeaseIsFreeAgain(t *testing.T) {
	tr := fixture(t)
	if err := claim(tr, "мертвец"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	later := now.Add(time.Hour)
	tr.Now = func() time.Time { return later }

	ready, err := tr.ListReady("OFF", "InProgress")
	if err != nil {
		t.Fatalf("список кандидатов не прочитан: %v", err)
	}
	if len(ready) != 1 {
		t.Errorf("кандидатов %d, ожидался 1: истёкшая аренда считается свободной", len(ready))
	}

	expired, err := tr.ListExpired("OFF", later)
	if err != nil {
		t.Fatalf("список истёкших не прочитан: %v", err)
	}
	if len(expired) != 1 || expired[0].Key != "OFF-1" {
		t.Errorf("reaper нашёл %+v, ожидалась OFF-1", expired)
	}

	// А до истечения — ни там, ни там.
	tr.Now = func() time.Time { return now }
	if ready, _ := tr.ListReady("OFF", "InProgress"); len(ready) != 0 {
		t.Error("задача с живой арендой попала в кандидаты")
	}
	if expired, _ := tr.ListExpired("OFF", now); len(expired) != 0 {
		t.Error("живая аренда сочтена истёкшей")
	}
}

func TestListReadyFiltersByProjectAndStatus(t *testing.T) {
	tr := fixture(t)
	for _, task := range []tracker.Task{
		{Key: "OFF-2", Project: "OFF", Status: "Review", Summary: "не тот статус"},
		{Key: "OTH-1", Project: "OTH", Status: "Ready", Summary: "не тот проект"},
	} {
		if err := tr.Add(task); err != nil {
			t.Fatalf("задача не создана: %v", err)
		}
	}

	ready, err := tr.ListReady("OFF", "Ready")
	if err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}
	if len(ready) != 1 || ready[0].Key != "OFF-1" {
		t.Errorf("в кандидатах %+v, ожидалась только OFF-1", ready)
	}
}

// Правило владения — общее для всех трекеров, но проверять его обязана каждая
// реализация: mock, забывший про CheckOwner, пропустил бы гонку молча.
func TestMutationsFollowOwnership(t *testing.T) {
	alive := tracker.ByRun("прогон-1")
	foreign := tracker.ByRun("чужой")
	system := tracker.BySystem()

	cases := []struct {
		name    string
		who     tracker.Actor
		expired bool // сдвинуть часы за срок аренды
		want    error
	}{
		{name: "владелец живой аренды", who: alive},
		{name: "чужой прогон", who: foreign, want: tracker.ErrNotOwner},
		{name: "владелец после истечения", who: alive, expired: true, want: tracker.ErrNotOwner},
		{name: "системная операция при живой аренде", who: system, want: tracker.ErrNotOwner},
		{name: "системная операция при истёкшей", who: system, expired: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := fixture(t)
			if err := claim(tr, "прогон-1"); err != nil {
				t.Fatalf("захват не удался: %v", err)
			}
			if tc.expired {
				later := now.Add(time.Hour)
				tr.Now = func() time.Time { return later }
			}

			if err := tr.Transition("OFF-1", tc.who, "Ready"); !errors.Is(err, tc.want) {
				t.Errorf("Transition дал %v, ожидалось %v", err, tc.want)
			}
			if err := tr.Comment("OFF-1", tc.who, "тело"); !errors.Is(err, tc.want) {
				t.Errorf("Comment дал %v, ожидалось %v", err, tc.want)
			}
			if err := tr.SetAttempts("OFF-1", tc.who, 2); !errors.Is(err, tc.want) {
				t.Errorf("SetAttempts дал %v, ожидалось %v", err, tc.want)
			}
			if err := tr.SetHumanFlag("OFF-1", tc.who, true); !errors.Is(err, tc.want) {
				t.Errorf("SetHumanFlag дал %v, ожидалось %v", err, tc.want)
			}
		})
	}
}

// Прогон, у которого аренду отобрал reaper, не вправе писать в трекер, даже если
// аренды сейчас нет вовсе: задачу мог уже взять кто-то другой.
func TestRunWithoutLeaseIsNotOwner(t *testing.T) {
	tr := fixture(t)
	if err := claim(tr, "прогон-1"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}
	if err := tr.Release("OFF-1", tracker.ByRun("прогон-1")); err != nil {
		t.Fatalf("аренда не снята: %v", err)
	}

	if err := tr.Transition("OFF-1", tracker.ByRun("прогон-1"), "Review"); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("прогон без аренды сходил в трекер: %v", err)
	}
	// А системной операции ровно это и разрешено.
	if err := tr.Transition("OFF-1", tracker.BySystem(), "Ready"); err != nil {
		t.Errorf("системная операция при свободной задаче отклонена: %v", err)
	}
}

func TestRenewExtendsOnlyOwnLease(t *testing.T) {
	tr := fixture(t)
	if err := claim(tr, "прогон-1"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	if err := tr.Renew("OFF-1", "чужой", now.Add(time.Hour)); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("продление чужой аренды дало %v, ожидалось ErrNotOwner", err)
	}

	until := now.Add(2 * time.Hour)
	if err := tr.Renew("OFF-1", "прогон-1", until); err != nil {
		t.Fatalf("своя аренда не продлена: %v", err)
	}
	if task := get(t, tr); !task.LeaseUntil.Equal(until) {
		t.Errorf("срок аренды %s, ожидался %s", task.LeaseUntil, until)
	}

	// После снятия продлевать нечего.
	if err := tr.Release("OFF-1", tracker.ByRun("прогон-1")); err != nil {
		t.Fatalf("аренда не снята: %v", err)
	}
	if err := tr.Renew("OFF-1", "прогон-1", until); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("продление снятой аренды дало %v, ожидалось ErrNotOwner", err)
	}
}

// Release статуса не трогает: задачу двигает Transition, и порядок «комментарий,
// потом переход» не должен ломаться снятием аренды.
func TestReleaseKeepsStatus(t *testing.T) {
	tr := fixture(t)
	if err := claim(tr, "прогон-1"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}
	if err := tr.Release("OFF-1", tracker.ByRun("прогон-1")); err != nil {
		t.Fatalf("аренда не снята: %v", err)
	}

	task := get(t, tr)
	if task.Status != "InProgress" {
		t.Errorf("статус после Release %q, ожидался InProgress", task.Status)
	}
	if task.LeaseAlive(now) || task.RunID != "" {
		t.Errorf("аренда пережила Release: %+v", task)
	}
}

// `runner ls` показывает доску, а не очередь: задача с живой арендой — это ровно
// та, над которой сейчас работают, и прятать её значило бы не показать главного.
func TestListShowsBoardIncludingLeasedTasks(t *testing.T) {
	tr := fixture(t)
	if err := tr.Add(tracker.Task{Key: "OFF-2", Project: "OFF", Status: "Review", Summary: "Вторая"}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	if err := tr.Add(tracker.Task{Key: "OTH-1", Project: "OTH", Status: "Ready", Summary: "Чужая"}); err != nil {
		t.Fatalf("чужая задача не создана: %v", err)
	}
	if err := claim(tr, "прогон-1"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	refs, err := tr.List("OFF", []string{"Ready", "InProgress", "Review"})
	if err != nil {
		t.Fatalf("доска не прочитана: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("задач %d, ожидалось 2 (без чужого проекта): %+v", len(refs), refs)
	}

	byKey := map[string]tracker.TaskRef{}
	for _, ref := range refs {
		byKey[ref.Key] = ref
	}
	working := byKey["OFF-1"]
	if working.Owner != "implementer" || working.RunID != "прогон-1" {
		t.Errorf("владелец захваченной задачи потерян: %+v", working)
	}
	if !working.LeaseAlive(now) {
		t.Errorf("аренда не видна: %+v", working)
	}
	if working.Updated.IsZero() {
		t.Errorf("возраст задачи неизвестен: %+v", working)
	}
	if byKey["OFF-2"].RunID != "" {
		t.Errorf("у свободной задачи взялся владелец: %+v", byKey["OFF-2"])
	}
}

// Статус, которого не спрашивали, в ответ не попадает: `ls` показывает статусы
// графа, а не всё, что лежит в хранилище.
func TestListFiltersByStatus(t *testing.T) {
	tr := fixture(t)
	if err := tr.Add(tracker.Task{Key: "OFF-2", Project: "OFF", Status: "Done", Summary: "Смержена"}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}

	refs, err := tr.List("OFF", []string{"Ready"})
	if err != nil {
		t.Fatalf("доска не прочитана: %v", err)
	}
	if len(refs) != 1 || refs[0].Key != "OFF-1" {
		t.Errorf("отбор по статусам не сработал: %+v", refs)
	}
}

// Конфигурации у файлового трекера нет, поэтому учётка роли — соглашение об имени.
// Роль подписывается своей, системные записи — общей: так история читается человеком
// так же, как в JIRA с раздельными учётками.
func TestRoleSignsCommentsWithItsOwnAccount(t *testing.T) {
	tr := fixture(t)
	reviewer := tr.As(RoleAccount("reviewer"))
	if err := claim(reviewer, "прогон-1"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	if err := reviewer.Comment("OFF-1", tracker.ByRun("прогон-1"), "разобрал"); err != nil {
		t.Fatalf("комментарий не записан: %v", err)
	}

	if whoami, _ := reviewer.Whoami(); whoami != "office-reviewer" {
		t.Errorf("роль ходит под %q, ожидалась office-reviewer", whoami)
	}
	task := get(t, tr)
	if author := task.Comments[0].Author; author != "office-reviewer" {
		t.Errorf("комментарий подписан %q, ожидалась учётка роли", author)
	}
	// Хранилище одно на всех: подмена учётки не должна заводить второй трекер.
	if tr.Root() != reviewer.Root() {
		t.Errorf("учётка роли смотрит в другое хранилище: %q против %q", reviewer.Root(), tr.Root())
	}
}

func TestCommentsAreOrderedAndAttributed(t *testing.T) {
	tr := fixture(t)
	if err := claim(tr, "прогон-1"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	if err := tr.Comment("OFF-1", tracker.ByRun("прогон-1"), "первый"); err != nil {
		t.Fatalf("комментарий не записан: %v", err)
	}
	if err := tr.AddComment("OFF-1", "человек", "второй\nв две строки"); err != nil {
		t.Fatalf("комментарий человека не записан: %v", err)
	}
	if err := tr.Comment("OFF-1", tracker.ByRun("прогон-1"), "третий"); err != nil {
		t.Fatalf("комментарий не записан: %v", err)
	}

	task := get(t, tr)
	if len(task.Comments) != 3 {
		t.Fatalf("комментариев %d, ожидалось 3", len(task.Comments))
	}
	if task.Comments[0].Body != "первый" || task.Comments[2].Body != "третий" {
		t.Errorf("порядок комментариев нарушен: %+v", task.Comments)
	}
	if !strings.Contains(task.Comments[1].Body, "в две строки") {
		t.Errorf("многострочное тело потеряно: %q", task.Comments[1].Body)
	}

	account, err := tr.Whoami()
	if err != nil {
		t.Fatalf("учётка не прочитана: %v", err)
	}
	if task.Comments[0].Author != account {
		t.Errorf("автор %q, ожидалась учётка раннера %q", task.Comments[0].Author, account)
	}
	if task.Comments[1].Author != "человек" {
		t.Errorf("автор %q, ожидался человек", task.Comments[1].Author)
	}
	if task.Comments[0].Created.After(task.Comments[2].Created) {
		t.Error("время комментариев идёт вспять")
	}
}

// Ответ человека разбирается общим кодом контракта, но пройти он должен через
// настоящее хранилище: маркер, автор и порядок — всё из файлов.
func TestHumanReplyThroughStore(t *testing.T) {
	tr := fixture(t)
	if err := claim(tr, "прогон-1"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}
	account, _ := tr.Whoami()
	agents := []string{account}

	question := tracker.Marker{RunID: "прогон-1", Role: "implementer", Outcome: "needs_human", ConfigSHA: "5bc6a3b0"}
	if err := tr.Comment("OFF-1", tracker.ByRun("прогон-1"), tracker.ReportBody(question, runner.Result{Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human", Questions: []runner.Question{{ID: "Q1", Text: "Какую систему?"}}}, "", runner.Usage{})); err != nil {
		t.Fatalf("вопрос не записан: %v", err)
	}

	// Задав вопрос, прогон уходит: задача остаётся в Blocked без аренды,
	// и дальше с ней работают человек и системные операции.
	if err := tr.Release("OFF-1", tracker.ByRun("прогон-1")); err != nil {
		t.Fatalf("аренда не снята: %v", err)
	}

	if _, _, found := tracker.HumanReply(get(t, tr).Comments, agents); found {
		t.Error("ответ найден до того, как человек ответил")
	}

	if err := tr.AddComment("OFF-1", "человек", "Берём Stripe."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}
	reply, role, found := tracker.HumanReply(get(t, tr).Comments, agents)
	if !found {
		t.Fatal("ответ человека не найден")
	}
	if reply.Body != "Берём Stripe." {
		t.Errorf("ответ прочитан как %q", reply.Body)
	}
	if role != "implementer" {
		t.Errorf("задача вернётся роли %q, ожидалась implementer", role)
	}

	// Отметка о разборе закрывает вопрос: следующий tick не должен разбирать его снова.
	done := tracker.Marker{RunID: "прогон-2", Role: "implementer", Event: tracker.EventHumanReply, ConfigSHA: "5bc6a3b0"}
	if err := tr.Comment("OFF-1", tracker.BySystem(), tracker.NoticeBody(done, "Возвращаю в работу.")); err != nil {
		t.Fatalf("отметка не записана: %v", err)
	}
	if _, _, found := tracker.HumanReply(get(t, tr).Comments, agents); found {
		t.Error("разобранный ответ найден повторно")
	}
}

func TestSetAttemptsAndHumanFlag(t *testing.T) {
	tr := fixture(t)
	if err := claim(tr, "прогон-1"); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}
	who := tracker.ByRun("прогон-1")

	if err := tr.SetAttempts("OFF-1", who, 2); err != nil {
		t.Fatalf("счётчик не записан: %v", err)
	}
	if err := tr.SetHumanFlag("OFF-1", who, true); err != nil {
		t.Fatalf("флаг не записан: %v", err)
	}

	task := get(t, tr)
	if task.Attempts != 2 || !task.HumanFlag {
		t.Errorf("прочитано attempts=%d human_flag=%v", task.Attempts, task.HumanFlag)
	}
	// Счётчик попыток нужен раннеру при выборе задачи, значит виден в списке.
	ready, err := tr.ListReady("OFF", "InProgress")
	if err != nil {
		t.Fatalf("список не прочитан: %v", err)
	}
	if len(ready) != 0 {
		t.Fatalf("задача с живой арендой попала в кандидаты: %+v", ready)
	}
	if err := tr.Release("OFF-1", tracker.ByRun("прогон-1")); err != nil {
		t.Fatalf("аренда не снята: %v", err)
	}
	if ready, _ = tr.ListReady("OFF", "InProgress"); len(ready) != 1 || ready[0].Attempts != 2 {
		t.Errorf("в кандидатах %+v, ожидались попытки 2", ready)
	}
}

// Круг «записали — прочитали» на теле отчёта. У файлового трекера перевода
// разметки нет вовсе — markdown в нём читают глазами, — и тело обязано доехать
// целиком, не только его машинные куски.
func TestReportSurvivesRoundTrip(t *testing.T) {
	tr := fixture(t)
	if err := claim(tr, "прогон-1"); err != nil {
		t.Fatalf("задача не захвачена: %v", err)
	}

	marker := tracker.Marker{
		RunID: "abc12345", Role: "reviewer", Outcome: "needs_human",
		Next: "human", ConfigSHA: "9f2e1c",
	}
	res := runner.Result{
		Outcome:   "needs_human",
		Summary:   "Разбор упёрся в **вопрос** к человеку.",
		DetailsMD: "## Что смотрел\n\n- `pipeline/prpass.go`\n",
		Questions: []runner.Question{{
			ID: "Q1", Text: "Идемпотентность или скорость?",
			Options: []runner.Option{{ID: "a", Label: "идемпотентность"}, {ID: "b", Label: "скорость"}},
		}},
		Artifacts: []string{"https://github.com/kao73/office-pr-probe/pull/1"},
		NextOwner: "human",
	}
	body := tracker.ReportBody(marker, res, "agent/OFF-1", runner.Usage{CostUSD: 0.21, DurationMS: 40000, Turns: 12})

	if err := tr.Comment("OFF-1", tracker.ByRun("прогон-1"), body); err != nil {
		t.Fatalf("комментарий не записан: %v", err)
	}
	task, err := tr.Get("OFF-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	stored := task.Comments[0].Body

	if strings.TrimRight(stored, "\n") != strings.TrimRight(body, "\n") {
		t.Errorf("тело изменилось в пути:\n%s\nотправляли:\n%s", stored, body)
	}
	back, ok := tracker.MarkerOf(stored)
	if !ok || back != marker {
		t.Errorf("маркер после круга: %+v (ok=%v), отправляли %+v", back, ok, marker)
	}
	questions := tracker.ParseQuestions(stored)
	if len(questions) != 1 || questions[0].ID != "Q1" || len(questions[0].Options) != 2 {
		t.Errorf("вопросы после круга: %+v", questions)
	}
	if !strings.Contains(stored, "https://github.com/kao73/office-pr-probe/pull/1") {
		t.Errorf("адрес в артефактах изменился:\n%s", stored)
	}
}

func TestCreateTaskThenFindByMarker(t *testing.T) {
	tr := fixture(t) // OFF-1 уже есть; следующий ключ — OFF-2

	ref, err := tr.CreateTask("OFF", tracker.TaskInput{
		Summary: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
		Labels: []string{"split-child:OFF-1:category-crud"},
	})
	if err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	if ref.Key != "OFF-2" {
		t.Errorf("ключ %q, ожидался OFF-2", ref.Key)
	}
	if ref.Status != "Analysis" {
		t.Errorf("статус %q, ожидался Analysis", ref.Status)
	}

	found, err := tr.FindByMarker("OFF", "split-child:OFF-1:category-crud")
	if err != nil {
		t.Fatalf("поиск по метке не удался: %v", err)
	}
	if len(found) != 1 || found[0].Key != "OFF-2" {
		t.Errorf("найдено %+v, ожидалась одна OFF-2", found)
	}
}

func TestFindByMarkerEmptyWhenNoneMatch(t *testing.T) {
	tr := fixture(t)
	found, err := tr.FindByMarker("OFF", "split-child:OFF-1:none")
	if err != nil {
		t.Fatalf("поиск по метке не удался: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("найдено %+v, ожидался пустой список", found)
	}
}

func TestCreateTaskCollisionFailsInsteadOfOverwriting(t *testing.T) {
	tr := fixture(t)
	if err := os.Mkdir(filepath.Join(tr.Root(), "OFF-2"), 0o755); err != nil {
		t.Fatalf("подготовка не удалась: %v", err)
	}

	if _, err := tr.CreateTask("OFF", tracker.TaskInput{Summary: "x", Description: "y"}); err == nil {
		t.Error("коллизия ключа с уже существующим каталогом не замечена")
	}
}
